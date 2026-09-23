// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/batonos/baton/core/agent/internal/runtimespec"

	"github.com/batonos/baton/core/agent/internal/wscontract"
)


type Options struct {
	Logger *slog.Logger











	HasPendingMail func() bool


	SecretDir string

	Stdout io.Writer
	Stderr io.Writer

	NodeName string


















	FetchFacility func(ctx context.Context, ref string) (archive []byte, digest, id string, err error)
}


type processRunner struct {
	spec *runtimespec.Spec
	opts Options
	log  *slog.Logger



	extraEnv []string

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan Exit
}

func newProcessRunner(spec *runtimespec.Spec, opts Options) *processRunner {
	return &processRunner{spec: spec, opts: opts, log: opts.Logger}
}


func (p *processRunner) pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *processRunner) Describe() string {
	return "process " + strings.Join(p.spec.Package.Command, " ")
}

func (p *processRunner) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cmd != nil && p.cmd.Process != nil
}

func (p *processRunner) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd != nil {
		return errors.New("supervisor: the process is already running")
	}

	argv := p.spec.Package.Command
	if len(argv) == 0 {
		return errors.New("supervisor: the spec has no command")
	}

	env, err := p.environment()
	if err != nil {
		return err
	}





	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Args = argv
	cmd.Env = env
	cmd.Stdout = p.opts.Stdout
	cmd.Stderr = p.opts.Stderr
	if dir := p.spec.Package.WorkingDir; dir != "" {
		cmd.Dir = dir



		if home, err := runtimeHome(); err == nil {
			exposeInWorkspace(p.log, dir, home, p.spec.Adapter.PersistedPaths())
		}
	}




	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

















	if u := strings.TrimSpace(p.spec.Execution.User); u != "" {
		cred, err := credentialFor(u)
		if err != nil {
			return fmt.Errorf("supervisor: execution.user %q: %w", u, err)
		}
		if os.Geteuid() != 0 {
			return fmt.Errorf(
				"supervisor: this spec asks the runtime to run as uid %s, and this container's "+
					"PID 1 is uid %d, which cannot drop to another id (the shipped agent image deliberately "+
					"keeps its non-root PID 1 rather than give up that hardening, so it cannot offer this; "+
					"the dev image, whose PID 1 is root, can)", u, os.Geteuid())
		}
		cmd.SysProcAttr.Credential = cred
	}






















	if u := strings.TrimSpace(p.spec.Execution.User); u != "" && os.Geteuid() == 0 {
		if cred, err := credentialFor(u); err == nil {
			if dir := p.spec.Adapter.Workspace.MountPath; dir != "" {
				if err := chownWorkspace(dir, int(cred.Uid), int(cred.Gid)); err != nil {
					p.log.Warn("could not give the workspace to the runtime's uid",
						"path", dir, "uid", cred.Uid, "error", err)
				}
			}




			if p.spec.Adapter.Status.Declared() {
				dir, _ := StatusPaths(envOr("BATON_DATA_DIR", DefaultDataDir))
				if err := os.Chown(dir, int(cred.Uid), int(cred.Gid)); err != nil {
					p.log.Warn("could not give the status directory to the runtime's uid",
						"path", dir, "uid", cred.Uid, "error", err)
				}
			}




			_ = p.spec.Adapter.Skills.MountPath
		}
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("supervisor: start %s: %w", argv[0], err)
	}

	p.cmd = cmd
	done := make(chan Exit, 1)
	p.done = done



	oomBefore, oomReadable := oomKills()



	go func() {
		err := cmd.Wait()
		ex := exitFrom(err, cmd)
		if oomReadable {
			if after, ok := oomKills(); ok && after > oomBefore {
				ex.OOMKilled = true
			}
		}
		done <- ex
		close(done)
	}()

	p.log.Info("runtime started", "pid", cmd.Process.Pid, "command", argv[0])
	return nil
}

func (p *processRunner) environment() ([]string, error) {
	env, err := buildEnvironment(p.spec)
	if err != nil {
		return nil, err
	}
	return mergeEnv(env, p.extraEnv), nil
}




func mergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	idx := map[string]int{}
	out := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		idx[kv[:strings.IndexByte(kv, '=')]] = len(out)
		out = append(out, kv)
	}
	for _, kv := range extra {
		k := kv[:strings.IndexByte(kv, '=')]
		if i, ok := idx[k]; ok {
			out[i] = kv
			continue
		}
		idx[k] = len(out)
		out = append(out, kv)
	}
	return out
}









func buildEnvironment(spec *runtimespec.Spec) ([]string, error) {
	home, err := runtimeHome()
	if err != nil {
		return nil, err
	}

	env := []string{
		"PATH=" + envOr("PATH", "/usr/local/bin:/usr/bin:/bin"),
		"HOME=" + home,


		"TERM=" + envOr("TERM", "xterm-256color"),
	}




	if ws := spec.Adapter.Workspace.MountPath; ws != "" {
		env = append(env, "BATON_WORKSPACE="+ws)
	}













	env = append(env, "BATON_DATA_DIR="+envOr("BATON_DATA_DIR", DefaultDataDir))








	if spec.Adapter.Status.Declared() {
		dir, file := StatusPaths(envOr("BATON_DATA_DIR", DefaultDataDir))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("supervisor: create status dir %s: %w", dir, err)
		}
		env = append(env, "BATON_RUNTIME_STATUS_FILE="+file)
	}
	for _, e := range spec.Execution.Env {
		env = append(env, e.Name+"="+e.Value)
	}











	for _, s := range spec.Execution.Secrets {
		if _, ok := s.FilePath(); !ok {
			return nil, fmt.Errorf("supervisor: secret %s has a non-file source", s.Name)
		}
		path := s.Path()
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf(
				"supervisor: secret %s is not mounted at %s inside this container: %w",
				s.Name, path, err)
		}

		env = append(env, s.Name+"_FILE="+path)
	}
	return env, nil
}
















const RuntimeHomeDir = "home"






const DefaultDataDir = "/var/lib/baton"







func runtimeHome() (string, error) {
	dir := filepath.Join(envOr("BATON_DATA_DIR", DefaultDataDir), RuntimeHomeDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("supervisor: create runtime home %s: %w", dir, err)
	}
	return dir, nil
}





















func exposeInWorkspace(log *slog.Logger, workspace string, home string, paths []string) {
	if workspace == "" {
		return
	}




	declared := make(map[string]bool, len(paths))
	for _, target := range paths {
		if target == "" {
			continue
		}
		abs := target
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(home, target)
		}
		declared[filepath.Base(abs)] = true
	}
	defer sweepUndeclaredLinks(log, workspace, declared)
	for _, target := range paths {
		if target == "" {
			continue
		}


		abs := target
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(home, target)
		}
		if err := os.MkdirAll(abs, 0o700); err != nil {
			log.Warn("could not prepare a persisted path", "path", abs, "error", err)
			continue
		}
		link := filepath.Join(workspace, filepath.Base(abs))



		if fi, err := os.Lstat(link); err == nil {
			if fi.Mode()&os.ModeSymlink == 0 {
				log.Warn("not linking: a real path is already there",
					"link", link, "target", abs)
				continue
			}
			_ = os.Remove(link)
		}
		if err := os.Symlink(abs, link); err != nil {
			log.Warn("could not link a persisted path into the workspace",
				"link", link, "target", abs, "error", err)
			continue
		}
		log.Info("persisted path exposed in the workspace", "link", link, "target", abs)
	}
}


































func sweepUndeclaredLinks(log *slog.Logger, workspace string, declared map[string]bool) {
	entries, err := os.ReadDir(workspace)
	if err != nil {
		log.Warn("could not read the workspace to look for links a previous runtime left",
			"workspace", workspace, "error", err)
		return
	}
	root := envOr("BATON_DATA_DIR", DefaultDataDir)
	for _, e := range entries {
		name := e.Name()
		if declared[name] {
			continue
		}
		link := filepath.Join(workspace, name)
		fi, err := os.Lstat(link)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(link)
		if err != nil {
			log.Warn("could not read a workspace link", "link", link, "error", err)
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(workspace, target)
		}
		if !isUnder(target, root) {
			continue
		}
		if err := os.Remove(link); err != nil {
			log.Warn("could not remove a link the running runtime does not declare",
				"link", link, "target", target, "error", err)
			continue
		}



		log.Info("removed a workspace link the running runtime does not declare",
			"link", link, "target", target)
	}
}






