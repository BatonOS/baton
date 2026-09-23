// SPDX-License-Identifier: Apache-2.0

package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)




const hookTimeout = 60 * time.Second











type Manager struct {


	Root string

	InventoryPath string


	Workspace string



	MountPath string




	Discovery   string
	RuntimePath string


	StateDir string

	Env []string









	Supplier string
	Log *slog.Logger
}


type Exposure string

const (
	ExposureSymlink Exposure = "symlink"




	ExposureExternalDirs Exposure = "external-dirs"


	ExposureUnknown Exposure = "unknown"

	ExposureRefused Exposure = "refused"
)


type SkillReport struct {
	Name   string   `json:"name"`
	Source string   `json:"source"`
	State  Exposure `json:"state"`
	At     string   `json:"at,omitempty"`
	Why    string   `json:"why,omitempty"`
}


type StateReport struct {
	Path      string    `json:"path"`
	BelongsTo BelongsTo `json:"belongs_to"`
	Position  string    `json:"position"`
	At        string    `json:"at,omitempty"`
	Why       string    `json:"why,omitempty"`
}


type PluginReport struct {
	ID          string        `json:"id"`
	Version     string        `json:"version"`
	Dir         string        `json:"dir"`
	Trust       Class         `json:"trust"`
	TrustReason string        `json:"trust_reason"`




	Disabled bool `json:"disabled,omitempty"`
	State       []StateReport `json:"state,omitempty"`
	Skills      []SkillReport `json:"skills,omitempty"`


	Hook string `json:"hook"`



	Instructions string `json:"instructions"`






	InstructionsPath string `json:"instructions_path"`



	InstructionsSHA256 string `json:"instructions_sha256,omitempty"`






	Service *ServiceReport `json:"service,omitempty"`
}









