// SPDX-License-Identifier: Apache-2.0














package runtimespec

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)


const APIVersion = "baton.mailloop.dev/v1alpha1"


const Kind = "AgentRuntime"


type ProbeKind string

const (

	ProbeProcess ProbeKind = "process"
	ProbeTCP     ProbeKind = "tcp"
	ProbeExec    ProbeKind = "exec"


	ProbeNone ProbeKind = "none"
)


type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartNever     RestartPolicy = "never"
)


type Spec struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`













	Package   Package   `yaml:"package"`
	Adapter   Adapter   `yaml:"adapter,omitempty"`
	Execution Execution `yaml:"execution,omitempty"`



	source string
}


func (s *Spec) Source() string { return s.source }

type Metadata struct {
	Name   string            `yaml:"name"`
	Labels map[string]string `yaml:"labels,omitempty"`
}












type Package struct {

	Image string `yaml:"image,omitempty"`




	Install [][]string `yaml:"install,omitempty"`

	Command    []string `yaml:"command,omitempty"`
	WorkingDir string   `yaml:"workingDir,omitempty"`
}






type Adapter struct {
	Terminal    Terminal  `yaml:"terminal,omitempty"`
	Workspace   Workspace `yaml:"workspace,omitempty"`
	Lifecycle   Lifecycle `yaml:"lifecycle,omitempty"`
	Health      Health    `yaml:"health,omitempty"`







	Status      Status    `yaml:"status,omitempty"`







	Skills      Skills    `yaml:"skills,omitempty"`








	Persistence []Persisted `yaml:"persistence,omitempty"`







	Inbox Inbox `yaml:"inbox,omitempty"`
}








type Inbox struct {












	OnMessage []string `yaml:"onMessage,omitempty"`
}


type PersistClass string

const (
	PersistCache  PersistClass = "cache"
	PersistState  PersistClass = "state"
	PersistSecret PersistClass = "secret"
)


type Persisted struct {
	Path  string       `yaml:"path"`
	Class PersistClass `yaml:"class,omitempty"`
}


func (a Adapter) PersistedPaths() []string {
	out := make([]string, 0, len(a.Persistence))
	for _, p := range a.Persistence {
		out = append(out, p.Path)
	}
	return out
}


type Terminal struct {
	TTY bool `yaml:"tty,omitempty"`
}


type Lifecycle struct {
	Restart      RestartPolicy `yaml:"restart,omitempty"`
	StopGraceSec int           `yaml:"stopGraceSec,omitempty"`
}








type Execution struct {






	Resources Resources `yaml:"resources,omitempty"`



















	User string `yaml:"user,omitempty"`



















	DriverNetwork string `yaml:"driver_network,omitempty"`

	Network    Network    `yaml:"network,omitempty"`
	Checkpoint Checkpoint `yaml:"checkpoint,omitempty"`




	Secrets []Secret `yaml:"secrets,omitempty"`
	Env     []EnvVar `yaml:"env,omitempty"`
}


type Network struct {
	Egress Egress `yaml:"egress,omitempty"`
}




type Egress struct {
	Mode  string   `yaml:"mode,omitempty"`
	Allow []string `yaml:"allow,omitempty"`
}






type Checkpoint struct {
	Requested string `yaml:"requested,omitempty"`
}







type Workspace struct {

	Volume string `yaml:"volume,omitempty"`

	MountPath string `yaml:"mountPath,omitempty"`

	ReadOnly bool `yaml:"readOnly,omitempty"`
}

























type Skills struct {


	MountPath string `yaml:"mountPath,omitempty"`









	Discovery string `yaml:"discovery,omitempty"`






	RuntimePath string `yaml:"runtimePath,omitempty"`
}




const (
	DiscoverySymlink      = "symlink"
	DiscoveryExternalDirs = "external-dirs"
	DiscoveryNone         = "none"
)






type Secret struct {
	Name string `yaml:"name"`

	From string `yaml:"from"`


	MountPath string `yaml:"mountPath,omitempty"`






	Label    string `yaml:"label,omitempty"`
	Provider string `yaml:"provider,omitempty"`
	HelpURL  string `yaml:"help_url,omitempty"`








	Required *bool `yaml:"required"`
}


type EnvVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type Resources struct {
	CPU    string `yaml:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty"`
}






type StatusReport string

const (








	StatusReportFile StatusReport = "file"
)


type Status struct {
	Report StatusReport `yaml:"report,omitempty"`
}