func isUnder(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func (p *processRunner) Signal(sig syscall.Signal) error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return ErrNotRunning
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("supervisor: signal %v: %w", sig, err)
	}
	return nil
}

func (p *processRunner) Wait(ctx context.Context) (Exit, error) {
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()

	if done == nil {
		return Exit{}, ErrNotRunning
	}

	select {
	case <-ctx.Done():
		return Exit{}, ctx.Err()
	case ex, ok := <-done:
		if !ok {
			return Exit{}, ErrNotRunning
		}
		p.mu.Lock()
		p.cmd, p.done = nil, nil
		p.mu.Unlock()
		return ex, nil
	}
}





func (p *processRunner) Stop(ctx context.Context, grace time.Duration) error {
	p.mu.Lock()
	cmd, done := p.cmd, p.done
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	pgid := cmd.Process.Pid
	signalGroup(pgid, p.log)

	select {
	case <-done:
		return nil
	case <-time.After(grace):
	case <-ctx.Done():
	}

	p.log.Warn("runtime did not stop within its grace period; killing",
		"pgid", pgid, "grace", grace.String())
	killGroup(pgid, p.log)



	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("supervisor: process %d did not exit after SIGKILL", pgid)
	}
	return nil
}







func signalGroup(pgid int, log *slog.Logger) {
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		log.Warn("sending SIGTERM", "pgid", pgid, "error", err)
	}
}

func killGroup(pgid int, log *slog.Logger) {
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		log.Warn("sending SIGKILL", "pgid", pgid, "error", err)
	}
}






func signalName(sig int) string { return syscall.Signal(sig).String() }


func exitFrom(err error, cmd *exec.Cmd) Exit {
	ex := Exit{At: time.Now().UTC()}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		ex.Code = 0
	case errors.As(err, &exitErr):
		ex.Code = exitErr.ExitCode()
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			ex.Signal = status.Signal().String()


			ex.Code = 128 + int(status.Signal())
		}
	default:
		ex.Code = -1
	}
	return ex
}











func oomKills() (int64, bool) {
	raw, err := os.ReadFile("/sys/fs/cgroup/memory.events")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		field, value, found := strings.Cut(strings.TrimSpace(line), " ")
		if !found || field != "oom_kill" {
			continue
		}
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}







func credentialFor(user string) (*syscall.Credential, error) {
	uidStr, gidStr, hasGid := strings.Cut(user, ":")
	uid, err := strconv.ParseUint(uidStr, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("not a numeric uid: %w", err)
	}
	gid := uid
	if hasGid {
		g, err := strconv.ParseUint(gidStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("not a numeric gid: %w", err)
		}
		gid = g
	}
	if uid == 0 {




		return nil, fmt.Errorf("uid 0 is the container's root, which owns everything it can see")
	}
	return &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, nil
}





















func chownWorkspace(root string, uid, gid int) error {
	baton := filepath.Join(root, wscontract.BatonDir)
	writable := runtimeWritablePath(root)

	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == baton {



			if lerr := os.Lchown(path, uid, gid); lerr != nil {
				return lerr
			}
			return chownWritableBranch(baton, writable, uid, gid)
		}
		if strings.HasPrefix(path, baton+string(os.PathSeparator)) {
			return nil
		}



		_ = info
		return os.Lchown(path, uid, gid)
	})
}










func chownWritableBranch(baton, writable string, uid, gid int) error {
	rel, err := filepath.Rel(baton, writable)
	if err != nil {
		return err
	}
	at := baton
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		at = filepath.Join(at, part)
		if _, serr := os.Stat(at); serr != nil {




			return fmt.Errorf("supervisor: %s is missing; the workspace gate did not run", at)
		}
		if lerr := os.Lchown(at, uid, gid); lerr != nil {
			return lerr
		}
	}
	return filepath.Walk(writable, func(path string, _ os.FileInfo, werr error) error {
		if werr != nil {
			return werr
		}
		return os.Lchown(path, uid, gid)
	})
}

