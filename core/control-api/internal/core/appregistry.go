// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"fmt"
	"sort"
)







type KnownStepsFunc func(ctx context.Context, appID string) ([]string, error)


























func RegisterApp(ctx context.Context, appID string, a AppDeclaration, knownSteps KnownStepsFunc) error {
	if appID == "" {
		return fmt.Errorf("register app: no app id")
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if _, taken := lookupAction(a.Name); taken {


















		return fmt.Errorf("register app %q: the action name %q is already declared by this build", appID, a.Name)
	}
	known, err := knownSteps(ctx, appID)
	if err != nil {




		return fmt.Errorf("register app %q: could not ask its executor which steps it knows: %w", appID, err)
	}
	set := make(map[string]bool, len(known))
	for _, s := range known {
		set[s] = true
	}
	missing := make([]string, 0, len(a.Steps))
	for _, s := range a.Steps {
		if !set[s.Name] {
			missing = append(missing, s.Name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("register app %q: its declaration names step(s) %v that its own executor does not handle — "+
			"admitting it would put the action in discovery and refuse it at the executor, after a transaction exists "+
			"and possibly after a person was asked", appID, missing)
	}
	return nil
}
