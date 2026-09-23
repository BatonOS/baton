// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type callStore struct{ db *DB }

const callColumns = `call_id, tenant_id, node_id, capability_name, capability_version,
	idempotency_key, requester, status, input_json, output_json, error_code,
	error_message, created_at, dispatched_at, completed_at, duration_ms, request_id, trace_id`

func scanCall(sc interface{ Scan(...any) error }) (*spi.Call, error) {
	var (
		c            spi.Call
		input        string
		output       sql.NullString
		created      string
		dispatchedAt sql.NullString
		completedAt  sql.NullString
	)
	err := sc.Scan(&c.CallID, &c.TenantID, &c.NodeID, &c.CapabilityName, &c.CapabilityVersion,
		&c.IdempotencyKey, &c.Requester, &c.Status, &input, &output, &c.ErrorCode,
		&c.ErrorMessage, &created, &dispatchedAt, &completedAt, &c.DurationMS,
		&c.RequestID, &c.TraceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Input = []byte(input)
	if output.Valid {
		c.Output = []byte(output.String)
	}
	c.CreatedAt, _ = parseTime(created)
	c.DispatchedAt = nullTime(dispatchedAt)
	c.CompletedAt = nullTime(completedAt)
	return &c, nil
}









func (s *callStore) Create(ctx context.Context, c *spi.Call) (*spi.Call, error) {
	if c.TenantID == "" {
		c.TenantID = spi.DefaultTenant
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	if c.Status == "" {
		c.Status = spi.CallQueued
	}

	var existing *spi.Call
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO calls (`+callColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.CallID, c.TenantID, c.NodeID, c.CapabilityName, c.CapabilityVersion,
			c.IdempotencyKey, c.Requester, c.Status, orEmptyJSON(c.Input), nil,
			c.ErrorCode, c.ErrorMessage, fmtTime(c.CreatedAt), nil, nil,
			c.DurationMS, c.RequestID, c.TraceID)
		if isUniqueViolation(err) {
			existing, err = scanCall(tx.QueryRowContext(ctx,
				`SELECT `+callColumns+` FROM calls WHERE tenant_id = ? AND idempotency_key = ?`,
				c.TenantID, c.IdempotencyKey))
			if err != nil {
				return err
			}
			return spi.ErrConflict
		}
		return err
	})

	if errors.Is(err, spi.ErrConflict) {
		return existing, spi.ErrConflict
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *callStore) Get(ctx context.Context, callID string) (*spi.Call, error) {
	return scanCall(s.db.queryRow(ctx, `SELECT `+callColumns+` FROM calls WHERE call_id = ?`, callID))
}

func (s *callStore) GetByIdempotencyKey(ctx context.Context, tenantID, key string) (*spi.Call, error) {
	return scanCall(s.db.queryRow(ctx,
		`SELECT `+callColumns+` FROM calls WHERE tenant_id = ? AND idempotency_key = ?`,
		orDefault(tenantID, spi.DefaultTenant), key))
}

func (s *callStore) SetStatus(ctx context.Context, callID string, st spi.CallStatus) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if st == spi.CallDispatched {
			res, err = tx.ExecContext(ctx,
				`UPDATE calls SET status = ?, dispatched_at = ? WHERE call_id = ?`,
				st, fmtTime(time.Now()), callID)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE calls SET status = ? WHERE call_id = ?`, st, callID)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}






func (s *callStore) Complete(ctx context.Context, c *spi.Call) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		if c.CompletedAt == nil {
			c.CompletedAt = &now
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE calls
			   SET status = ?, output_json = ?, error_code = ?, error_message = ?,
			       completed_at = ?, duration_ms = ?
			 WHERE call_id = ? AND status IN ('queued','dispatched')`,
			c.Status, nullableJSON(c.Output), c.ErrorCode, c.ErrorMessage,
			fmtTime(*c.CompletedAt), c.DurationMS, c.CallID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrConflict
		}
		if c.Status == spi.CallSucceeded {
			_, err = tx.ExecContext(ctx, `
				UPDATE capabilities SET last_call_at = ?
				 WHERE node_id = ? AND name = ?`,
				fmtTime(*c.CompletedAt), c.NodeID, c.CapabilityName)
		}
		return err
	})
}

func (s *callStore) List(ctx context.Context, tenantID, nodeID string, limit int) ([]spi.Call, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	q := `SELECT ` + callColumns + ` FROM calls WHERE tenant_id = ?`
	args := []any{orDefault(tenantID, spi.DefaultTenant)}
	if nodeID != "" {
		q += ` AND node_id = ?`
		args = append(args, nodeID)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []spi.Call
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