type ServiceReport struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`

	User string `json:"user,omitempty"`


	Outbox string `json:"outbox,omitempty"`





	Isolation string `json:"isolation"`

	PID int `json:"pid,omitempty"`
}


const (
	ServiceUnknown     = "unknown"
	ServiceRefusedDoor = "refused (door)"
)







type Report struct {
	Plugins []PluginReport `json:"plugins"`





	Services map[string]*ServiceDecl `json:"-"`




	Assigned []SkillReport `json:"assigned,omitempty"`
	Refused  []string      `json:"refused,omitempty"`




	Withdrawn []string `json:"withdrawn,omitempty"`








	Retired map[string][]string `json:"retired"`
	Summary Summary             `json:"summary"`
}


type Summary struct {
	Plugins       int `json:"plugins"`
	SkillsExposed int `json:"skills_exposed"`
	SkillsUnknown int `json:"skills_unknown"`
	SkillsRefused int `json:"skills_refused"`
	HooksRan      int `json:"hooks_ran"`
	HooksRefused  int `json:"hooks_refused"`
	Unreadable    int `json:"unreadable"`


	Retired int `json:"retired"`
}


func (s Summary) Line() string {
	return fmt.Sprintf("%d plugins, %d skills exposed, %d unknown, %d refused, %d hooks ran, %d hooks refused, %d unreadable, %d retired",
		s.Plugins, s.SkillsExposed, s.SkillsUnknown, s.SkillsRefused, s.HooksRan, s.HooksRefused, s.Unreadable, s.Retired)
}






func (m *Manager) Reconcile(ctx context.Context) (Report, error) {
	rep := Report{Plugins: []PluginReport{}, Services: map[string]*ServiceDecl{}}






	prev, prevErr := m.readPrevious()

	inv, err := loadInventory(m.InventoryPath)
	if err != nil {
		return rep, fmt.Errorf("read %s: %w", m.InventoryPath, err)
	}

	dirs, err := m.discover()
	if err != nil {
		return rep, err
	}






	taken := map[string]string{}
	rep.Assigned = m.exposeAssigned(taken)

	for _, dir := range dirs {
		pr, ok := m.reconcileOne(ctx, dir, inv, taken, &rep, prev)
		if !ok {
			continue
		}
		rep.Plugins = append(rep.Plugins, pr)
	}
	dangling := m.withdrawDangling()
	for _, w := range dangling {
		rep.Withdrawn = append(rep.Withdrawn, w.line)
	}
	rep.Retired = m.retireGone(prev, prevErr, dangling, &rep)

	rep.Summary.Plugins = len(rep.Plugins)
	rep.Summary.Unreadable = len(rep.Refused)
	for _, s := range rep.Assigned {
		m.count(&rep.Summary, s.State)
	}
	for _, p := range rep.Plugins {
		for _, s := range p.Skills {
			m.count(&rep.Summary, s.State)
		}
		switch {
		case strings.HasPrefix(p.Hook, "refused"):
			rep.Summary.HooksRefused++
		case p.Hook == "ran":
			rep.Summary.HooksRan++
		}
	}
	if err := m.writeReport(rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func (m *Manager) count(s *Summary, e Exposure) {
	switch e {
	case ExposureSymlink, ExposureExternalDirs:
		s.SkillsExposed++
	case ExposureUnknown:
		s.SkillsUnknown++
	case ExposureRefused:
		s.SkillsRefused++
	}
}




func (m *Manager) discover() ([]string, error) {
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", m.Root, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(m.Root, e.Name())
		if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err != nil {
			continue
		}
		out = append(out, dir)
	}
	sort.Strings(out)
	return out, nil
}

func (m *Manager) reconcileOne(ctx context.Context, dir string, inv inventory, taken map[string]string, rep *Report, prev Report) (PluginReport, bool) {
	name := filepath.Base(dir)
	raw, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		rep.Refused = append(rep.Refused, fmt.Sprintf("%s: %v", name, err))
		return PluginReport{}, false
	}
	man, err := Parse(raw, filepath.Join(name, ManifestFile))
	if err != nil {



		rep.Refused = append(rep.Refused, err.Error())
		return PluginReport{}, false
	}



	if man.Metadata.ID != name {
		rep.Refused = append(rep.Refused,
			fmt.Sprintf("%s: the directory is %q and the manifest says id %q — a plugin answers to where it was installed", name, name, man.Metadata.ID))
		return PluginReport{}, false
	}

	class, why := classify(dir, name, inv)
	pr := PluginReport{
		ID: man.Metadata.ID, Version: man.Metadata.Version, Dir: dir,
		Trust: class, TrustReason: why, Hook: "none",
	}


















	if IsDisabled(m.StateDir, man.Metadata.ID) {
		pr.Disabled = true
		pr.Hook, pr.Instructions = "skipped: disabled", "skipped: disabled"
		for i := range prev.Plugins {
			if prev.Plugins[i].ID == man.Metadata.ID {
				rep.Withdrawn = append(rep.Withdrawn, m.withdrawSkillLinks(prev.Plugins[i], "disabled")...)
				break
			}
		}
		return pr, true
	}

	pr.State = m.materialiseState(man)
	pr.Skills = m.exposePluginSkills(man, dir, taken)
	pr.Instructions, pr.InstructionsPath, pr.InstructionsSHA256 = m.applyInstructions(dir, man)
	pr.Hook = m.runHook(ctx, man, dir, class)
	if man.HasService() {




		if class != ClassSystem {
			pr.Service = &ServiceReport{Status: ServiceRefusedDoor, Reason: why, User: man.Service.User, Isolation: ServiceUnknown}
		} else {
			pr.Service = &ServiceReport{Status: "pending", User: man.Service.User, Isolation: ServiceUnknown}
			rep.Services[man.Metadata.ID] = man.Service
		}
	}
	return pr, true
}





func (m *Manager) UpdateService(id string, sr ServiceReport) error {
	rep, err := m.readPrevious()
	if err != nil {
		return err
	}
	for i := range rep.Plugins {
		if rep.Plugins[i].ID == id {
			rep.Plugins[i].Service = &sr
			return m.writeReport(rep)
		}
	}
	return nil
}








func (m *Manager) materialiseState(man *Manifest) []StateReport {
	out := make([]StateReport, 0, len(man.State))
	for _, s := range man.State {
		position := "portable"
		if s.BelongsTo == BelongsToOffice {
			position = "local"
		}
		r := StateReport{Path: s.Path, BelongsTo: s.BelongsTo, Position: position}
		if m.Workspace == "" {
			r.Why = "this runtime has no workspace, so there is nowhere for state to be"
			out = append(out, r)
			continue
		}
		at := filepath.Join(m.Workspace, ".baton", position, "ext", man.Metadata.ID, filepath.FromSlash(s.Path))
		if err := os.MkdirAll(at, 0o700); err != nil {
			r.Why = err.Error()
		} else {
			r.At = at
		}
		out = append(out, r)
	}
	return out
}









func (m *Manager) exposeAssigned(taken map[string]string) []SkillReport {
	if m.MountPath == "" {
		return nil
	}
	entries, err := os.ReadDir(m.MountPath)
	if err != nil {
		return nil
	}



	stocked := readStocked(m.MountPath)
	var out []SkillReport
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}




		source := "assigned"
		if t, ok := stocked[e.Name()]; ok {
			source = "template:" + t
		}
		r := SkillReport{Name: e.Name(), Source: source}
		r.State, r.At, r.Why = m.link(filepath.Join(m.MountPath, e.Name()), e.Name(), taken, source)
		out = append(out, r)
	}
	return out
}








const StockedFile = ".stocked"














func readStocked(mount string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(filepath.Join(mount, StockedFile))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		name, tmpl, ok := strings.Cut(strings.TrimSpace(line), "  ")
		if !ok || name == "" {
			continue
		}
		out[name] = strings.TrimSpace(tmpl)
	}
	return out
}

func (m *Manager) exposePluginSkills(man *Manifest, dir string, taken map[string]string) []SkillReport {
	out := make([]SkillReport, 0, len(man.Skills))
	for _, s := range man.Skills {
		if s.State != "" {
			out = append(out, m.exposeStateSkills(man, s, taken)...)
			continue
		}
		name := filepath.Base(filepath.Clean(s.Path))
		src := filepath.Join(dir, filepath.FromSlash(s.Path))
		r := SkillReport{Name: name, Source: "plugin:" + man.Metadata.ID}
		if st, err := os.Stat(src); err != nil || !st.IsDir() {
			r.State, r.Why = ExposureRefused, fmt.Sprintf("%s is declared and not a directory in the plugin", s.Path)
			out = append(out, r)
			continue
		}
		r.State, r.At, r.Why = m.link(src, name, taken, "plugin:"+man.Metadata.ID)
		out = append(out, r)
	}
	return out
}
















func (m *Manager) exposeStateSkills(man *Manifest, s SkillDecl, taken map[string]string) []SkillReport {
	source := "state:" + man.Metadata.ID
	pos, rel := s.StateSkillsDir()
	if m.Workspace == "" {
		return []SkillReport{{Name: s.State, Source: source, State: ExposureUnknown,
			Why: "this runtime has no workspace, so there is no state zone to look in"}}
	}
	root := filepath.Join(m.Workspace, ".baton", pos, "ext", man.Metadata.ID, filepath.FromSlash(rel))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []SkillReport{{Name: s.State, Source: source, State: ExposureRefused, Why: err.Error()}}
	}
	var out []SkillReport
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		src := filepath.Join(root, name)
		r := SkillReport{Name: name, Source: source}
		switch {
		case !skillName.MatchString(name):
			r.State, r.Why = ExposureRefused, fmt.Sprintf("%s/%s is not a skill name — lowercase letters, digits and interior hyphens", s.State, name)
		default:
			if st, statErr := os.Stat(filepath.Join(src, "SKILL.md")); statErr != nil || st.IsDir() {
				r.State, r.Why = ExposureRefused, fmt.Sprintf("%s/%s has no SKILL.md — a skill is a SKILL.md, and a directory without one is not exposed", s.State, name)
			} else {
				r.State, r.At, r.Why = m.link(src, name, taken, source)
			}
		}
		out = append(out, r)
	}
	return out
}



func (m *Manager) link(src, name string, taken map[string]string, owner string) (Exposure, string, string) {
	if prev, ok := taken[name]; ok {
		return ExposureRefused, "", fmt.Sprintf("the name %q is already exposed by %s", name, prev)
	}

	switch m.Discovery {
	case "symlink", "external-dirs":















		state := ExposureSymlink
		why := ""
		if m.Discovery == "external-dirs" {
			state = ExposureExternalDirs
			why = "BATON linked it here; that this runtime reads this directory rests on the template's config write, which BATON did not verify"
		}
		if m.RuntimePath == "" {
			return ExposureUnknown, "", "discovery is " + m.Discovery + " and runtimePath is empty"
		}
		if err := m.clearStaleBridge(); err != nil {
			return ExposureRefused, "", err.Error()
		}
		dst := filepath.Join(m.RuntimePath, name)
		if err := os.MkdirAll(m.RuntimePath, 0o755); err != nil {
			return ExposureRefused, "", err.Error()
		}



		if fi, err := os.Lstat(dst); err == nil {
			if fi.Mode()&fs.ModeSymlink == 0 {
				return ExposureRefused, "", fmt.Sprintf("%s exists and is not a link BATON made", dst)
			}
			if cur, err := os.Readlink(dst); err == nil && cur == src {
				taken[name] = owner
				return state, dst, why
			}
			if err := os.Remove(dst); err != nil {
				return ExposureRefused, "", err.Error()
			}
		}
		if err := os.Symlink(src, dst); err != nil {
			return ExposureRefused, "", err.Error()
		}
		taken[name] = owner
		return state, dst, why

	default:


		taken[name] = owner
		return ExposureUnknown, src, "this runtime declares no skills discovery, so where it looks — or whether it looks — is unmeasured"
	}
}
















type withdrawal struct {
	line   string
	target string
}

func (m *Manager) withdrawDangling() []withdrawal {
	if m.RuntimePath == "" {
		return nil
	}
	entries, err := os.ReadDir(m.RuntimePath)
	if err != nil {
		return nil
	}
	ours := []string{m.Root + string(filepath.Separator)}
	if m.Workspace != "" {
		for _, pos := range []string{"portable", "local"} {
			ours = append(ours, filepath.Join(m.Workspace, ".baton", pos, "ext")+string(filepath.Separator))
		}
	}
	var out []withdrawal
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink == 0 {
			continue
		}
		dst := filepath.Join(m.RuntimePath, e.Name())
		target, err := os.Readlink(dst)
		if err != nil {
			continue
		}
		mine := false
		for _, prefix := range ours {
			if strings.HasPrefix(target, prefix) {
				mine = true
			}
		}
		if !mine {
			continue
		}
		if _, err := os.Stat(target); err == nil {
			continue
		}
		if err := os.Remove(dst); err != nil {
			out = append(out, withdrawal{fmt.Sprintf("%s → %s: gone, and could not be withdrawn: %v", e.Name(), target, err), target})
			continue
		}
		out = append(out, withdrawal{fmt.Sprintf("%s → %s: source gone, link withdrawn", e.Name(), target), target})
	}
	return out
}














func (m *Manager) clearStaleBridge() error {
	fi, err := os.Lstat(m.RuntimePath)
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		return nil
	}
	target, err := os.Readlink(m.RuntimePath)
	if err != nil {
		return err
	}
	if m.MountPath == "" || filepath.Clean(target) != filepath.Clean(m.MountPath) {
		return fmt.Errorf("%s is a symlink to %s — BATON only removes its own old whole-directory bridge (to %s), so this one is left for whoever made it",
			m.RuntimePath, target, m.MountPath)
	}
	if m.Log != nil {
		m.Log.Info("plugins: removed the old whole-directory skills bridge",
			"path", m.RuntimePath, "was", target)
	}
	return os.Remove(m.RuntimePath)
}








func (m *Manager) runHook(ctx context.Context, man *Manifest, dir string, class Class) string {
	if man.Hooks.Configure == "" {
		return "none"
	}
	if class != ClassSystem {
		return fmt.Sprintf("refused: %s declares hooks.configure and its trust is %s — "+
			"only a facility that came through the %q door may run code here. "+
			"Seed it with `baton init` (or re-seed it if you edited it); an installed or "+
			"adopted facility keeps every declaration it makes and runs no code",
			man.Metadata.ID, class, trustedDoor)
	}

	hostname, _ := os.Hostname()
	marker := filepath.Join(m.StateDir, man.Metadata.ID+".configure.done")
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == hostname && hostname != "" {
		return "already-ran"
	}

	hookCtx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	bin := filepath.Join(dir, filepath.FromSlash(man.Hooks.Configure))
	cmd := exec.CommandContext(hookCtx, bin)
	cmd.Dir = dir
	cmd.Env = m.Env
	out, err := cmd.CombinedOutput()
	if err != nil {




		tail := string(out)
		if len(tail) > 1000 {
			tail = "…" + tail[len(tail)-1000:]
		}
		return fmt.Sprintf("failed: %v %s", err, strings.TrimSpace(tail))
	}
	if err := os.MkdirAll(m.StateDir, 0o700); err != nil {
		return fmt.Sprintf("failed: %v", err)
	}
	if err := os.WriteFile(marker, []byte(hostname+"\n"), 0o600); err != nil {
		return fmt.Sprintf("failed: %v", err)
	}
	return "ran"
}












































func (m *Manager) applyInstructions(dir string, man *Manifest) (status, path, sha string) {
	in := man.Instructions
	if in == nil {
		return "none", "", ""
	}












	path = m.expand(in.Path)



















	if m.Supplier == "" || man.Metadata.ID != m.Supplier {


		why := "this node's runtime was supplied by " + m.Supplier
		if m.Supplier == "" {
			why = "this node's spec names no runtime supplier"
		}
		return "refused: " + man.Metadata.ID + " did not supply this node's runtime — " + why +
			", and only the supplier may write into the agent's instruction file", path, ""
	}
	if path == "" {
		return "failed: instructions.path expanded to nothing", "", ""
	}



	src := filepath.Join(dir, filepath.FromSlash(in.File))
	if st, err := os.Lstat(src); err != nil {
		return "refused: instructions.file " + in.File + " is not in the plugin (" + err.Error() + ")", path, ""
	} else if st.Mode()&os.ModeSymlink != 0 {
		return "refused: instructions.file " + in.File + " is a symlink, and a symlink names bytes that are not in the plugin", path, ""
	}
	content, err := os.ReadFile(src)
	if err != nil {
		return "failed: " + err.Error(), path, ""
	}
	if strings.TrimSpace(string(content)) == "" {
		return "refused: instructions.file " + in.File + " is empty — a facility that meant to say something and did not", path, ""
	}
	if !strings.HasSuffix(string(content), "\n") {
		content = append(content, '\n')
	}
	start, end := InstructionMarkers(man.Metadata.ID)

	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return "failed: " + err.Error(), path, ""
	}
	if strings.Contains(string(body), start) {
		return "already-there", path, blockSHA(content)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "failed: " + err.Error(), path, ""
	}


















	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return "failed: " + err.Error(), path, ""
	}
	defer func() { _ = f.Close() }()
	prefix := ""
	if len(body) > 0 && !strings.HasSuffix(string(body), "\n") {



		prefix = "\n"
	}
	if _, err := f.WriteString(prefix + start + "\n" + string(content) + end + "\n"); err != nil {
		return "failed: " + err.Error(), path, ""
	}
	return "appended " + path, path, blockSHA(content)
}







func blockSHA(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}




func (m *Manager) expand(p string) string {
	if p == "" {
		return ""
	}
	home := ""
	for _, kv := range m.Env {
		if v, ok := strings.CutPrefix(kv, "HOME="); ok {
			home = v
		}
	}
	return os.Expand(p, func(k string) string {
		if k == "HOME" {
			return home
		}



		return "$" + k
	})
}




const ReportFile = "plugins.json"






func (m *Manager) writeReport(rep Report) error {
	if m.StateDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.StateDir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(m.StateDir, ReportFile)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}













func InstructionBlocksAt(stateDir string) []PluginReport {
	raw, err := os.ReadFile(filepath.Join(stateDir, ReportFile))
	if err != nil {
		return nil
	}
	var rep Report
	if json.Unmarshal(raw, &rep) != nil {
		return nil
	}
	out := make([]PluginReport, 0, len(rep.Plugins))
	for _, p := range rep.Plugins {
		if p.InstructionsSHA256 != "" && p.InstructionsPath != "" {
			out = append(out, p)
		}
	}
	return out
}







func (m *Manager) readPrevious() (Report, error) {
	if m.StateDir == "" {
		return Report{}, nil
	}
	raw, err := os.ReadFile(filepath.Join(m.StateDir, ReportFile))
	if err != nil {
		if os.IsNotExist(err) {
			return Report{}, nil
		}
		return Report{}, err
	}
	var rep Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return Report{}, err
	}
	return rep, nil
}




















func (m *Manager) retireGone(prev Report, prevErr error, dangling []withdrawal, rep *Report) map[string][]string {
	retired := map[string][]string{}
	if prevErr != nil {
		m.Log.Warn("previous plugins report unreadable; nothing retired", "err", prevErr)
		rep.Withdrawn = append(rep.Withdrawn, fmt.Sprintf("previous %s unreadable (%v): nothing retired — the leftovers of any plugin removed since stay in place until a readable record says what they were", ReportFile, prevErr))
		return retired
	}
	present := map[string]bool{}
	for _, p := range rep.Plugins {
		present[p.ID] = true
	}
	for id, lines := range prev.Retired {
		if !present[id] {
			retired[id] = lines
		}
	}
	for _, p := range prev.Plugins {
		if p.ID == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.Root, p.ID)); err == nil || !os.IsNotExist(err) {
			continue
		}



		var lines []string
		prefix := filepath.Join(m.Root, p.ID) + string(filepath.Separator)
		for _, w := range dangling {
			if strings.HasPrefix(w.target, prefix) {
				lines = append(lines, w.line)
			}
		}
		own := m.retire(p)
		rep.Withdrawn = append(rep.Withdrawn, own...)
		retired[p.ID] = append(lines, own...)
		rep.Summary.Retired++
	}
	return retired
}




























func (m *Manager) withdrawSkillLinks(p PluginReport, why string) []string {
	var out []string
	for _, s := range p.Skills {
		if s.At == "" || (s.State != ExposureSymlink && s.State != ExposureExternalDirs) {
			continue
		}
		fi, err := os.Lstat(s.At)
		if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(s.At)
		if err != nil || !m.ownedBy(target, p.ID) {
			continue
		}
		if err := os.Remove(s.At); err != nil {
			out = append(out, fmt.Sprintf("%s → %s: %s %s, and the link could not be withdrawn: %v", s.Name, target, p.ID, why, err))
			continue
		}
		out = append(out, fmt.Sprintf("%s → %s: %s %s, link withdrawn", s.Name, target, p.ID, why))
	}
	return out
}

func (m *Manager) retire(p PluginReport) []string {
	out := []string{p.ID + ": directory gone, retired"}

	out = append(out, m.withdrawSkillLinks(p, "retired")...)

	if p.InstructionsPath != "" {
		out = append(out, m.cutInstructions(p.ID, p.InstructionsPath))
	}

	for _, st := range p.State {
		if st.At == "" {
			continue
		}
		if st.BelongsTo != BelongsToOffice {
			out = append(out, fmt.Sprintf("%s: %s kept — belongs to %s, not to the plugin", p.ID, st.At, st.BelongsTo))
			continue
		}
		if err := os.RemoveAll(st.At); err != nil {
			out = append(out, fmt.Sprintf("%s: office state %s could not be removed: %v", p.ID, st.At, err))
			continue
		}
		out = append(out, fmt.Sprintf("%s: office state %s removed", p.ID, st.At))
	}



	marker := filepath.Join(m.StateDir, p.ID+".configure.done")
	switch err := os.Remove(marker); {
	case err == nil:
		out = append(out, p.ID+": configure marker cleared")
	case os.IsNotExist(err):
		out = append(out, p.ID+": no configure marker to clear")
	default:
		out = append(out, fmt.Sprintf("%s: configure marker could not be cleared: %v", p.ID, err))
	}
	return out
}



func (m *Manager) ownedBy(target, id string) bool {
	sep := string(filepath.Separator)
	if strings.HasPrefix(target, filepath.Join(m.Root, id)+sep) {
		return true
	}
	if m.Workspace == "" {
		return false
	}
	for _, pos := range []string{"portable", "local"} {
		if strings.HasPrefix(target, filepath.Join(m.Workspace, ".baton", pos, "ext", id)+sep) {
			return true
		}
	}
	return false
}












func (m *Manager) cutInstructions(id, path string) string {
	start, end := InstructionMarkers(id)
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("%s: instructions file %s absent, nothing to cut", id, path)
		}
		return fmt.Sprintf("%s: instructions file %s: %v", id, path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("%s: instructions file %s: %v", id, path, err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	from, to := -1, -1
	for i, l := range lines {
		switch strings.TrimSpace(l) {
		case start:
			if from == -1 {
				from = i
			}
		case end:
			if from != -1 && to == -1 {
				to = i
			}
		}
	}
	if from == -1 {
		return fmt.Sprintf("%s: no instruction block in %s, nothing to cut", id, path)
	}
	if to == -1 {
		return fmt.Sprintf("%s: instruction block in %s has its start marker and no end marker — edited; left in place", id, path)
	}
	kept := append(append([]string{}, lines[:from]...), lines[to+1:]...)
	if err := os.WriteFile(path, []byte(strings.Join(kept, "")), fi.Mode().Perm()); err != nil {
		return fmt.Sprintf("%s: instruction block in %s could not be cut: %v", id, path, err)
	}
	return fmt.Sprintf("%s: instruction block cut from %s", id, path)
}
