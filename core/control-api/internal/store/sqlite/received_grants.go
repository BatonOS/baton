// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type receivedGrantStore struct{ db *DB }

const receivedGrantColumns = `tenant_id, grant_id, issuer_network_id, issuer_key, subject_network_id, scope, ` +
	`issued_at, expiry_at, endpoint, peer_resolver, peer_domain, ca_pem, ca_fingerprint, payload, signature, imported_at`

func scanReceivedGrant(sc interface{ Scan(...any) error }) (*spi.ReceivedGrant, error) {
	var (
		g          spi.ReceivedGrant
		issuedAt   string
		expiryAt   string
		importedAt string
	)
	err := sc.Scan(&g.TenantID, &g.GrantID, &g.Issuer, &g.IssuerKey, &g.Subject, &g.Scope,
		&issuedAt, &expiryAt, &g.Endpoint, &g.PeerResolver, &g.PeerDomain, &g.CAPEM, &g.CAFingerprint, &g.Payload, &g.Signature, &importedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.IssuedAt, _ = parseTime(issuedAt)
	g.ExpiryAt, _ = parseTime(expiryAt)
	g.ImportedAt, _ = parseTime(importedAt)
	return &g, nil
}



func (s *receivedGrantStore) Import(ctx context.Context, g *spi.ReceivedGrant) error {
	if g.ImportedAt.IsZero() {
		g.ImportedAt = time.Now()
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO received_grants (`+receivedGrantColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			 ON CONFLICT(tenant_id, grant_id) DO UPDATE SET
			   issuer_network_id = excluded.issuer_network_id,
			   issuer_key        = excluded.issuer_key,
			   subject_network_id= excluded.subject_network_id,
			   scope             = excluded.scope,
			   issued_at         = excluded.issued_at,
			   expiry_at         = excluded.expiry_at,
			   endpoint          = excluded.endpoint,
			   peer_resolver     = excluded.peer_resolver,
			   peer_domain       = excluded.peer_domain,
			   ca_pem            = excluded.ca_pem,
			   ca_fingerprint    = excluded.ca_fingerprint,
			   payload           = excluded.payload,
			   signature         = excluded.signature,
			   imported_at       = excluded.imported_at`,
			g.TenantID, g.GrantID, g.Issuer, g.IssuerKey, g.Subject, g.Scope,
			fmtTime(g.IssuedAt), fmtTime(g.ExpiryAt), g.Endpoint, g.PeerResolver, g.PeerDomain, g.CAPEM, g.CAFingerprint,
			g.Payload, g.Signature, fmtTime(g.ImportedAt))
		return err
	})
}

func (s *receivedGrantStore) Get(ctx context.Context, tenantID, grantID string) (*spi.ReceivedGrant, error) {
	return scanReceivedGrant(s.db.queryRow(ctx,
		`SELECT `+receivedGrantColumns+` FROM received_grants WHERE tenant_id = ? AND grant_id = ?`, tenantID, grantID))
}

func (s *receivedGrantStore) List(ctx context.Context, tenantID string) ([]spi.ReceivedGrant, error) {
	rows, err := s.db.query(ctx,
		`SELECT `+receivedGrantColumns+` FROM received_grants WHERE tenant_id = ? ORDER BY imported_at DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []spi.ReceivedGrant{}
	for rows.Next() {
		g, err := scanReceivedGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

func (s *receivedGrantStore) Delete(ctx context.Context, tenantID, grantID string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM received_grants WHERE tenant_id = ? AND grant_id = ?`, tenantID, grantID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}
