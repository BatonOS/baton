// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/batonos/baton/core/agent/internal/runtimespec"
)


const tmuxBinary = "tmux"





const (
	tmuxSocket  = "runtime"
	tmuxSession = "runtime"
)





const minPollInterval = time.Second





















type tmuxRunner struct {
	spec *runtimespec.Spec
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	started bool
	done    chan Exit
	stopped chan struct{}



	deadSince time.Time
}

func newTmuxRunner(spec *runtimespec.Spec, opts Options) *tmuxRunner {
	return &tmuxRunner{spec: spec, opts: opts, log: opts.Logger}
}

func (t *tmuxRunner) Describe() string {
	return "tmux session " + tmuxSession + ": " + strings.Join(t.spec.Package.Command, " ")
}


func TmuxAvailable() bool {
	return exec.Command(tmuxBinary, "-V").Run() == nil
}

func (t *tmuxRunner) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.started {
		return errors.New("supervisor: the runtime session is already running")
	}

	if !TmuxAvailable() {




		return errors.New("supervisor: this runtime declares session: tty, which needs tmux " +
			"inside the agent-node, and this image does not have it (the distroless variant " +
			"has no tmux). Rebuild with the alpine variant, or set session: headless in the spec")
	}

	argv := t.spec.Package.Command
	if len(argv) == 0 {
		return errors.New("supervisor: the spec has no command")
	}

	env, err := t.environment()
	if err != nil {
		return err
	}



	_, _ = t.tmux(ctx, nil, "kill-server")

	conf, err := t.writeConfig()
	if err != nil {
		return err
	}












	args := []string{"-f", conf, "new-session", "-d", "-s", tmuxSession, "-x", "200", "-y", "50"}
	if dir := t.spec.Package.WorkingDir; dir != "" {
		args = append(args, "-c", dir)






		if home, err := runtimeHome(); err == nil {
			exposeInWorkspace(t.log, dir, home, t.spec.Adapter.PersistedPaths())
		}
	}





	args = append(args, "--")
	args = append(args, argv...)





	if _, err := t.tmux(ctx, env, args...); err != nil {
		_, _ = t.tmux(ctx, nil, "kill-server")
		return fmt.Errorf("supervisor: start %s in tmux: %w", argv[0], err)
	}

	pid, err := t.panePID(ctx)
	if err != nil {
		_, _ = t.tmux(ctx, nil, "kill-server")
		return fmt.Errorf("supervisor: the runtime pane did not start: %w", err)
	}

	t.started = true
	done := make(chan Exit, 1)
	stopped := make(chan struct{})
	t.done, t.stopped = done, stopped

	oomBefore, oomReadable := oomKills()
	go t.watch(done, stopped, oomBefore, oomReadable)

	t.log.Info("runtime started", "pid", pid, "command", argv[0], "session", tmuxSession)
	return nil
}


func (t *tmuxRunner) watch(done chan<- Exit, stopped <-chan struct{}, oomBefore int64, oomReadable bool) {
	interval := t.spec.Adapter.Health.Interval()
	if interval > minPollInterval {



		interval = minPollInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopped:
			return
		case <-ticker.C:
		}

		ex, dead := t.paneExit()
		if !dead {
			continue
		}
		if oomReadable {
			if after, ok := oomKills(); ok && after > oomBefore {
				ex.OOMKilled = true
			}
		}
		t.mu.Lock()
		t.started = false
		t.mu.Unlock()
		done <- ex
		close(done)
		return
	}
}






