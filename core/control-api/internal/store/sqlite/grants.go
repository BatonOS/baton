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

type grantStore struct{ db *DB }

const grantColumns = `g.tenant_id, g.grant_id, g.grantor, g.grantee, g.action, g.object,
	g.scope, g.effect, g.constraints, g.valid_from, g.valid_until, g.proof, g.created_at,
	r.revoked_at, r.revoked_by`



const grantFrom = ` FROM grants g
	LEFT JOIN grant_revocations r ON r.tenant_id = g.tenant_id AND r.grant_id = g.grant_id`

func scanGrant(row interface{ Scan(...any) error }) (*spi.Grant, error) {
	var g spi.Grant
	var from, until, created string
	var revokedAt, revokedBy sql.NullString
	err := row.Scan(&g.TenantID, &g.GrantID, &g.Grantor, &g.Grantee, &g.Action, &g.Object,
		&g.Scope, &g.Effect, &g.Constraints, &from, &until, &g.Proof, &created,
		&revokedAt, &revokedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.ValidFrom, _ = parseTime(from)
	if until != "" {
		g.ValidUntil, _ = parseTime(until)
	}
	g.CreatedAt, _ = parseTime(created)
	if revokedAt.Valid {
		t, _ := parseTime(revokedAt.String)
		g.RevokedAt = &t
		g.RevokedBy = revokedBy.String
	}
	return &g, nil
}

func (s *grantStore) Issue(ctx context.Context, in spi.Grant) (*spi.Grant, error) {
	if s.db.ReadOnly() {
		return nil, spi.ErrReadOnly
	}



	switch in.Effect {
	case "":
		in.Effect = spi.GrantAllow
	case spi.GrantAllow, spi.GrantDeny:
	default:
		return nil, errors.New("store: grant effect must be allow or deny, explicitly")
	}
	if in.GrantID == "" {
		in.GrantID = "grt_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	}



	if in.Proof == nil {
		in.Proof = []byte{}
	}
	now := time.Now().UTC()
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	if in.ValidFrom.IsZero() {
		in.ValidFrom = now
	}
	until := ""
	if !in.ValidUntil.IsZero() {
		until = fmtTime(in.ValidUntil)
	}
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO grants (tenant_id, grant_id, grantor, grantee, action, object,
			                    scope, effect, constraints, valid_from, valid_until, proof, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.TenantID, in.GrantID, in.Grantor, in.Grantee, in.Action, in.Object,
			in.Scope, in.Effect, in.Constraints, fmtTime(in.ValidFrom), until, in.Proof, fmtTime(in.CreatedAt))
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, in.TenantID, in.GrantID)
}

func (s *grantStore) Get(ctx context.Context, tenantID, grantID string) (*spi.Grant, error) {
	return scanGrant(s.db.queryRow(ctx,
		`SELECT `+grantColumns+grantFrom+` WHERE g.tenant_id = ? AND g.grant_id = ?`,
		tenantID, grantID))
}

func (s *grantStore) List(ctx context.Context, tenantID, grantee, action string) ([]spi.Grant, error) {
	q := `SELECT ` + grantColumns + grantFrom + ` WHERE g.tenant_id = ?`
	args := []any{tenantID}
	if grantee != "" {
		q += ` AND g.grantee = ?`
		args = append(args, grantee)
	}
	if action != "" {
		q += ` AND g.action = ?`
		args = append(args, action)
	}
	q += ` ORDER BY g.created_at DESC, g.grant_id`
	rows, err := s.db.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []spi.Grant{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}




func (s *grantStore) Revoke(ctx context.Context, tenantID, grantID, revokedBy string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	if _, err := s.Get(ctx, tenantID, grantID); err != nil {
		return err
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO grant_revocations (tenant_id, grant_id, revoked_by, revoked_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (tenant_id, grant_id) DO NOTHING`,
			tenantID, grantID, revokedBy, fmtTime(time.Now().UTC()))
		return err
	})
}
