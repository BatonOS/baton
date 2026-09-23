// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type certStore struct{ db *DB }

const certColumns = `serial, node_id, fingerprint_sha256, subject_cn, san_uri,
	not_before, not_after, issued_at, revoked_at, revoke_reason, superseded_by`

func scanCert(sc interface{ Scan(...any) error }) (*spi.Certificate, error) {
	var (
		c         spi.Certificate
		notBefore string
		notAfter  string
		issuedAt  string
		revokedAt sql.NullString
	)
	err := sc.Scan(&c.Serial, &c.NodeID, &c.FingerprintSHA256, &c.SubjectCN, &c.SANURI,
		&notBefore, &notAfter, &issuedAt, &revokedAt, &c.RevokeReason, &c.SupersededBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.NotBefore, _ = parseTime(notBefore)
	c.NotAfter, _ = parseTime(notAfter)
	c.IssuedAt, _ = parseTime(issuedAt)
	c.RevokedAt = nullTime(revokedAt)
	return &c, nil
}

func (s *certStore) Issue(ctx context.Context, c *spi.Certificate) error {
	if c.IssuedAt.IsZero() {
		c.IssuedAt = time.Now()
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO certificates (`+certColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			c.Serial, c.NodeID, c.FingerprintSHA256, c.SubjectCN, c.SANURI,
			fmtTime(c.NotBefore), fmtTime(c.NotAfter), fmtTime(c.IssuedAt),
			timePtr(c.RevokedAt), c.RevokeReason, c.SupersededBy)
		return err
	})
}

func (s *certStore) Get(ctx context.Context, serial string) (*spi.Certificate, error) {
	return scanCert(s.db.queryRow(ctx,
		`SELECT `+certColumns+` FROM certificates WHERE serial = ?`, serial))
}

func (s *certStore) ListByNode(ctx context.Context, nodeID string) ([]spi.Certificate, error) {
	rows, err := s.db.query(ctx,
		`SELECT `+certColumns+` FROM certificates WHERE node_id = ? ORDER BY issued_at DESC`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []spi.Certificate
	for rows.Next() {
		c, err := scanCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *certStore) Revoke(ctx context.Context, serial, reason string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE certificates SET revoked_at = ?, revoke_reason = ?
			  WHERE serial = ? AND revoked_at IS NULL`,
			fmtTime(time.Now()), reason, serial)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}

















func (s *certStore) RevokedSerials(ctx context.Context) ([]string, error) {
	rows, err := s.db.query(ctx, `
		SELECT serial FROM certificates          WHERE revoked_at IS NOT NULL
		UNION ALL
		SELECT serial FROM operator_certificates WHERE revoked_at IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var serial string
		if err := rows.Scan(&serial); err != nil {
			return nil, err
		}
		out = append(out, serial)
	}
	return out, rows.Err()
}