func (t *tmuxRunner) paneExit() (Exit, bool) {











	const sep = "|"
	out, err := t.tmux(context.Background(), nil, "list-panes", "-t", tmuxSession,
		"-F", "#{pane_dead}"+sep+"#{pane_dead_status}"+sep+"#{pane_dead_signal}")
	if err != nil {


		return Exit{At: time.Now().UTC(), Code: -1}, true
	}

	line := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	ex, dead, reported := parsePaneLine(line)
	if !dead {
		t.deadSince = time.Time{}
		return Exit{}, false
	}
	if !reported {















		if t.deadSince.IsZero() {
			t.deadSince = time.Now()
			return Exit{}, false
		}
		if time.Since(t.deadSince) < 2*time.Second {
			return Exit{}, false
		}
		t.log.Warn("tmux reported the pane dead but never its status; recording the exit as unknown", "line", line)
		return Exit{At: time.Now().UTC(), Code: -1}, true
	}
	t.deadSince = time.Time{}
	ex.At = time.Now().UTC()
	return ex, true
}






func parsePaneLine(line string) (ex Exit, dead bool, reported bool) {
	const sep = "|"
	fields := strings.Split(line, sep)
	if len(fields) < 3 || fields[0] != "1" {
		return Exit{}, false, false
	}
	status := strings.TrimSpace(fields[1])
	sig := strings.TrimSpace(fields[2])
	if status == "" && sig == "" {
		return Exit{}, true, false
	}
	if code, err := strconv.Atoi(status); err == nil {
		ex.Code = code
	}


	if n, err := strconv.Atoi(sig); err == nil && n > 0 {
		ex.Signal = signalName(n)
		ex.Code = 128 + n
	}
	return ex, true, true
}

func (t *tmuxRunner) panePID(ctx context.Context) (int, error) {
	out, err := t.tmux(ctx, nil, "list-panes", "-t", tmuxSession, "-F", "#{pane_pid}")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(strings.Split(out, "\n")[0]))
}






func (t *tmuxRunner) Signal(sig syscall.Signal) error {
	t.mu.Lock()
	started := t.started
	t.mu.Unlock()
	if !started {
		return ErrNotRunning
	}

	pid, err := t.panePID(context.Background())
	if err != nil {
		return fmt.Errorf("supervisor: find the runtime process: %w", err)
	}
	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("supervisor: signal %v: %w", sig, err)
	}
	return nil
}

func (t *tmuxRunner) Wait(ctx context.Context) (Exit, error) {
	t.mu.Lock()
	done := t.done
	t.mu.Unlock()

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
		t.mu.Lock()
		t.done = nil
		t.mu.Unlock()
		return ex, nil
	}
}






func (t *tmuxRunner) Stop(ctx context.Context, grace time.Duration) error {
	t.mu.Lock()
	started, done, stopped := t.started, t.done, t.stopped
	t.mu.Unlock()

	if !started {
		return nil
	}

	if pid, err := t.panePID(ctx); err == nil {
		signalGroup(pid, t.log)

		select {
		case <-done:
			t.teardown(stopped)
			return nil
		case <-time.After(grace):
		case <-ctx.Done():
		}

		t.log.Warn("runtime did not stop within its grace period; killing",
			"pgid", pid, "grace", grace.String())
		killGroup(pid, t.log)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.log.Warn("the runtime pane did not report an exit after SIGKILL")
		}
	}

	t.teardown(stopped)
	return nil
}






func (t *tmuxRunner) teardown(stopped chan struct{}) {
	if stopped != nil {
		select {
		case <-stopped:
		default:
			close(stopped)
		}
	}

	t.mu.Lock()
	t.started = false
	t.stopped = nil
	t.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = t.tmux(ctx, nil, "kill-server")
}




func (t *tmuxRunner) environment() ([]string, error) {
	return buildEnvironment(t.spec)
}
















func (t *tmuxRunner) writeConfig() (string, error) {
	path := filepath.Join("/tmp", "baton-runtime.conf")
	body := "set -g exit-empty off\n" +
		"set -gw remain-on-exit on\n" +
		"set -g history-limit 20000\n" +
		"set -g status off\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", fmt.Errorf("supervisor: write tmux config: %w", err)
	}
	return path, nil
}


func (t *tmuxRunner) tmux(ctx context.Context, env []string, args ...string) (string, error) {
	argv := append([]string{"-L", tmuxSocket}, args...)
	cmd := exec.CommandContext(ctx, tmuxBinary, argv...)
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}
