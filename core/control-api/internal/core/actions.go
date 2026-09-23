// SPDX-License-Identifier: Apache-2.0

package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)














const (
	ConsequenceReadOnly     = "read-only"
	ConsequenceReversible   = "reversible"
	ConsequenceCostsMoney   = "costs-money"
	ConsequenceDataEgress   = "data-egress"
	ConsequenceIrreversible = "irreversible"
)



var consequenceClasses = map[string]bool{
	ConsequenceReadOnly:     true,
	ConsequenceReversible:   true,
	ConsequenceCostsMoney:   true,
	ConsequenceDataEgress:   true,
	ConsequenceIrreversible: true,
}


func KnownConsequenceClass(class string) bool { return consequenceClasses[class] }





func consequenceClassList() string {
	out := make([]string, 0, len(consequenceClasses))
	for c := range consequenceClasses {
		out = append(out, c)
	}
	sort.Strings(out)
	return strings.Join(out, " · ")
}







type ParamSpec struct {
	Name string `json:"name"`

	Type     string `json:"type"`
	Required bool   `json:"required"`








	Enum   []string `json:"enum,omitempty"`
	Closed bool     `json:"enum_closed,omitempty"`



	EnumFact string `json:"-"`




	EnumSource string `json:"-"`



	Doc string `json:"doc"`
}












func (s ParamSpec) MarshalJSON() ([]byte, error) {
	if !s.Closed {
		return json.Marshal(struct {
			Name     string   `json:"name"`
			Type     string   `json:"type"`
			Required bool     `json:"required"`
			Enum     []string `json:"enum,omitempty"`
			Doc      string   `json:"doc"`
		}{s.Name, s.Type, s.Required, s.Enum, s.Doc})
	}
	enum := s.Enum
	if enum == nil {
		enum = []string{}
	}
	source := s.EnumSource
	if source == "" && s.EnumFact == "" {
		source = "declared"
	}
	if source == "" {

		source = "unavailable"
	}
	return json.Marshal(struct {
		Name       string   `json:"name"`
		Type       string   `json:"type"`
		Required   bool     `json:"required"`
		Enum       []string `json:"enum"`
		Closed     bool     `json:"enum_closed"`
		EnumSource string   `json:"enum_source"`
		Doc        string   `json:"doc"`
	}{s.Name, s.Type, s.Required, enum, true, source, s.Doc})
}



func withFactEnums(specs []ParamSpec, facts []GovernanceFact) []ParamSpec {
	out := slices.Clone(specs)
	for i := range out {
		if out[i].EnumFact == "" {
			continue
		}
		vals, src, ok := factStringSet(facts, out[i].EnumFact)
		if ok {
			out[i].Enum, out[i].EnumSource = vals, src
		} else {
			out[i].Enum, out[i].EnumSource = []string{}, "unavailable"
		}
	}
	return out
}









func checkFactEnums(specs []ParamSpec, params map[string]any, facts []GovernanceFact) *OutcomeError {
	for _, s := range specs {
		if s.EnumFact == "" || !s.Closed {
			continue
		}
		v, present := params[s.Name].(string)
		if !present {
			continue
		}
		vals, src, ok := factStringSet(facts, s.EnumFact)
		if !ok {
			return &OutcomeError{
				Code:    CodePreconditionUnmet,
				Message: fmt.Sprintf("parameters.%s: cannot verify which values this Provider accepts — its %s fact could not be read", s.Name, s.EnumFact),
				Remediation: []Remedy{{Text: "Check that the local Provider is running and reachable from the control plane, then submit again."}},
				Detail: map[string]any{"field": "parameters." + s.Name, "fact": s.EnumFact},
			}
		}
		if !slices.Contains(vals, v) {
			can := "none"
			if len(vals) > 0 {
				can = strings.Join(vals, " · ")
			}
			return &OutcomeError{
				Code:    CodeInvalidProposal,
				Message: fmt.Sprintf("parameters.%s: %q is not one this Provider can start (it can start: %s) — %s", s.Name, v, can, s.Doc),
				Remediation: []Remedy{{Text: "Use one of the values the Provider reports, or install that Workspace Template on the Provider."}},
				Detail: map[string]any{"field": "parameters." + s.Name, "reason": "not one this Provider can start",
					"available": vals, "source": src},
			}
		}
	}
	return nil
}


func paramNames(specs []ParamSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}