func (s Status) Declared() bool { return s.Report == StatusReportFile }

type Health struct {
	Probe       ProbeKind `yaml:"probe,omitempty"`
	IntervalSec int       `yaml:"intervalSec,omitempty"`

	TCP string `yaml:"tcp,omitempty"`

	Exec []string `yaml:"exec,omitempty"`

	FailureThreshold int `yaml:"failureThreshold,omitempty"`
}


const (
	DefaultStopGrace       = 10 * time.Second
	DefaultProbeInterval   = 15 * time.Second
	DefaultFailureThresh   = 3
	DefaultSecretMountRoot = "/run/secrets"
)


func (h Health) Interval() time.Duration {
	if h.IntervalSec <= 0 {
		return DefaultProbeInterval
	}
	return time.Duration(h.IntervalSec) * time.Second
}


func (h Health) Threshold() int {
	if h.FailureThreshold <= 0 {
		return DefaultFailureThresh
	}
	return h.FailureThreshold
}


func (l Lifecycle) StopGrace() time.Duration {
	if l.StopGraceSec <= 0 {
		return DefaultStopGrace
	}
	return time.Duration(l.StopGraceSec) * time.Second
}


func (a Adapter) Enterable() bool { return a.Terminal.TTY }


func (s Secret) Path() string {
	if s.MountPath != "" {
		return s.MountPath
	}
	return filepath.Join(DefaultSecretMountRoot, s.Name)
}


func (s Secret) FilePath() (string, bool) {
	rest, ok := strings.CutPrefix(s.From, "file:")
	return rest, ok
}


var ErrNotFound = errors.New("runtimespec: no spec file")















func Load(path, defaultName string) (*Spec, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s", ErrNotFound, path)
	}
	if err != nil {
		return nil, err
	}
	s, err := Parse(raw, defaultName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.source = path
	return s, nil
}


func Parse(raw []byte, defaultName string) (*Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(bytes.NewReader(raw))


	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("runtimespec: parse: %w", err)
	}

	if s.Metadata.Name == "" {
		s.Metadata.Name = defaultName
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}



var nameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)





func (s *Spec) Validate() error {


	for i := range s.Adapter.Persistence {
		p := &s.Adapter.Persistence[i]
		if p.Path == "" {
			return fmt.Errorf("runtimespec: adapter.persistence[%d] has no path", i)
		}
		switch p.Class {
		case "":
			p.Class = PersistState
		case PersistCache, PersistState, PersistSecret:
		default:
			return fmt.Errorf("runtimespec: adapter.persistence[%d].class is %q; it is cache, state or secret", i, p.Class)
		}
	}
	if s.APIVersion != APIVersion {
		return fmt.Errorf("runtimespec: apiVersion is %q; this build understands %q",
			s.APIVersion, APIVersion)
	}
	if s.Kind != Kind {
		return fmt.Errorf("runtimespec: kind is %q; expected %q", s.Kind, Kind)
	}
	if !nameRE.MatchString(s.Metadata.Name) {
		return fmt.Errorf("runtimespec: metadata.name %q is not usable: lowercase letters, "+
			"digits, and interior hyphens, 1-40 characters", s.Metadata.Name)
	}

	pkg := &s.Package








	if len(pkg.Command) == 0 {
		return errors.New("runtimespec: package.command is required, as a list: " +
			"command: [\"your-runtime\", \"--workspace\", \"/workspace\"]")
	}


	for i, argv := range pkg.Install {
		if len(argv) == 0 {
			return fmt.Errorf("runtimespec: package.install[%d] is empty; give it as a "+
				"list: [\"apt-get\", \"install\", \"-y\", \"ripgrep\"]", i)
		}
	}
	for i, arg := range pkg.Command {
		if arg == "" {
			return fmt.Errorf("runtimespec: package.command[%d] is empty; "+
				"remove it rather than passing an empty argument", i)
		}
	}

	lc := &s.Adapter.Lifecycle
	switch lc.Restart {
	case "":
		lc.Restart = RestartOnFailure
	case RestartAlways, RestartOnFailure, RestartNever:
	default:
		return fmt.Errorf("runtimespec: adapter.lifecycle.restart is %q; use always, on-failure, or never",
			lc.Restart)
	}

	if lc.StopGraceSec < 0 {
		return fmt.Errorf("runtimespec: adapter.lifecycle.stopGraceSec is %d; it cannot be negative",
			lc.StopGraceSec)
	}






	if err := s.validateWorkspace(); err != nil {
		return err
	}
	if err := s.validateSecrets(); err != nil {
		return err
	}
	if err := s.validateEnv(); err != nil {
		return err
	}
	if err := s.validateResources(); err != nil {
		return err
	}
	if p := s.Adapter.Skills.MountPath; p != "" && !filepath.IsAbs(p) {
		return fmt.Errorf("runtimespec: adapter.skills.mountPath %q must be absolute", p)
	}
	if err := s.validateSkillDiscovery(); err != nil {
		return err
	}
	if err := s.validateHealth(); err != nil {
		return err
	}
	if err := s.validateStatus(); err != nil {
		return err
	}
	return s.validateInbox()
}









