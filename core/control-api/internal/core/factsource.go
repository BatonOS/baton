// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"
	"fmt"
	"log/slog"
	"path"
)































type FactsUnavailable func(ctx context.Context, reason string)













func ProviderFacts(p *ProviderClient, declaredBoundary string, logger *slog.Logger, unavailable FactsUnavailable) FactResolver {
	note := func(ctx context.Context, reason string) {
		logger.Warn("governance facts unavailable; conditions that need them are treated as holding", "reason", reason)
		if unavailable != nil {
			unavailable(ctx, reason)
		}
	}
	return func(ctx context.Context, proposal map[string]any) []GovernanceFact {
		facts, err := p.EnvironmentFacts(ctx, destinationOf(proposal))
		if err != nil {



			note(ctx, "the local Provider could not be asked: "+err.Error())
			return nil
		}
		kept := make([]GovernanceFact, 0, len(facts))
		for _, f := range facts {
			if why, ok := f.Valid(); !ok {
				note(ctx, "the local Provider stated a fact this build refuses: "+why)
				continue
			}
			kept = append(kept, f)
		}
		if err := boundaryAgrees(declaredBoundary, kept); err != nil {





			logger.Error("REFUSING the local Provider's environment facts: it does not serve the workspace this control plane was configured with", "error", err)
			note(ctx, "refused: "+err.Error())
			return nil
		}
		return kept
	}
}





















func boundaryAgrees(declared string, facts []GovernanceFact) error {
	stated, has := factNamed(facts, FactWorkspaceBoundary)
	switch {
	case declared == "" && !has:
		return nil
	case declared == "" && has:
		return fmt.Errorf("the local Provider serves the workspace %s and this control plane declares none (spec.workspaceBoundary / BATON_WORKSPACE_BOUNDARY)", stated.Value)
	case declared != "" && !has:
		return fmt.Errorf("this control plane declares the workspace boundary %s and the local Provider serves no workspace (it was started without --workspace-dir)", declared)
	case path.Clean(declared) != stated.Value:
		return fmt.Errorf("this control plane declares the workspace boundary %s and the local Provider states %s (source %s)", path.Clean(declared), stated.Value, stated.Source)
	}
	return nil
}






















func CheckWorkspaceBoundary(ctx context.Context, p *ProviderClient, declaredBoundary string, logger *slog.Logger) error {
	facts, err := p.EnvironmentFacts(ctx, "")
	if err != nil {
		logger.Warn("could not confirm the workspace boundary with the local Provider at startup; it is checked again on every decision",
			"declared", declaredBoundary, "error", err)
		return nil
	}
	if err := boundaryAgrees(declaredBoundary, facts); err != nil {
		return fmt.Errorf("core: refusing to start: %w", err)
	}
	if declaredBoundary != "" {
		logger.Info("workspace boundary confirmed with the local Provider", "boundary", path.Clean(declaredBoundary))
	}
	return nil
}
