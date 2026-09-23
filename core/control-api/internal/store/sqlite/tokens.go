// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type tokenStore struct{ db *DB }

const tokenColumns = `token_id, tenant_id, token_hash, roles, name_pattern,
	max_uses, used_count, expires_at, revoked_at, created_by, created_at,
	invitee, from_network, from_network_key, approved_at, bound_key_fingerprint,
	purpose`

func scanToken(sc interface{ Scan(...any) error }) (*spi.EnrollmentToken, error) {
	var (
		t         spi.EnrollmentToken
		expires   string
		created   string
		revokedAt sql.NullString
		rolesJSON string
	)
	var approvedAt string
	err := sc.Scan(&t.TokenID, &t.TenantID, &t.TokenHash, &rolesJSON, &t.NamePattern,
		&t.MaxUses, &t.UsedCount, &expires, &revokedAt, &t.CreatedBy, &created,
		&t.Invitee, &t.FromNetwork, &t.FromNetworkKey, &approvedAt, &t.BoundKeyFingerprint,
		&t.Purpose)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := decodeRoles(rolesJSON, &t.Roles); err != nil {
		return nil, err
	}
	t.ExpiresAt, _ = parseTime(expires)
	t.CreatedAt, _ = parseTime(created)
	t.RevokedAt = nullTime(revokedAt)
	t.ApprovedAt, _ = parseTime(approvedAt)
	return &t, nil
}

func (s *tokenStore) Create(ctx context.Context, t *spi.EnrollmentToken) error {
	if t.TenantID == "" {
		t.TenantID = spi.DefaultTenant
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}




	if t.Purpose != spi.TokenPurposeNode && t.Purpose != spi.TokenPurposeOperator {
		return fmt.Errorf("%w: enrollment token purpose must be %q or %q, got %q",
			spi.ErrInvalid, spi.TokenPurposeNode, spi.TokenPurposeOperator, t.Purpose)
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO enrollment_tokens (`+tokenColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			t.TokenID, t.TenantID, t.TokenHash, encodeRoles(t.Roles), t.NamePattern,
			t.MaxUses, t.UsedCount, fmtTime(t.ExpiresAt), timePtr(t.RevokedAt),
			t.CreatedBy, fmtTime(t.CreatedAt), t.Invitee, t.FromNetwork, t.FromNetworkKey, "", t.BoundKeyFingerprint,
			t.Purpose)
		return err
	})
}

func (s *tokenStore) GetByHash(ctx context.Context, hash string) (*spi.EnrollmentToken, error) {
	return scanToken(s.db.queryRow(ctx,
		`SELECT `+tokenColumns+` FROM enrollment_tokens WHERE token_hash = ?`, hash))
}

func (s *tokenStore) List(ctx context.Context, tenantID string) ([]spi.EnrollmentToken, error) {
	rows, err := s.db.query(ctx,
		`SELECT `+tokenColumns+` FROM enrollment_tokens WHERE tenant_id = ? ORDER BY created_at DESC`,
		orDefault(tenantID, spi.DefaultTenant))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []spi.EnrollmentToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}











func (s *tokenStore) Consume(ctx context.Context, hash, purpose string) (*spi.EnrollmentToken, error) {
	var out *spi.EnrollmentToken

	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		now := fmtTime(time.Now())
		res, err := tx.ExecContext(ctx, `
			UPDATE enrollment_tokens
			   SET used_count = used_count + 1
			 WHERE token_hash = ?
			   AND revoked_at IS NULL
			   AND used_count < max_uses
			   AND expires_at > ?
			   AND purpose = ?`, hash, now, purpose)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			out, err = scanToken(tx.QueryRowContext(ctx,
				`SELECT `+tokenColumns+` FROM enrollment_tokens WHERE token_hash = ?`, hash))
			return err
		}

		existing, err := scanToken(tx.QueryRowContext(ctx,
			`SELECT `+tokenColumns+` FROM enrollment_tokens WHERE token_hash = ?`, hash))
		if errors.Is(err, spi.ErrNotFound) {
			return fmt.Errorf("%w: no such enrollment token", spi.ErrNotFound)
		}
		if err != nil {
			return err
		}
		switch {





		case existing.Purpose != purpose:
			return fmt.Errorf("%w: token was not issued for this purpose", spi.ErrConflict)
		case existing.RevokedAt != nil:
			return fmt.Errorf("%w: token was revoked", spi.ErrConflict)
		case existing.UsedCount >= existing.MaxUses:
			return fmt.Errorf("%w: token already used %d/%d times",
				spi.ErrConflict, existing.UsedCount, existing.MaxUses)
		default:
			return fmt.Errorf("%w: token expired at %s",
				spi.ErrConflict, existing.ExpiresAt.Format(time.RFC3339))
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *tokenStore) Revoke(ctx context.Context, tokenID string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE enrollment_tokens SET revoked_at = ? WHERE token_id = ? AND revoked_at IS NULL`,
			fmtTime(time.Now()), tokenID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}








func (s *tokenStore) ReleaseMember(ctx context.Context, tenantID, agentName, toNetwork string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO released_members (tenant_id, agent_name, to_network, released_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(tenant_id, agent_name, to_network) DO UPDATE SET
			  released_at = excluded.released_at`,
			tenantID, agentName, toNetwork, fmtTime(time.Now().UTC()))
		return err
	})
}
