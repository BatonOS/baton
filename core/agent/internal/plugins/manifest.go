// SPDX-License-Identifier: Apache-2.0
























package plugins

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/batonos/baton/core/agent/internal/runtimespec"
	"github.com/batonos/baton/core/agent/internal/wscontract"
)










const APIVersion = "baton.mailloop.dev/v1alpha1"







const Kind = "WorkspacePlugin"


const ManifestFile = "manifest.yaml"







const PageFile = "PLUGIN.md"







type BelongsTo string

const (



	BelongsToIdentity BelongsTo = "identity"


	BelongsToScene BelongsTo = "scene"

	BelongsToOffice BelongsTo = "office"
)

func (b BelongsTo) valid() bool {
	return b == BelongsToIdentity || b == BelongsToScene || b == BelongsToOffice
}






type Manifest struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`


	State []StateDecl `yaml:"state,omitempty"`


	Skills []SkillDecl `yaml:"skills,omitempty"`


	Hooks Hooks `yaml:"hooks,omitempty"`
















	Runtime yaml.Node `yaml:"runtime,omitempty"`








	Service *ServiceDecl `yaml:"service,omitempty"`









	Instructions *Instructions `yaml:"instructions,omitempty"`
}
















type Instructions struct {


	Path string `yaml:"path"`




	File string `yaml:"file"`
}





func InstructionMarkers(id string) (start, end string) {
	return "<!-- baton:" + id + ":instructions -->", "<!-- /baton:" + id + ":instructions -->"
}


type Metadata struct {




	ID string `yaml:"id"`
























	Version string `yaml:"version"`
}









var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)


type StateDecl struct {
	Path      string    `yaml:"path"`
	BelongsTo BelongsTo `yaml:"belongs_to"`
}


















type SkillDecl struct {
	Path  string `yaml:"path,omitempty"`
	State string `yaml:"state,omitempty"`
}




func (sk SkillDecl) StateSkillsDir() (position, rel string) {
	pos, rest, _ := strings.Cut(sk.State, "/")
	return pos, rest
}



























type Hooks struct {



	Configure string `yaml:"configure,omitempty"`
}
















var Phases = []string{"plan", "provision", "configure", "activate", "run", "deactivate", "destroy"}





var ExecutableHooks = []string{"configure"}


const PhaseConfigure = "configure"



var idSegment = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)




var skillName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)





func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("metadata.id is empty")
	}
	parts := strings.Split(id, ".")
	if len(parts) < 2 {
		return fmt.Errorf("%q is not a plugin id: it needs a namespace, like example.memory — "+
			"a bare name collides with every other vendor's idea of the same facility", id)
	}
	for _, p := range parts {
		if !idSegment.MatchString(p) {
			return fmt.Errorf("%q is not a plugin id: the segment %q is not lowercase letters, "+
				"digits and interior hyphens", id, p)
		}
	}
	return nil
}






func relativeInside(field, p string) error {
	switch {
	case p == "":
		return fmt.Errorf("%s is empty", field)
	case path.IsAbs(p) || strings.HasPrefix(p, "\\") || strings.Contains(p, ":"):
		return fmt.Errorf("%s (%q) must be relative to the plugin, not an absolute path", field, p)
	}
	clean := path.Clean(p)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("%s (%q) leaves the plugin directory", field, p)
	}
	return nil
}









type ServiceDecl struct {
	Command      []string                  `yaml:"command"`
	WorkingDir   string                    `yaml:"workingDir,omitempty"`
	User         string                    `yaml:"user,omitempty"`
	Env          []runtimespec.EnvVar      `yaml:"env,omitempty"`
	Secrets      []runtimespec.Secret      `yaml:"secrets,omitempty"`
	Health       runtimespec.Health        `yaml:"health,omitempty"`
	Restart      runtimespec.RestartPolicy `yaml:"restart,omitempty"`
	StopGraceSec int                       `yaml:"stopGraceSec,omitempty"`
}

func (sd *ServiceDecl) validate(label string) error {
	if len(sd.Command) == 0 {
		return fmt.Errorf("%s: service has no command — there is nothing to start", label)
	}
	switch sd.Restart {
	case "", runtimespec.RestartAlways, runtimespec.RestartOnFailure, runtimespec.RestartNever:
	default:
		return fmt.Errorf("%s: service.restart is %q — it must be one of always, on-failure, never", label, sd.Restart)
	}
	switch sd.Health.Probe {
	case "", runtimespec.ProbeNone, runtimespec.ProbeProcess, runtimespec.ProbeTCP, runtimespec.ProbeExec:
	default:
		return fmt.Errorf("%s: service.health.probe is %q — it must be one of none, process, tcp, exec", label, sd.Health.Probe)
	}
	if sd.StopGraceSec < 0 {
		return fmt.Errorf("%s: service.stopGraceSec is negative", label)
	}
	for i, e := range sd.Env {
		if e.Name == "" {
			return fmt.Errorf("%s: service.env[%d] has no name", label, i)
		}
		if strings.HasPrefix(e.Name, "BATON_") {
			return fmt.Errorf("%s: service.env sets %s — BATON_* is the daemon's to set, and a facility that sets it is lying to its own process", label, e.Name)
		}
	}
	return nil
}





func Parse(data []byte, label string) (*Manifest, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("%s is empty", label)
	}
	if err := namePhasesBeforeTheDecoderDoes(data, label); err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}

	if m.APIVersion != APIVersion {
		return nil, fmt.Errorf("%s: apiVersion is %q, and this build reads %q",
			label, m.APIVersion, APIVersion)
	}
	if m.Kind != Kind {
		return nil, fmt.Errorf("%s: kind is %q, and this build reads %q", label, m.Kind, Kind)
	}
	if err := ValidateID(m.Metadata.ID); err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	if err := validateVersion(m.Metadata.Version, label); err != nil {
		return nil, err
	}

	seenState := map[string]bool{}
	for i, s := range m.State {
		if err := relativeInside(fmt.Sprintf("state[%d].path", i), s.Path); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if !s.BelongsTo.valid() {



			return nil, fmt.Errorf("%s: state[%d] (%s) has belongs_to %q — it must say one of "+
				"identity, scene or office, because that is what decides whether uninstalling "+
				"this plugin deletes it", label, i, s.Path, s.BelongsTo)
		}
		clean := path.Clean(s.Path)
		if seenState[clean] {
			return nil, fmt.Errorf("%s: state %q is declared twice", label, clean)
		}
		seenState[clean] = true
	}

	seenSkill := map[string]bool{}
	for i, s := range m.Skills {
		switch {
		case s.Path != "" && s.State != "":
			return nil, fmt.Errorf("%s: skills[%d] gives both path (%q) and state (%q) — one entry is one place, "+
				"and a shipped directory and a run-time directory are different places", label, i, s.Path, s.State)
		case s.Path == "" && s.State == "":
			return nil, fmt.Errorf("%s: skills[%d] gives neither path nor state — it names no directory", label, i)
		case s.State != "":


			pos, rest, ok := strings.Cut(s.State, "/")
			if !ok || (pos != "portable" && pos != "local") || rest == "" {
				return nil, fmt.Errorf("%s: skills[%d].state %q must be `portable/<dir>` or `local/<dir>` — "+
					"the prefix says whether the skills it grows travel with the workspace", label, i, s.State)
			}
			if err := relativeInside(fmt.Sprintf("skills[%d].state", i), rest); err != nil {
				return nil, fmt.Errorf("%s: %w", label, err)
			}
			continue
		}
		if err := relativeInside(fmt.Sprintf("skills[%d].path", i), s.Path); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		name := path.Base(path.Clean(s.Path))
		if !skillName.MatchString(name) {
			return nil, fmt.Errorf("%s: skills[%d] (%s) ends in %q, which is not a skill name — "+
				"lowercase letters, digits and interior hyphens; it becomes a directory inside "+
				"the runtime's own skills path", label, i, s.Path, name)
		}
		if seenSkill[name] {
			return nil, fmt.Errorf("%s: two skills would both be exposed as %q", label, name)
		}
		seenSkill[name] = true
	}

	if m.Hooks.Configure != "" {
		if err := relativeInside("hooks.configure", m.Hooks.Configure); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
	}

	if in := m.Instructions; in != nil {
		switch {
		case in.Path == "":
			return nil, fmt.Errorf("%s: instructions.path is empty — there is nowhere to write the block", label)
		case in.File == "":



			return nil, fmt.Errorf("%s: instructions names %s and no file to put in it", label, in.Path)
		case !strings.HasPrefix(in.Path, "/") && !strings.HasPrefix(in.Path, "$HOME") && !strings.HasPrefix(in.Path, "${HOME}"):
			return nil, fmt.Errorf("%s: instructions.path %q must be absolute or start at $HOME — "+
				"$HOME is the only coordinate a facility may depend on, because where the host maps it is the host's business", label, in.Path)
		}
		if why := contractSurface(in.Path); why != "" {
			return nil, fmt.Errorf("%s: instructions.path %q points into %s — %s, "+
				"and a facility may not write into a surface whose bytes belong to someone else",
				label, in.Path, why, surfaceOwner(why))
		}
		if err := relativeInside("instructions.file", in.File); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
	}
	if m.HasRuntime() && m.Service != nil {
		return nil, fmt.Errorf("%s: gives both runtime and service — a facility is the occupant or it is "+
			"equipment beside the occupant, never both", label)
	}
	if m.Service != nil {
		if err := m.Service.validate(label); err != nil {
			return nil, err
		}
	}
	return &m, nil
}





func (m *Manifest) HasRuntime() bool {
	return m.Runtime.Kind != 0
}


func (m *Manifest) HasService() bool { return m.Service != nil }












func namePhasesBeforeTheDecoderDoes(data []byte, label string) error {
	var probe struct {
		Hooks map[string]any `yaml:"hooks"`
	}



	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil
	}
	for k := range probe.Hooks {
		if k == PhaseConfigure {
			continue
		}
		for _, p := range Phases {
			if k == p {
				return fmt.Errorf("%s: hooks.%s names a lifecycle phase, and `hooks:` is not the list of phases — "+
					"it is the list of phases that are EXECUTABLE HOOKS, which today is %s. "+
					"The lifecycle vocabulary is %s, and a phase being in it is not a promise that it "+
					"becomes a hook: `run` names a long-lived process, and the workload is already "+
					"`runtime.package.command`",
					label, k, strings.Join(ExecutableHooks, " · "), strings.Join(Phases, " · "))
			}
		}
		return fmt.Errorf("%s: hooks.%s is not a lifecycle phase at all — the vocabulary is %s, "+
			"and the executable hooks are %s",
			label, k, strings.Join(Phases, " · "), strings.Join(ExecutableHooks, " · "))
	}
	return nil
}






func validateVersion(v, label string) error {
	switch {
	case v == "":
		return fmt.Errorf("%s: metadata.version is missing — it is MAJOR.MINOR.PATCH, "+
			"for example `1.0.0`", label)
	case strings.ContainsAny(v, "-+"):
		return fmt.Errorf("%s: metadata.version is %q — pre-release and build metadata are not "+
			"accepted yet. They exist so a resolver can rank candidates, and this build has no "+
			"resolver; accepting them would mean accepting an ordering nothing here can compute", label, v)
	case !semver.MatchString(v):






		return fmt.Errorf("%s: metadata.version is %q — it is MAJOR.MINOR.PATCH, three non-negative "+
			"integers with no leading zeroes (`1.0.0`, `2.13.4`). "+
			"An unquoted `1` or `1.0` in YAML is a number, and it arrives here as those digits "+
			"and nothing else", label, v)
	}
	return nil
}


func (m *Manifest) SkillNames() []string {
	out := make([]string, 0, len(m.Skills))
	for _, s := range m.Skills {
		out = append(out, path.Base(path.Clean(s.Path)))
	}
	return out
}























func contractSurface(p string) string {
	cleaned := path.Clean(strings.ReplaceAll(p, "\\", "/"))
	if path.Base(cleaned) == wscontract.BatonPage {
		return wscontract.BatonPage
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == wscontract.BatonDir {
			return wscontract.BatonDir
		}
		for _, norm := range wscontract.RootNorms {
			if seg == norm {
				return norm + "/"
			}
		}
	}
	return ""
}



func surfaceOwner(surface string) string {
	switch surface {
	case wscontract.BatonDir:
		return "those bytes are BATON's own contract face, rebuilt at every start"
	case wscontract.BatonPage:
		return "that page is BATON speaking to the occupant, and a third party's block there reads as BATON's words"
	default:
		return "those bytes belong to the workspace's owner, who is promised they are seeded once and then never touched"
	}
}
