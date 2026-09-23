// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"
	"time"
)


















type GovernanceFact struct {
	Name  string `json:"name"`
	Value string `json:"value"`



	Source string `json:"source"`

	ResolvedAt string `json:"resolved_at"`
}




const (




	FactHarnessesAvailable = "harnesses_available"


	FactWorkspaceBoundary = "workspace_boundary"


















	FactDestinationResolved = "destination_resolved"
)


func factNamed(facts []GovernanceFact, name string) (GovernanceFact, bool) {
	for _, f := range facts {
		if f.Name == name {
			return f, true
		}
	}
	return GovernanceFact{}, false
}










func underBoundary(resolved, boundary string) bool {
	if boundary == "" || resolved == "" {
		return false
	}
	b := path.Clean(boundary)
	r := path.Clean(resolved)
	if r == b {
		return true
	}
	return strings.HasPrefix(r, strings.TrimSuffix(b, "/")+"/")
}







func (f GovernanceFact) Valid() (string, bool) {
	switch {
	case f.Name == "":
		return "a fact with no name", false
	case f.Value == "":
		return "fact " + f.Name + " has no value", false
	case f.Source == "" || f.Source == "system" || isCoreItself(f.Source):












		return "fact " + f.Name + " must name the party answerable for it (which Provider, which instance, which method) — ⊘ \"core\", ⊘ \"system\", ⊘ empty", false
	case f.ResolvedAt == "":
		return "fact " + f.Name + " has no resolved_at", false
	}
	if _, err := time.Parse(time.RFC3339, f.ResolvedAt); err != nil {
		return "fact " + f.Name + " has an unreadable resolved_at: " + f.ResolvedAt, false
	}
	return "", true
}




























































func isCoreItself(source string) bool {
	s := strings.ToLower(strings.TrimSpace(source))
	for _, claim := range []string{"core", "baton-core", "baton core"} {
		if s == claim || strings.HasPrefix(s, claim+":") || strings.HasPrefix(s, claim+"/") {
			return true
		}
	}
	return false
}















type FactResolver func(ctx context.Context, p map[string]any) []GovernanceFact

type factsKey struct{}


func WithFacts(ctx context.Context, f FactResolver) context.Context {
	return context.WithValue(ctx, factsKey{}, f)
}







func factsFor(ctx context.Context, p map[string]any) []GovernanceFact {
	f, _ := ctx.Value(factsKey{}).(FactResolver)
	if f == nil {
		return nil
	}
	return f(ctx, p)
}




func destinationOf(p map[string]any) string {
	a, _ := p["action"].(map[string]any)
	name, _ := a["name"].(string)
	decl, ok := lookupAction(name)
	if !ok || decl.DestinationKey == "" {
		return ""
	}
	params, _ := p["parameters"].(map[string]any)
	dest, _ := params[decl.DestinationKey].(string)
	return dest
}



















type FactsAppeared func(ctx context.Context, txID string, names []string)

type factsAppearedKey struct{}




func WithFactsAppeared(ctx context.Context, f FactsAppeared) context.Context {
	return context.WithValue(ctx, factsAppearedKey{}, f)
}

func factsAppearedFor(ctx context.Context, txID string, names []string) {
	if f, _ := ctx.Value(factsAppearedKey{}).(FactsAppeared); f != nil {
		f(ctx, txID, names)
	}
}












func factStringSet(facts []GovernanceFact, name string) (values []string, source string, ok bool) {
	for _, f := range facts {
		if f.Name != name {
			continue
		}
		var got []string
		if err := json.Unmarshal([]byte(f.Value), &got); err != nil || got == nil {
			return nil, f.Source, false
		}
		canon := slices.Clone(got)
		slices.Sort(canon)
		canon = slices.Compact(canon)
		b, err := json.Marshal(canon)
		if err != nil || string(b) != f.Value {
			return nil, f.Source, false
		}
		return got, f.Source, true
	}
	return nil, "", false
}
