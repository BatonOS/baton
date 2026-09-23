// SPDX-License-Identifier: Apache-2.0

package server

import (
	"path/filepath"
	"os"
	"github.com/batonos/baton/core/control-api/internal/eventlog"
	"context"
	"time"

	"github.com/batonos/baton/core/control-api/internal/core"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)




const transactionExpiryInterval = 30 * time.Second









func (s *Server) transactionLoop(ctx context.Context) {

	if s.cfg.ReadOnly() {
		return
	}




	ctx = core.WithFacts(ctx, s.facts)
	ctx = withFactsAppeared(ctx, s.log)
	ctx = core.WithEvidenceWriter(ctx, s.transactionEvidenceWriter())
	txs := s.store.Transactions()
	results, err := core.Recover(ctx, txs, s.store.Interactions(), time.Now())
	if err != nil {
		s.logger.Error("transaction recovery: could not list non-terminal transactions", "error", err)
	}
	for _, r := range results {
		if r.Err != nil {
			s.logger.Error("transaction recovery refused", "transaction_id", r.ID, "state", string(r.State), "error", r.Err)
			continue
		}
		if r.Reconcile {
			s.logger.Info("transaction was executing: handed to the executor to reconcile with the Provider", "transaction_id", r.ID)
			continue
		}
		s.logger.Info("transaction recovered", "transaction_id", r.ID, "was", string(r.State), "now", string(r.Now))
	}


	go s.executionLoop(ctx)

	ticker := time.NewTicker(transactionExpiryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := core.ExpireDue(ctx, txs, time.Now())
			if err != nil {
				s.logger.Warn("transaction expiry sweep", "error", err)
			}
			for _, id := range ids {
				s.logger.Info("transaction expired", "transaction_id", id)
			}
		}
	}
}








func (s *Server) facts(ctx context.Context, p map[string]any) []core.GovernanceFact {
	if s.cfg.Spec.ProviderSocket == "" {
		return nil
	}
	s.factsOnce.Do(func() {





		s.factsFn = core.ProviderFacts(core.NewProviderClient(s.cfg.Spec.ProviderSocket), s.cfg.Spec.WorkspaceBoundary, s.logger, factsUnavailableSink(s.log))
	})
	return s.factsFn(ctx, p)
}




const transactionExecutionInterval = 2 * time.Second







func (s *Server) executionLoop(ctx context.Context) {
	if s.cfg.Spec.ProviderSocket == "" {
		s.logger.Warn("no local Provider configured (BATON_PROVIDER_SOCKET): approved transactions will stay approved and not execute")
		return
	}











	apps := core.AppClients(s.cfg.Spec.DataDir)
	loaded, refused := core.LoadApps(ctx, s.cfg.Spec.DataDir, core.AskExecutors(apps))
	core.SetApps(loaded)
	for _, err := range refused {
		s.logger.Warn("an App declaration was refused and its action is NOT in the catalogue", "error", err)









		if s.log != nil {
			s.log.System(ctx, "core.app_declaration_refused", map[string]any{"error": err.Error()})
		}
	}
	if len(loaded) > 0 {
		names := make([]string, 0, len(loaded))
		for _, a := range loaded {
			names = append(names, a.Decl.Name+"@"+a.AppID)
		}
		s.logger.Info("App actions registered", "count", len(loaded), "actions", names)
	}
	e := &core.Executor{
		Store: s.store, Provider: core.NewProviderClient(s.cfg.Spec.ProviderSocket),
		Apps: apps,
		Log:  s.log, Now: time.Now, PollEvery: 2 * time.Second, Logger: s.logger,
	}
	s.logger.Info("executing approved transactions through the local Provider", "socket", s.cfg.Spec.ProviderSocket)
	ticker := time.NewTicker(transactionExecutionInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		list, err := s.store.Transactions().ListNonTerminal(ctx)
		if err != nil {
			s.logger.Warn("transaction execution: could not list", "error", err)
			continue
		}
		for _, t := range list {
			var got *spi.Transaction
			var err error
			switch t.State {
			case spi.TxApproved:
				got, err = e.Execute(ctx, t.ID)
			case spi.TxExecuting:



				got, err = e.Resume(ctx, t.ID)
			default:
				continue
			}
			if err != nil {
				s.logger.Error("transaction execution", "transaction_id", t.ID, "error", err)
				continue
			}
			s.logger.Info("transaction executed", "transaction_id", t.ID, "state", string(got.State))
		}
	}
}















func withFactsAppeared(ctx context.Context, log *eventlog.Log) context.Context {
	if log == nil {
		return ctx
	}
	return core.WithFactsAppeared(ctx, func(ctx context.Context, txID string, names []string) {
		log.System(ctx, "core.governance_facts_appeared", map[string]any{
			"transaction_id": txID, "facts": names,
		})
	})
}












func factsUnavailableSink(log *eventlog.Log) core.FactsUnavailable {
	if log == nil {
		return nil
	}
	return func(ctx context.Context, reason string) {
		log.System(ctx, "core.governance_facts_unavailable", map[string]any{"reason": reason})
	}
}















func (s *Server) transactionEvidenceWriter() core.EvidenceWriter {
	dir := s.cfg.Spec.DataDir
	if dir == "" {
		return nil
	}
	return func(ctx context.Context, t *spi.Transaction) {
		arts, err := core.ExportTransaction(t)
		if err != nil {
			s.logger.Warn("could not build this transaction's record", "transaction", t.ID, "error", err)
			return
		}
		out := filepath.Join(dir, core.TransactionsDirName, t.ID)
		if err := os.MkdirAll(out, 0o700); err != nil {
			s.logger.Warn("could not create the transaction record directory", "dir", out, "error", err)
			return
		}
		for _, a := range arts {
			if err := os.WriteFile(filepath.Join(out, a.Name), a.Bytes, 0o600); err != nil {
				s.logger.Warn("could not write a transaction record artifact", "file", a.Name, "error", err)
				return
			}
		}
	}
}