func checkSpecs(specs []ParamSpec, params map[string]any) (string, string) {
	byName := map[string]ParamSpec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		spec, declared := byName[k]
		if !declared {
			return "parameters." + k, fmt.Sprintf("not a parameter of this action (it takes %s): an undeclared parameter would not be in the plan_digest, so an approval could not bind it",
				strings.Join(paramNames(specs), ", "))
		}
		if bad := specValue(spec, params[k]); bad != "" {
			return "parameters." + k, bad
		}
	}
	for _, s := range specs {
		if _, present := params[s.Name]; !present && s.Required {
			return "parameters." + s.Name, "required — " + s.Doc
		}
	}
	return "", ""
}

func specValue(s ParamSpec, v any) string {
	switch s.Type {
	case "string":
		str, ok := v.(string)
		if !ok {
			return "must be a string — " + s.Doc
		}





		if str == "" && !(s.Required && s.Name == "content") {
			return "must not be empty — " + s.Doc
		}
		if len(s.Enum) > 0 && s.Closed && !slices.Contains(s.Enum, str) {
			return fmt.Sprintf("must be one of %s — %s", strings.Join(s.Enum, " · "), s.Doc)
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return "must be an object — " + s.Doc
		}
	}
	return ""
}


















type Effects struct {
	Read               bool `json:"read"`
	Write              bool `json:"write"`
	ExternalSideEffect bool `json:"external_side_effect"`
	Destructive        bool `json:"destructive"`
}





func (e Effects) ReadOnlyEffects() bool {
	return !e.Write && !e.ExternalSideEffect && !e.Destructive
}


type Properties struct {
	Reversible bool `json:"reversible"`
	Idempotent bool `json:"idempotent"`
}



type Consequence struct {
	Class string `json:"class"`




	When string `json:"when"`









	Rendered string `json:"rendered"`
}






const (
	WhenAlways             = "always"
	WhenLeavesCurrentHost  = "leaves-current-host"
	WhenProviderPriced     = "provider-priced"
	WhenChangesPricedRsrcs = "changes-priced-resources"
)




type ActionDecl struct {
	Name    string
	Version string












	Summary string

	TargetKind string








	Effects Effects


	Properties Properties




	Consequences []Consequence













	ApprovalFloor ConfirmRung



	RequiresGrants []string








	PlacementKey string












	DestinationKey string











	Params []ParamSpec




	Check func(params map[string]any) (field, reason string)














	Steps func(p map[string]any) []spi.TxStep





	Declarer DeclaredBy







	DeclarationSHA string
}








type DeclaredBy struct {












	_ [0]func()

	Kind string `json:"kind"`








	ID string `json:"id"`




















	DeclarationSHA256 *string `json:"declaration_sha256"`
}












func (d DeclaredBy) Same(o DeclaredBy) bool {
	if d.Kind != o.Kind || d.ID != o.ID {
		return false
	}
	switch {
	case d.DeclarationSHA256 == nil && o.DeclarationSHA256 == nil:
		return true
	case d.DeclarationSHA256 == nil || o.DeclarationSHA256 == nil:
		return false
	default:
		return *d.DeclarationSHA256 == *o.DeclarationSHA256
	}
}














const (
	DeclaredByCore = "core"
	DeclaredByApp  = "app"
)


const coreDeclarerID = "baton-core"











func declaredByOf(name string) (DeclaredBy, bool) {
	if _, ok := declared[name]; ok {
		return DeclaredBy{Kind: DeclaredByCore, ID: coreDeclarerID}, true
	}




	if d, ok := lookupAction(name); ok && d.Declarer.Kind != "" {
		return d.Declarer, true
	}



	return DeclaredBy{}, false
}








































type ConfirmRung = string






const ConfirmRungButton ConfirmRung = "button"

const (
	ActionClone       = "baton.workspace.clone"
	ActionEditAndTest = "baton.workspace.edit-and-test"
	ActionInspect     = "baton.workspace.inspect"
	ActionCreate      = "baton.workspace.create"
)











const (
	GrantAgentRead   = "agent.read"
	GrantAgentCreate = "agent.create"
)

