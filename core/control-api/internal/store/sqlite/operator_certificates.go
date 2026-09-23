// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)






type operatorCertStore struct{ db *DB }

const operatorCertColumns = `serial, tenant_id, operator_name, fingerprint_sha256,
	subject_cn, san_uri, not_before, not_after, issued_at, issued_by, from_ip,
	revoked_at, revoke_reason`

func scanOperatorCert(sc interface{ Scan(...any) error }) (*spi.OperatorCertificate, error) {
	var (
		c         spi.OperatorCertificate
		notBefore string
		notAfter  string
		issuedAt  string
		revokedAt sql.NullString
	)
	err := sc.Scan(&c.Serial, &c.TenantID, &c.OperatorName, &c.Fingerprint,
		&c.SubjectCN, &c.SANURI, &notBefore, &notAfter, &issuedAt,
		&c.IssuedBy, &c.FromIP, &revokedAt, &c.RevokeReason)
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

func (s *operatorCertStore) Issue(ctx context.Context, c *spi.OperatorCertificate) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	if c.IssuedAt.IsZero() {
		c.IssuedAt = time.Now()
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO operator_certificates (`+operatorCertColumns+`)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			c.Serial, c.TenantID, c.OperatorName, c.Fingerprint,
			c.SubjectCN, c.SANURI, fmtTime(c.NotBefore), fmtTime(c.NotAfter),
			fmtTime(c.IssuedAt), c.IssuedBy, c.FromIP,
			timePtr(c.RevokedAt), c.RevokeReason)
		return err
	})
}

func (s *operatorCertStore) Get(ctx context.Context, serial string) (*spi.OperatorCertificate, error) {
	return scanOperatorCert(s.db.queryRow(ctx,
		`SELECT `+operatorCertColumns+` FROM operator_certificates WHERE serial = ?`, serial))
}








func (s *operatorCertStore) ListByName(ctx context.Context, name string) ([]spi.OperatorCertificate, error) {
	return s.list(ctx, `WHERE operator_name = ?`, name)
}

func (s *operatorCertStore) List(ctx context.Context, tenantID string) ([]spi.OperatorCertificate, error) {
	return s.list(ctx, `WHERE tenant_id = ?`, tenantID)
}

func (s *operatorCertStore) list(ctx context.Context, where string, args ...any) ([]spi.OperatorCertificate, error) {
	rows, err := s.db.query(ctx,
		`SELECT `+operatorCertColumns+` FROM operator_certificates `+where+
			` ORDER BY issued_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()



	out := []spi.OperatorCertificate{}
	for rows.Next() {
		c, err := scanOperatorCert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}















func (s *operatorCertStore) Revoke(ctx context.Context, serial, reason string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE operator_certificates
			SET revoked_at = ?, revoke_reason = ?
			WHERE serial = ? AND revoked_at IS NULL`,
			fmtTime(time.Now()), reason, serial)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {

			var one int
			err := tx.QueryRowContext(ctx,
				`SELECT 1 FROM operator_certificates WHERE serial = ?`, serial).Scan(&one)
			if errors.Is(err, sql.ErrNoRows) {
				return spi.ErrNotFound
			}
			return err
		}
		return nil
	})
}
