// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type transactionStore struct{ db *DB }

const txColumns = `transaction_id, tenant_id, proposal_json, proposal_digest, actor, via_json,
	action_name, action_version, state, plan_digest, plan_detail_json,
	approval_required, approval_basis, approval_rule_ref, approval_request_id,
	approval_expires_at, approval_decision_json,
	approval_required_rung, approval_cleared_rung, outcome_ref, receipt_ref, created_at, updated_at,
	interaction_id, idempotency_key, target_kind, target_id, error_json, provider_instance_id, authorization_json, rechecked_facts_json`








var txPlaceholders = strings.TrimSuffix(strings.Repeat("?,", strings.Count(txColumns, ",")+1), ",")




func (s *transactionStore) Create(ctx context.Context, t *spi.Transaction, opts spi.CreateOptions) (*spi.Transaction, error) {
	if t.State != spi.TxProposed {
		return nil, fmt.Errorf("%w: a transaction is created proposed, not %q", spi.ErrInvalid, t.State)
	}
	if t.TenantID == "" {
		t.TenantID = spi.DefaultTenant
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	t.UpdatedAt = t.CreatedAt
	var held *spi.Transaction
	var sentinel error
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		if t.IdempotencyKey != "" {
			h, err := scanTransaction(tx.QueryRowContext(ctx,
				`SELECT `+txColumns+` FROM transactions WHERE actor = ? AND idempotency_key = ?`, t.Actor, t.IdempotencyKey))
			if err == nil {
				held, sentinel = h, spi.ErrIdempotencyKeyHeld
				return nil
			}
			if !errors.Is(err, spi.ErrNotFound) {
				return err
			}
		}
		if opts.CheckInteraction && t.InteractionID != "" {
			o, err := scanTransaction(tx.QueryRowContext(ctx, `SELECT `+txColumns+` FROM transactions
				WHERE interaction_id = ? AND action_name = ? AND target_kind = ? AND target_id = ?
				  AND state IN ('proposed','pending_approval','approved','executing')
				ORDER BY created_at, transaction_id LIMIT 1`,
				t.InteractionID, t.ActionName, t.TargetKind, t.TargetID))
			if err == nil {
				held, sentinel = o, spi.ErrOpenOnInteraction
				return nil
			}
			if !errors.Is(err, spi.ErrNotFound) {
				return err
			}
		}





















		if opts.CheckSamePlanOpen && t.Plan != nil && t.Plan.PlanDigest != "" {
			o, err := scanTransaction(tx.QueryRowContext(ctx, `SELECT `+txColumns+` FROM transactions
				WHERE action_name = ? AND target_kind = ? AND target_id = ? AND plan_digest = ?
				  AND state IN ('proposed','pending_approval','approved','executing')
				ORDER BY created_at, transaction_id LIMIT 1`,
				t.ActionName, t.TargetKind, t.TargetID, t.Plan.PlanDigest))
			if err == nil {
				held, sentinel = o, spi.ErrSamePlanOpen
				return nil
			}
			if !errors.Is(err, spi.ErrNotFound) {
				return err
			}
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO transactions (`+txColumns+`) VALUES (`+txPlaceholders+`)`,
			t.ID, t.TenantID, orEmptyJSON(t.Proposal), t.ProposalDigest, t.Actor, orEmptyJSON(t.Via),
			t.ActionName, t.ActionVersion, string(t.State), planDigest(t.Plan), planDetail(t.Plan),
			boolInt(t.Approval.Required), t.Approval.Basis, t.Approval.RuleRef, t.Approval.RequestID,
			nullableTime(t.Approval.ExpiresAt), nullableJSON(t.Approval.Decision),
			t.Approval.RequiredRung, t.Approval.ClearedRung, t.OutcomeRef, t.ReceiptRef,
			fmtTime(t.CreatedAt), fmtTime(t.UpdatedAt),
			nullableString(t.InteractionID), nullableString(t.IdempotencyKey), t.TargetKind, t.TargetID, nullableJSON(t.Error),
			nullableString(t.ProviderInstanceID), nullableString(string(t.Authorization)),
			nullableJSON(t.RecheckedFacts))
		if isUniqueViolation(err) {
			return spi.ErrConflict
		}
		if err != nil {
			return err
		}
		if err := insertHistory(ctx, tx, t.ID, 0, spi.TxStateEntry{State: t.State, At: t.CreatedAt, By: spi.TxByCore}); err != nil {
			return err
		}
		if t.Plan != nil {
			return writeSteps(ctx, tx, t.ID, t.Plan.Steps)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if sentinel != nil {
		if err := s.fill(ctx, held); err != nil {
			return nil, err
		}
		return held, sentinel
	}
	return t, nil
}

func (s *transactionStore) GetByIdempotencyKey(ctx context.Context, actor, key string) (*spi.Transaction, error) {
	t, err := scanTransaction(s.db.queryRow(ctx,
		`SELECT `+txColumns+` FROM transactions WHERE actor = ? AND idempotency_key = ?`, actor, key))
	if err != nil {
		return nil, err
	}
	if err := s.fill(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *transactionStore) Get(ctx context.Context, id string) (*spi.Transaction, error) {
	t, err := scanTransaction(s.db.queryRow(ctx, `SELECT `+txColumns+` FROM transactions WHERE transaction_id = ?`, id))
	if err != nil {
		return nil, err
	}
	if err := s.fill(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *transactionStore) ListNonTerminal(ctx context.Context) ([]spi.Transaction, error) {
	return s.list(ctx, `SELECT `+txColumns+` FROM transactions
		WHERE state IN ('proposed','pending_approval','approved','executing')
		ORDER BY created_at, transaction_id`)
}

func (s *transactionStore) ListRecentByActor(ctx context.Context, actor string, limit int) ([]spi.Transaction, error) {
	return s.list(ctx, `SELECT `+txColumns+` FROM transactions WHERE actor = ?
		ORDER BY created_at DESC, transaction_id DESC LIMIT ?`, actor, limit)
}

func (s *transactionStore) list(ctx context.Context, query string, args ...any) ([]spi.Transaction, error) {
	rows, err := s.db.query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []spi.Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *t)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fill(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}





func (s *transactionStore) Transition(ctx context.Context, tr spi.TxTransition) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		set := `state = ?, updated_at = ?`
		args := []any{string(tr.To), fmtTime(tr.At)}
		if tr.Plan != nil {
			set += `, plan_digest = ?, plan_detail_json = ?`
			args = append(args, planDigest(tr.Plan), planDetail(tr.Plan))
		}
		if a := tr.Approval; a != nil {
			set += `, approval_required = ?, approval_basis = ?, approval_rule_ref = ?,
				approval_request_id = ?, approval_expires_at = ?, approval_decision_json = ?,
				approval_required_rung = ?, approval_cleared_rung = ?`
			args = append(args, boolInt(a.Required), a.Basis, a.RuleRef, a.RequestID,
				nullableTime(a.ExpiresAt), nullableJSON(a.Decision),
				a.RequiredRung, a.ClearedRung)
		}
		if len(tr.Error) > 0 {
			set += `, error_json = ?`
			args = append(args, string(tr.Error))
		}
		args = append(args, tr.ID, string(tr.From))
		res, err := tx.ExecContext(ctx, `UPDATE transactions SET `+set+` WHERE transaction_id = ? AND state = ?`, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM transactions WHERE transaction_id = ?`, tr.ID).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return spi.ErrNotFound
			}
			return spi.ErrConflict
		}
		var seq int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(seq), -1) + 1 FROM transaction_history WHERE transaction_id = ?`, tr.ID).Scan(&seq); err != nil {
			return err
		}
		if err := insertHistory(ctx, tx, tr.ID, seq, spi.TxStateEntry{State: tr.To, At: tr.At, By: tr.By}); err != nil {
			return err
		}
		if tr.Plan != nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM transaction_steps WHERE transaction_id = ?`, tr.ID); err != nil {
				return err
			}
			return writeSteps(ctx, tx, tr.ID, tr.Plan.Steps)
		}
		return nil
	})
}





