// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)











const (
	ActionAvailable        = "available"
	ActionRequiresApproval = "requires-approval"
)









type CatalogueEntry struct {
	Name         string        `json:"name"`
	Version      string        `json:"version"`
	Summary      string        `json:"summary"`
	TargetKind   string        `json:"target_kind"`
	Effects      Effects       `json:"effects"`
	Properties   Properties    `json:"properties"`
	Params       []ParamSpec   `json:"params"`
	Consequences []Consequence `json:"consequences"`



	DeclaredBy DeclaredBy `json:"declared_by"`










	State string `json:"state"`
}










func Catalogue(ctx context.Context, grants spi.GrantStore, subject string, now time.Time) ([]CatalogueEntry, error) {
	names := actionNames()



	var facts []GovernanceFact
	asked := false
	out := make([]CatalogueEntry, 0, len(names))
	for _, name := range names {
		decl, _ := lookupAction(name)
		if _, err := authorize(ctx, grants, subject, decl, now); err != nil {
			if _, refused := err.(*NotGrantedError); refused {
				continue
			}





			return nil, err
		}
		e := entryFor(decl)
		if hasFactEnum(e.Params) {
			if !asked {
				facts, asked = factsFor(ctx, nil), true
			}
			e.Params = withFactEnums(e.Params, facts)
		}
		out = append(out, e)
	}
	return out, nil
}

func entryFor(decl ActionDecl) CatalogueEntry {
	e := CatalogueEntry{
		Name: decl.Name, Version: decl.Version, Summary: decl.Summary,
		TargetKind: decl.TargetKind, Effects: decl.Effects, Properties: decl.Properties,
		Params: decl.Params, Consequences: decl.Consequences,




























		DeclaredBy: DeclaredBy{},
		State:      ActionAvailable,
	}









	if d := declaredByFor(decl.Name); d != nil {
		e.DeclaredBy = *d
	}
	if e.Params == nil {
		e.Params = []ParamSpec{}
	}
	if required, _, _, _ := decideApproval(decl, maximumConsequences(decl)); required {
		e.State = ActionRequiresApproval
	}
	return e
}








func maximumConsequences(decl ActionDecl) []map[string]any {
	out := make([]map[string]any, 0, len(decl.Consequences))
	for _, c := range decl.Consequences {
		out = append(out, map[string]any{"class": c.Class, "rendered": c.Rendered})
	}
	return out
}

func hasFactEnum(specs []ParamSpec) bool {
	for _, s := range specs {
		if s.EnumFact != "" {
			return true
		}
	}
	return false
}