func (s *Spec) validateSkillDiscovery() error {
	d := s.Adapter.Skills.Discovery
	switch d {
	case "", DiscoveryNone, DiscoveryExternalDirs:
	case DiscoverySymlink:
		p := s.Adapter.Skills.RuntimePath
		if p == "" {
			return fmt.Errorf("runtimespec: adapter.skills.discovery is %q and runtimePath is empty — "+
				"there is nowhere to link to, and a link BATON cannot make is a skill the runtime will not find", d)
		}
		if !filepath.IsAbs(p) && !strings.HasPrefix(p, "$HOME") && !strings.HasPrefix(p, "${HOME}") {
			return fmt.Errorf("runtimespec: adapter.skills.runtimePath %q must be absolute or start at $HOME — "+
				"$HOME is the only coordinate a template may depend on, because where the host maps it is the host's business", p)
		}
	default:
		return fmt.Errorf("runtimespec: adapter.skills.discovery is %q — it is one of %s, %s or %s",
			d, DiscoverySymlink, DiscoveryExternalDirs, DiscoveryNone)
	}
	if s.Adapter.Skills.RuntimePath != "" && d != DiscoverySymlink {
		return fmt.Errorf("runtimespec: adapter.skills.runtimePath is set and discovery is %q — "+
			"a path nothing links into is a path nobody will notice is wrong", d)
	}
	return nil
}






func (s *Spec) validateStatus() error {
	switch s.Adapter.Status.Report {
	case "", StatusReportFile:
		return nil
	}
	return fmt.Errorf("runtimespec: spec.status.report %q is not a known form; "+
		"want %q, or leave it out for a runtime that does not report",
		s.Adapter.Status.Report, StatusReportFile)
}







func (s *Spec) validateInbox() error {
	on := s.Adapter.Inbox.OnMessage
	if len(on) == 0 {
		return nil
	}
	if strings.TrimSpace(on[0]) == "" {
		return errors.New("runtimespec: adapter.inbox.onMessage names no command; " +
			"leave the whole section out for a runtime that does not want waking")
	}
	return nil
}

func (s *Spec) validateWorkspace() error {
	w := s.Adapter.Workspace
	if w.Volume == "" && w.MountPath == "" {
		return nil
	}









	if w.MountPath == "" {
		return errors.New("runtimespec: spec.workspace.volume is set without a mountPath")
	}
	if !filepath.IsAbs(w.MountPath) {
		return fmt.Errorf("runtimespec: spec.workspace.mountPath %q must be absolute", w.MountPath)
	}
	return nil
}

func (s *Spec) validateSecrets() error {
	seen := make(map[string]struct{}, len(s.Execution.Secrets))
	for i, sec := range s.Execution.Secrets {
		if sec.Name == "" {
			return fmt.Errorf("runtimespec: spec.secrets[%d] has no name", i)
		}
		if _, dup := seen[sec.Name]; dup {
			return fmt.Errorf("runtimespec: spec.secrets[%d] repeats the name %q", i, sec.Name)
		}
		seen[sec.Name] = struct{}{}

		path, ok := sec.FilePath()
		if !ok {


			return fmt.Errorf("runtimespec: spec.secrets[%d] (%s) has from: %q; only file: sources "+
				"are supported. An environment variable is visible in `docker inspect`, in "+
				"/proc/<pid>/environ, and in crash reports, so a secret is never passed that way. "+
				"Write from: file:/run/secrets/%s and mount the value.",
				i, sec.Name, sec.From, sec.Name)
		}
		if path == "" {
			return fmt.Errorf("runtimespec: spec.secrets[%d] (%s) has an empty file: path", i, sec.Name)
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("runtimespec: spec.secrets[%d] (%s) path %q must be absolute",
				i, sec.Name, path)
		}
		if m := sec.MountPath; m != "" && !filepath.IsAbs(m) {
			return fmt.Errorf("runtimespec: spec.secrets[%d] (%s) mountPath %q must be absolute",
				i, sec.Name, m)
		}
		if sec.Required == nil {
			return fmt.Errorf("runtimespec: spec.secrets[%d] (%s) does not say whether it is required — "+
				"write required: true (the node cannot do its job without it) or required: false "+
				"(an alternative exists, such as an interactive login); silence would be read as optional",
				i, sec.Name)
		}
	}
	return nil
}