func (s *transactionStore) UpdateStep(ctx context.Context, u spi.TxStepUpdate) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		set := `status = ?`
		args := []any{string(u.To)}
		switch u.To {
		case spi.StepRunning:
			set += `, started_at = ?, nonce = ?`
			args = append(args, fmtTime(u.At), nullableString(u.Nonce))
		case spi.StepSucceeded, spi.StepFailed, spi.StepUnknown:
			set += `, ended_at = ?, evidence_json = ?`
			args = append(args, fmtTime(u.At), nullableJSON(u.Evidence))
		}
		args = append(args, u.ID, u.Index, string(u.From), u.ID, string(spi.TxExecuting))
		res, err := tx.ExecContext(ctx, `UPDATE transaction_steps SET `+set+`
			WHERE transaction_id = ? AND idx = ? AND status = ?
			  AND (SELECT state FROM transactions WHERE transaction_id = ?) = ?`, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM transaction_steps WHERE transaction_id = ? AND idx = ?`, u.ID, u.Index).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return spi.ErrNotFound
			}
			return spi.ErrConflict
		}
		_, err = tx.ExecContext(ctx, `UPDATE transactions SET updated_at = ? WHERE transaction_id = ?`, fmtTime(u.At), u.ID)
		return err
	})
}





func (s *transactionStore) RecordRecheckedFacts(ctx context.Context, id string, facts json.RawMessage) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE transactions SET rechecked_facts_json = ? WHERE transaction_id = ?`, nullableJSON(facts), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}




