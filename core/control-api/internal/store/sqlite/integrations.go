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

type integrationStore struct{ db *DB }

const integrationColumns = `integration_id, tenant_id, provider, public_key_pem, fingerprint,
	scopes, target_network, enabled, created_at, confirmed_at`

func scanIntegration(row interface{ Scan(...any) error }) (*spi.Integration, error) {
	var in spi.Integration
	var scopes, created, confirmed string
	var enabled int
	err := row.Scan(&in.IntegrationID, &in.TenantID, &in.Provider, &in.PublicKeyPEM,
		&in.Fingerprint, &scopes, &in.TargetNetwork, &enabled, &created, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	in.Enabled = enabled == 1




	if scopes != "" {
		in.Scopes = strings.Split(scopes, "\n")
	} else {
		in.Scopes = []string{}
	}
	in.CreatedAt, _ = parseTime(created)
	in.ConfirmedAt, _ = parseTime(confirmed)
	return &in, nil
}

func (s *integrationStore) Create(ctx context.Context, in spi.Integration) (*spi.Integration, error) {
	if s.db.ReadOnly() {
		return nil, spi.ErrReadOnly
	}
	if in.IntegrationID == "" {
		in.IntegrationID = "itg_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	}
	now := time.Now().UTC()
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO provider_integrations
			  (integration_id, tenant_id, provider, public_key_pem, fingerprint,
			   scopes, target_network, enabled, created_at, confirmed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '')`,
			in.IntegrationID, in.TenantID, in.Provider, in.PublicKeyPEM, in.Fingerprint,
			strings.Join(in.Scopes, "\n"), in.TargetNetwork, 0, fmtTime(now))
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, in.TenantID, in.IntegrationID)
}

func (s *integrationStore) Get(ctx context.Context, tenantID, integrationID string) (*spi.Integration, error) {
	return scanIntegration(s.db.queryRow(ctx,
		`SELECT `+integrationColumns+` FROM provider_integrations
		  WHERE tenant_id = ? AND integration_id = ?`, tenantID, integrationID))
}







func (s *integrationStore) ByFingerprint(ctx context.Context, tenantID, fingerprint string) (*spi.Integration, error) {
	return scanIntegration(s.db.queryRow(ctx,
		`SELECT `+integrationColumns+` FROM provider_integrations
		  WHERE tenant_id = ? AND fingerprint = ?`, tenantID, fingerprint))
}

func (s *integrationStore) List(ctx context.Context, tenantID string) ([]spi.Integration, error) {
	rows, err := s.db.query(ctx,
		`SELECT `+integrationColumns+` FROM provider_integrations
		  WHERE tenant_id = ? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []spi.Integration{}
	for rows.Next() {
		in, err := scanIntegration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rows.Err()
}

func (s *integrationStore) SetEnabled(ctx context.Context, tenantID, integrationID string, enabled bool) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	v, confirmed := 0, ""
	if enabled {
		v, confirmed = 1, fmtTime(time.Now().UTC())
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE provider_integrations SET enabled = ?, confirmed_at = ?
			 WHERE tenant_id = ? AND integration_id = ?`,
			v, confirmed, tenantID, integrationID)
		if err != nil {
			return err
		}



		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}






func (s *integrationStore) Delete(ctx context.Context, tenantID, integrationID string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM provider_integrations WHERE tenant_id = ? AND integration_id = ?`,
			tenantID, integrationID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}







func (s *integrationStore) ClaimNonce(ctx context.Context, scope, nonce string, ttl time.Duration) (bool, error) {
	if s.db.ReadOnly() {
		return false, spi.ErrReadOnly
	}
	now := time.Now().UTC()
	claimed := false
	err := s.db.exec(ctx, func(tx *sql.Tx) error {


		if _, err := tx.ExecContext(ctx,
			`DELETE FROM replay_nonces WHERE expires_at < ?`, fmtTime(now)); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO replay_nonces (nonce, scope, expires_at) VALUES (?, ?, ?)`,
			nonce, scope, fmtTime(now.Add(ttl)))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		claimed = n == 1
		return nil
	})
	return claimed, err
}
