// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type accessNonceStore struct{ db *DB }





func (s *accessNonceStore) Consume(ctx context.Context, tenantID, purpose, subject, nonce string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO access_nonces (tenant_id, purpose, subject, nonce, seen_at) VALUES (?,?,?,?,?)
			 ON CONFLICT(tenant_id, purpose, subject, nonce) DO NOTHING`,
			tenantID, purpose, subject, nonce, fmtTime(time.Now()))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrReplayed
		}
		return nil
	})
}
