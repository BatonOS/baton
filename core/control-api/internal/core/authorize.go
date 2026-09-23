// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)









const CodeNotGranted = "NOT_GRANTED"




type NotGrantedError struct{ Outcome *OutcomeError }

func (e *NotGrantedError) Error() string { return e.Outcome.Message }



type authorization struct {
	Granted        bool
	RequiredGrants []string





	Source    string
	Subject   string
	Action    string
	CheckedAt time.Time
}

const (
	authSourceGrants      = "core_action_grants"
	authSourceDeclaration = "declaration"
)














func authorize(ctx context.Context, grants spi.GrantStore, subject string, decl ActionDecl, now time.Time) (*authorization, error) {
	a := &authorization{
		Granted: true, RequiredGrants: decl.RequiresGrants,
		Source: authSourceDeclaration, Subject: subject, Action: decl.Name, CheckedAt: now,
	}
	if a.RequiredGrants == nil {
		a.RequiredGrants = []string{}
	}
	if len(a.RequiredGrants) == 0 {



		return a, nil
	}
	a.Source = authSourceGrants
	if grants == nil {



		return nil, errors.New("core: no grant store to check action authorization against")
	}
	held, err := grants.List(ctx, spi.DefaultTenant, subject, spi.ActionActionInvoke)
	if err != nil {
		return nil, err
	}
	for _, want := range a.RequiredGrants {
		allowed := false
		for i := range held {
			g := &held[i]
			if g.Object != want || g.StatusAt(now) != spi.GrantActive {
				continue
			}
			if g.Effect == spi.GrantDeny {



				allowed = false
				break
			}
			allowed = true
		}
		if !allowed {
			return nil, &NotGrantedError{Outcome: notGranted(decl, want, a.RequiredGrants)}
		}
	}
	return a, nil
}







func notGranted(decl ActionDecl, missing string, required []string) *OutcomeError {
	all := append([]string(nil), required...)
	sort.Strings(all)
	return &OutcomeError{
		Code:    CodeNotGranted,
		Message: fmt.Sprintf("%s requires the grant %q, which this subject does not hold", decl.Name, missing),
		Remediation: []Remedy{
			{Text: fmt.Sprintf("Ask an operator for %q on this network.", missing)},
		},
		Detail: map[string]any{
			"action": decl.Name, "missing_grant": missing, "required_grants": all,
		},
	}
}
