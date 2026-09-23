// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type joinRequestStore struct{ db *DB }

const joinRequestColumns = `request_id, tenant_id, agent, node_public_key, fingerprint, from_ip,
	state, reason, token_hash, created_at, expires_at, decided_at, decided_by`

func scanJoinRequest(sc interface{ Scan(...any) error }) (*spi.JoinRequest, error) {
	var (
		r                        spi.JoinRequest
		state                    string
		created, expires, decided string
	)
	err := sc.Scan(&r.RequestID, &r.TenantID, &r.Agent, &r.NodePublicKeyPEM, &r.Fingerprint, &r.FromIP,
		&state, &r.Reason, &r.TokenHash, &created, &expires, &decided, &r.DecidedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.State = spi.JoinRequestState(state)
	r.CreatedAt, _ = parseTime(created)
	r.ExpiresAt, _ = parseTime(expires)
	if decided != "" {
		r.DecidedAt, _ = parseTime(decided)
	}
	return &r, nil
}

func (s *joinRequestStore) Create(ctx context.Context, r *spi.JoinRequest) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	if r.TenantID == "" {
		r.TenantID = spi.DefaultTenant
	}
	if r.RequestID == "" {
		r.RequestID = "jr_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.State == "" {
		r.State = spi.JoinPending
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {


		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM join_requests WHERE tenant_id = ? AND agent = ? AND state = ?`,
			r.TenantID, r.Agent, string(spi.JoinPending)).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return spi.ErrConflict
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO join_requests (`+joinRequestColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.RequestID, r.TenantID, r.Agent, r.NodePublicKeyPEM, r.Fingerprint, r.FromIP,
			string(r.State), r.Reason, r.TokenHash, fmtTime(r.CreatedAt), fmtTime(r.ExpiresAt), "", "")
		return err
	})
}

func (s *joinRequestStore) Get(ctx context.Context, requestID string) (*spi.JoinRequest, error) {
	return scanJoinRequest(s.db.queryRow(ctx,
		`SELECT `+joinRequestColumns+` FROM join_requests WHERE request_id = ?`, requestID))
}

func (s *joinRequestStore) PendingByAgent(ctx context.Context, tenantID, agent string) (*spi.JoinRequest, error) {
	if tenantID == "" {
		tenantID = spi.DefaultTenant
	}
	return scanJoinRequest(s.db.queryRow(ctx,
		`SELECT `+joinRequestColumns+` FROM join_requests
		 WHERE tenant_id = ? AND agent = ? AND state = ? ORDER BY created_at DESC LIMIT 1`,
		tenantID, agent, string(spi.JoinPending)))
}

func (s *joinRequestStore) List(ctx context.Context, tenantID string, state spi.JoinRequestState) ([]spi.JoinRequest, error) {
	if tenantID == "" {
		tenantID = spi.DefaultTenant
	}
	q := `SELECT ` + joinRequestColumns + ` FROM join_requests WHERE tenant_id = ?`
	args := []any{tenantID}
	if state != "" {
		q += ` AND state = ?`
		args = append(args, string(state))
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []spi.JoinRequest
	for rows.Next() {
		r, err := scanJoinRequest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *joinRequestStore) CountsByState(ctx context.Context, tenantID string) (map[spi.JoinRequestState]int, error) {
	if tenantID == "" {
		tenantID = spi.DefaultTenant
	}
	rows, err := s.db.query(ctx,
		`SELECT state, COUNT(1) FROM join_requests WHERE tenant_id = ? GROUP BY state`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[spi.JoinRequestState]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[spi.JoinRequestState(state)] = n
	}
	return out, rows.Err()
}

func (s *joinRequestStore) Decide(ctx context.Context, requestID string, state spi.JoinRequestState, reason, by string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	if state != spi.JoinAdmitted && state != spi.JoinDenied {
		return errors.New("sqlite: a decision is admitted or denied")
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE join_requests SET state = ?, reason = ?, decided_at = ?, decided_by = ?
			 WHERE request_id = ? AND state = ?`,
			string(state), reason, fmtTime(time.Now().UTC()), by, requestID, string(spi.JoinPending))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {


			return spi.ErrConflict
		}
		return nil
	})
}

func (s *joinRequestStore) SetCollected(ctx context.Context, requestID, tokenHash string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE join_requests SET state = ?, token_hash = ? WHERE request_id = ? AND state = ?`,
			string(spi.JoinCollected), tokenHash, requestID, string(spi.JoinAdmitted))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrConflict
		}
		return nil
	})
}

func (s *joinRequestStore) ConsumeByTokenHash(ctx context.Context, tokenHash string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	if tokenHash == "" {
		return nil
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE join_requests SET state = ? WHERE token_hash = ? AND state = ?`,
			string(spi.JoinConsumed), tokenHash, string(spi.JoinCollected))
		return err
	})
}

func (s *joinRequestStore) ExpirePending(ctx context.Context, now time.Time) (int, error) {
	if s.db.ReadOnly() {
		return 0, spi.ErrReadOnly
	}
	var n int64
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE join_requests SET state = ? WHERE state = ? AND expires_at < ?`,
			string(spi.JoinExpired), string(spi.JoinPending), fmtTime(now.UTC()))
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}