var shaHex = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var declared = map[string]ActionDecl{






	ActionEditAndTest: {
		Name:       ActionEditAndTest,
		Version:    "1.0.0",
		Summary:    "Replace one file inside a workspace and run one test there",
		TargetKind: "workspace",
















		Effects:    Effects{Read: true, Write: true},
		Properties: Properties{Reversible: true},
		Consequences: []Consequence{{Class: ConsequenceReversible, When: WhenAlways,
			Rendered: "replaces one file inside the workspace it names and runs one test there; " +
				"a snapshot taken before it restores the file"}},
		Params: []ParamSpec{
			{Name: "path", Type: "string", Required: true, Doc: "the file to replace, relative to the workspace"},
			{Name: "content", Type: "string", Required: true, Doc: "the file's new content; the empty string is allowed and means an empty file"},
			{Name: "expected_sha_before", Type: "string", Required: true, Doc: "sha256:<64 hex> of the file as the Harness read it; the write happens only if it still has that content"},
			{Name: "test_path", Type: "string", Required: true, Doc: "the test to run afterwards, relative to the workspace"},
		},


		Check: func(p map[string]any) (string, string) {
			if s, _ := p["expected_sha_before"].(string); !shaHex.MatchString(s) {
				return "parameters.expected_sha_before", "must be sha256:<64 lowercase hex>"
			}
			return "", ""
		},
		Steps: editAndTestSteps,
	},
	ActionClone: {
		Name:       ActionClone,
		Version:    "1.0.0",
		Summary:    "Copy a workspace onto a placement as a new Agent with a new identity",
		TargetKind: "workspace",

		Effects:    Effects{Read: true, Write: true, ExternalSideEffect: true},
		Properties: Properties{Reversible: true, Idempotent: true},
		Consequences: []Consequence{
			{Class: ConsequenceReversible, When: WhenAlways,
				Rendered: "creates a second workspace; deleting it undoes this"},
			{Class: ConsequenceDataEgress, When: WhenLeavesCurrentHost,
				Rendered: "copies this workspace's contents onto a placement that is not this host"},
			{Class: ConsequenceCostsMoney, When: WhenProviderPriced,
				Rendered: "the placement charges for what it runs"},
		},
		PlacementKey: "to_placement",
		Params: []ParamSpec{
			{Name: "name", Type: "string", Required: true, Doc: "the new Workspace's name; a name already taken under this tenant is refused at submission (PRECONDITION_UNMET)"},
			{Name: "to_placement", Type: "string", Required: true, Enum: []string{PlacementLocal}, Doc: "a provider id. " + placementDoc},
		},











		Check: nil,
		Steps: cloneSteps,
	},










	ActionInspect: {
		Name:       ActionInspect,
		Version:    "1.0.0",
		Summary:    "Read a workspace's live placement, lifecycle state and resources",
		TargetKind: "workspace",
		Effects:    Effects{Read: true},
		Properties: Properties{Reversible: true, Idempotent: true},
		Consequences: []Consequence{{Class: ConsequenceReadOnly, When: WhenAlways,
			Rendered: "reads this workspace's live placement, lifecycle state and resources; changes nothing"}},


		RequiresGrants: []string{GrantAgentRead},
		Params:         []ParamSpec{},
		Check:          nil,
		Steps:          inspectSteps,
	},


	ActionCreate: {
		Name:    ActionCreate,
		Version: "1.0.0",




		Summary:    "Create an empty persistent workspace with a managed runtime",
		TargetKind: "none",





		Effects:    Effects{Read: true, Write: true, ExternalSideEffect: true},
		Properties: Properties{Reversible: true, Idempotent: true},
		Consequences: []Consequence{
			{Class: ConsequenceReversible, When: WhenAlways,
				Rendered: "brings a new Agent and its workspace into being; deleting them undoes this"},
			{Class: ConsequenceDataEgress, When: WhenLeavesCurrentHost,





				Rendered: "creates the new Agent and its workspace on a placement that is not this host"},
			{Class: ConsequenceCostsMoney, When: WhenProviderPriced,
				Rendered: "the placement charges for the runtime this starts"},
		},
		RequiresGrants: []string{GrantAgentCreate},
		PlacementKey:   "placement",






		Params: []ParamSpec{
			{Name: "name", Type: "string", Required: true, Doc: "the new Workspace's name; a name already taken under this tenant is refused at submission (PRECONDITION_UNMET)"},
			{Name: "harness", Type: "string", Required: true, Closed: true, EnumFact: FactHarnessesAvailable,
				Doc: "the runtime the new Agent runs: the name of a Workspace Template installed on the Provider. Closed, and the Provider says which: an unknown one would otherwise only be discovered after the approval, after the container"},
			{Name: "placement", Type: "string", Required: true, Enum: []string{PlacementLocal},
				Doc: "a provider id, NO DEFAULT — it decides the effective consequences, and a default would be a governance decision made by omission. " + placementDoc},
		},










		Check: nil,
		Steps: createSteps,
	},
}



const placementDoc = "a placement this build has no traits for is LEGAL: every consequence condition about it is then treated as holding, " +
	"so the plan reports data-egress and costs-money and a person is asked"




func resolveAction(name, version string) (ActionDecl, bool) {
	d, ok := lookupAction(name)
	if !ok {
		return ActionDecl{}, false
	}
	if version == d.Version || version == majorOf(d.Version) {
		return d, true
	}
	return ActionDecl{}, false
}

func majorOf(semver string) string {
	for i, c := range semver {
		if c == '.' {
			return semver[:i]
		}
	}
	return semver
}