func (s *transactionStore) PinProviderInstance(ctx context.Context, id, instance string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE transactions SET provider_instance_id = ?
			WHERE transaction_id = ? AND (provider_instance_id IS NULL OR provider_instance_id = ?)`, instance, id, instance)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM transactions WHERE transaction_id = ?`, id).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				return spi.ErrNotFound
			}
			return spi.ErrConflict
		}
		return nil
	})
}


func (s *transactionStore) fill(ctx context.Context, t *spi.Transaction) error {
	rows, err := s.db.query(ctx,
		`SELECT state, at, by FROM transaction_history WHERE transaction_id = ? ORDER BY seq`, t.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var st, at, by string
		if err := rows.Scan(&st, &at, &by); err != nil {
			rows.Close()
			return err
		}
		when, _ := parseTime(at)
		t.History = append(t.History, spi.TxStateEntry{State: spi.TxState(st), At: when, By: spi.TxBy(by)})
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if t.Plan == nil {
		return nil
	}
	rows, err = s.db.query(ctx, `SELECT idx, name, executor, summary, operation_id, status, started_at, ended_at, nonce, evidence_json
		FROM transaction_steps WHERE transaction_id = ? ORDER BY idx`, t.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			st              spi.TxStep
			status          string
			start, end      sql.NullString
			nonce, evidence sql.NullString
		)
		if err := rows.Scan(&st.Index, &st.Name, &st.Executor, &st.Summary, &st.OperationID, &status, &start, &end, &nonce, &evidence); err != nil {
			return err
		}
		st.Status = spi.StepStatus(status)
		st.StartedAt, st.EndedAt = nullTime(start), nullTime(end)
		st.Nonce = nonce.String
		if evidence.Valid {
			st.Evidence = json.RawMessage(evidence.String)
		}
		t.Plan.Steps = append(t.Plan.Steps, st)
	}
	return rows.Err()
}

