// SPDX-License-Identifier: Apache-2.0

package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)


























type AppDeclaration struct {
	Name         string        `json:"name"`
	Version      string        `json:"version"`
	Summary      string        `json:"summary"`
	TargetKind   string        `json:"target_kind"`
	Effects      Effects       `json:"effects"`
	Properties   Properties    `json:"properties"`
	Consequences []Consequence `json:"consequences"`
	RequiresGrants []string    `json:"requires_grants"`
	Params       []ParamSpec   `json:"params"`
	Steps        []AppStep     `json:"steps"`



	DestinationKey string `json:"destination_key"`
}






type AppStep struct {
	Name string `json:"name"`

	Summary string `json:"summary"`
}















func DecodeAppDeclaration(raw []byte) (AppDeclaration, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var a AppDeclaration
	if err := d.Decode(&a); err != nil {
		return AppDeclaration{}, fmt.Errorf("app declaration: %w", err)
	}
	return a, nil
}


const coreNamespace = "baton."







func (a AppDeclaration) Validate() error {
	switch {
	case a.Name == "":
		return fmt.Errorf("app declaration: name is required")
	case strings.HasPrefix(a.Name, coreNamespace):



		return fmt.Errorf("app declaration %q: the %s namespace is Core's", a.Name, coreNamespace)
	case a.Version == "":
		return fmt.Errorf("app declaration %q: version is required", a.Name)
	case a.Summary == "":



		return fmt.Errorf("app declaration %q: summary is required — it is what a person reads when deciding", a.Name)
	case len(a.Steps) == 0:
		return fmt.Errorf("app declaration %q: at least one step is required", a.Name)
	case len(a.Steps) > 1:

		return fmt.Errorf("app declaration %q: this build takes exactly one step per action (S2 boundary, PM 2026-09-20); it declares %d", a.Name, len(a.Steps))
	}
	for i, c := range a.Consequences {
		if !KnownConsequenceClass(c.Class) {
			return fmt.Errorf("app declaration %q: consequences[%d].class %q is not one of the five", a.Name, i, c.Class)
		}
		if !knownConditionWord(c.When) {
			return fmt.Errorf("app declaration %q: consequences[%d].when %q is not a condition word this build evaluates", a.Name, i, c.When)
		}
		if c.Rendered == "" {



			return fmt.Errorf("app declaration %q: consequences[%d] has no rendered sentence", a.Name, i)
		}
	}
	for i, s := range a.Steps {
		if s.Name == "" {
			return fmt.Errorf("app declaration %q: steps[%d] has no name", a.Name, i)
		}
	}
	return a.validateDestinationKey()
}












func (a AppDeclaration) validateDestinationKey() error {
	if a.DestinationKey != "" {
		known := false
		for _, p := range a.Params {
			if p.Name == a.DestinationKey {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("app declaration %q: destination_key %q is not one of its declared params", a.Name, a.DestinationKey)
		}
		return nil
	}
	for i, c := range a.Consequences {
		if c.When == WhenOutsideWorkspace {
			return fmt.Errorf("app declaration %q: consequences[%d] is conditioned on %s and it declares no destination_key — nothing could compute that condition, and an uncomputable condition is treated as HOLDING", a.Name, i, WhenOutsideWorkspace)
		}
	}
	return nil
}










func knownConditionWord(when string) bool {
	switch when {
	case WhenAlways, WhenLeavesCurrentHost, WhenProviderPriced, WhenChangesPricedRsrcs,
		WhenOutsideWorkspace:
		return true
	}
	return false
}












func (a AppDeclaration) ActionDecl(appID, sha string) ActionDecl {
	steps := make([]AppStep, len(a.Steps))
	copy(steps, a.Steps)



	if appID == "" {
		panic("core: an App declaration reached ActionDecl with no app id — the step could not be stamped")
	}
	executor := ExecutorAppPrefix + appID
	return ActionDecl{
		Name: a.Name, Version: a.Version, Summary: a.Summary, TargetKind: a.TargetKind,
		Effects: a.Effects, Properties: a.Properties, Consequences: a.Consequences,
		RequiresGrants: a.RequiresGrants, Params: a.Params,
		DestinationKey: a.DestinationKey,



		Declarer:       DeclaredBy{Kind: DeclaredByApp, ID: appID, DeclarationSHA256: &sha},
		DeclarationSHA: sha,





















		Steps: func(map[string]any) []spi.TxStep {



			out := make([]spi.TxStep, len(steps))
			for i, s := range steps {
				out[i] = spi.TxStep{Index: i, Name: s.Name, Summary: s.Summary, Executor: executor}
			}
			return out
		},
	}
}