func (s *Spec) validateEnv() error {
	seen := make(map[string]struct{}, len(s.Execution.Env))
	for i, e := range s.Execution.Env {
		if e.Name == "" {
			return fmt.Errorf("runtimespec: spec.env[%d] has no name", i)
		}
		if strings.ContainsAny(e.Name, "= ") {
			return fmt.Errorf("runtimespec: spec.env[%d] name %q cannot contain '=' or a space",
				i, e.Name)
		}
		if _, dup := seen[e.Name]; dup {
			return fmt.Errorf("runtimespec: spec.env[%d] repeats the name %q", i, e.Name)
		}
		seen[e.Name] = struct{}{}
	}
	return nil
}

func (s *Spec) validateResources() error {
	res := s.Execution.Resources












	if res.CPU != "" {
		v, err := strconv.ParseFloat(res.CPU, 64)
		if err != nil || v <= 0 {
			return fmt.Errorf("runtimespec: spec.resources.cpu is %q, which is not the canonical "+
				"form this file is generated in (a plain number of cores). This file is written by "+
				"the CLI on the host, so the value was not normalised there — the template it came "+
				"from is not at fault", res.CPU)
		}
	}
	if res.Memory != "" {















		n, err := strconv.ParseInt(strings.TrimSpace(res.Memory), 10, 64)
		if err != nil || n <= 0 {
			return fmt.Errorf("runtimespec: spec.resources.memory is %q, which is not the canonical "+
				"form this file is generated in (a plain number of bytes). This file is written by "+
				"the CLI on the host, so the value was not normalised there — the template it came "+
				"from is not at fault", res.Memory)
		}
	}
	return nil
}

func (s *Spec) validateHealth() error {
	h := &s.Adapter.Health
	switch h.Probe {
	case "":
		h.Probe = ProbeProcess
	case ProbeProcess, ProbeNone:
	case ProbeTCP:
		if h.TCP == "" {
			return errors.New("runtimespec: spec.health.probe is tcp but spec.health.tcp is empty")
		}
	case ProbeExec:
		if len(h.Exec) == 0 {
			return errors.New("runtimespec: spec.health.probe is exec but spec.health.exec is empty; " +
				"give it as a list, not a shell string")
		}
		for i, arg := range h.Exec {
			if arg == "" {
				return fmt.Errorf("runtimespec: spec.health.exec[%d] is empty", i)
			}
		}
	default:
		return fmt.Errorf("runtimespec: spec.health.probe is %q; use process, tcp, exec, or none",
			h.Probe)
	}
	if h.IntervalSec < 0 {
		return fmt.Errorf("runtimespec: spec.health.intervalSec is %d; it cannot be negative",
			h.IntervalSec)
	}
	if h.FailureThreshold < 0 {
		return fmt.Errorf("runtimespec: spec.health.failureThreshold is %d; it cannot be negative",
			h.FailureThreshold)
	}
	return nil
}






func ParseQuantity(raw string) (int64, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, errors.New("empty")
	}

	multipliers := []struct {
		suffix string
		factor int64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
		{"K", 1000}, {"M", 1000 * 1000}, {"G", 1000 * 1000 * 1000},
		{"T", 1000 * 1000 * 1000 * 1000},
	}

	for _, m := range multipliers {
		digits, ok := strings.CutSuffix(v, m.suffix)
		if !ok {
			continue
		}
		n, err := strconv.ParseFloat(strings.TrimSpace(digits), 64)
		if err != nil || n <= 0 {



			return 0, fmt.Errorf("use a positive number with an optional K/M/G suffix, e.g. 2G")
		}
		return int64(n * float64(m.factor)), nil
	}

	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("use a positive number of bytes, or a size like 512M or 2G")
	}
	return n, nil
}