func scanTransaction(sc interface{ Scan(...any) error }) (*spi.Transaction, error) {
	var (
		t                       spi.Transaction
		proposal, via, state    string
		digest, detail, expires sql.NullString
		decision, interaction   sql.NullString
		key, errJSON            sql.NullString
		providerInstance        sql.NullString
		authz                   sql.NullString
		rechecked               sql.NullString
		required                int
		created, updated        string
	)
	err := sc.Scan(&t.ID, &t.TenantID, &proposal, &t.ProposalDigest, &t.Actor, &via,
		&t.ActionName, &t.ActionVersion, &state, &digest, &detail,
		&required, &t.Approval.Basis, &t.Approval.RuleRef, &t.Approval.RequestID,
		&expires, &decision,
		&t.Approval.RequiredRung, &t.Approval.ClearedRung, &t.OutcomeRef, &t.ReceiptRef, &created, &updated,
		&interaction, &key, &t.TargetKind, &t.TargetID, &errJSON, &providerInstance, &authz, &rechecked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Proposal, t.Via, t.State = json.RawMessage(proposal), json.RawMessage(via), spi.TxState(state)
	if digest.Valid {
		t.Plan = &spi.TxPlan{PlanDigest: digest.String}
		if detail.Valid {
			t.Plan.Detail = json.RawMessage(detail.String)
		}
	}
	t.Approval.Required = required != 0
	t.Approval.ExpiresAt = nullTime(expires)
	if decision.Valid {
		t.Approval.Decision = json.RawMessage(decision.String)
	}
	t.InteractionID, t.IdempotencyKey = interaction.String, key.String
	t.ProviderInstanceID = providerInstance.String
	if authz.Valid {
		t.Authorization = json.RawMessage(authz.String)
	}
	if rechecked.Valid {
		t.RecheckedFacts = json.RawMessage(rechecked.String)
	}
	if errJSON.Valid {
		t.Error = json.RawMessage(errJSON.String)
	}
	t.CreatedAt, _ = parseTime(created)
	t.UpdatedAt, _ = parseTime(updated)
	return &t, nil
}

func insertHistory(ctx context.Context, tx *sql.Tx, id string, seq int, e spi.TxStateEntry) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO transaction_history (transaction_id, seq, state, at, by) VALUES (?,?,?,?,?)`,
		id, seq, string(e.State), fmtTime(e.At), string(e.By))
	return err
}

func writeSteps(ctx context.Context, tx *sql.Tx, id string, steps []spi.TxStep) error {
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, `INSERT INTO transaction_steps
			(transaction_id, idx, name, executor, summary, operation_id, status, started_at, ended_at, nonce, evidence_json)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			id, st.Index, st.Name, st.Executor, st.Summary, st.OperationID, string(st.Status),
			nullableTime(st.StartedAt), nullableTime(st.EndedAt), nullableString(st.Nonce), nullableJSON(st.Evidence)); err != nil {
			return err
		}
	}
	return nil
}



func planDigest(p *spi.TxPlan) any {
	if p == nil {
		return nil
	}
	return p.PlanDigest
}

func planDetail(p *spi.TxPlan) any {
	if p == nil || len(p.Detail) == 0 {
		return nil
	}
	return string(p.Detail)
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}



func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

type interactionStore struct{ db *DB }

func (s *interactionStore) Create(ctx context.Context, i *spi.Interaction) error {
	if i.TenantID == "" {
		i.TenantID = spi.DefaultTenant
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO interactions (interaction_id, tenant_id, actor, created_at) VALUES (?,?,?,?)`,
			i.ID, i.TenantID, i.Actor, fmtTime(i.CreatedAt))
		if isUniqueViolation(err) {
			return spi.ErrConflict
		}
		return err
	})
}

func (s *interactionStore) Get(ctx context.Context, id string) (*spi.Interaction, error) {
	var (
		i       spi.Interaction
		created string
	)
	err := s.db.queryRow(ctx, `SELECT interaction_id, tenant_id, actor, created_at FROM interactions WHERE interaction_id = ?`, id).
		Scan(&i.ID, &i.TenantID, &i.Actor, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	i.CreatedAt, _ = parseTime(created)
	return &i, nil
}
