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

type lockStore struct{ db *DB }

const lockColumns = `node_id, holder, session_id, acquired_at, expires_at, preempted_from`








const MinLockTTL = time.Second

func checkTTL(ttl time.Duration) error {
	if ttl < MinLockTTL {
		return fmt.Errorf("sqlite: a takeover lease of %v is shorter than the %v "+
			"the timestamp format can represent, and would be expired on arrival", ttl, MinLockTTL)
	}
	return nil
}

func scanLock(sc interface{ Scan(...any) error }) (*spi.TakeoverLock, error) {
	var (
		l          spi.TakeoverLock
		acquiredAt string
		expiresAt  string
	)
	err := sc.Scan(&l.NodeID, &l.Holder, &l.SessionID, &acquiredAt, &expiresAt, &l.PreemptedFrom)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.AcquiredAt, _ = parseTime(acquiredAt)
	l.ExpiresAt, _ = parseTime(expiresAt)
	return &l, nil
}











func (s *lockStore) Acquire(
	ctx context.Context,
	nodeID, holder, sessionID string,
	ttl time.Duration,
	preempt bool,
) (*spi.TakeoverLock, error) {
	if holder == "" || sessionID == "" {
		return nil, errors.New("sqlite: a takeover lock needs a holder and a session id")
	}
	if err := checkTTL(ttl); err != nil {
		return nil, err
	}

	var out *spi.TakeoverLock
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		nowStr := fmtTime(now)
		expires := fmtTime(now.Add(ttl))




		existing, existsErr := scanLock(tx.QueryRowContext(ctx,
			`SELECT `+lockColumns+` FROM takeover_locks WHERE node_id = ?`, nodeID))
		if existsErr != nil && !errors.Is(existsErr, spi.ErrNotFound) {
			return existsErr
		}

		preemptedFrom := ""
		if preempt && existing != nil && existing.Held(now) && existing.SessionID != sessionID {
			preemptedFrom = existing.Holder
		}








		res, err := tx.ExecContext(ctx, `
			INSERT INTO takeover_locks (`+lockColumns+`)
			VALUES (?,?,?,?,?,?)
			ON CONFLICT(node_id) DO UPDATE SET
			    holder         = excluded.holder,
			    session_id     = excluded.session_id,
			    acquired_at    = CASE WHEN takeover_locks.session_id = excluded.session_id
			                          THEN takeover_locks.acquired_at ELSE excluded.acquired_at END,
			    expires_at     = excluded.expires_at,
			    preempted_from = excluded.preempted_from
			WHERE takeover_locks.expires_at <= ?
			   OR takeover_locks.session_id = excluded.session_id
			   OR ? = 1`,
			nodeID, holder, sessionID, nowStr, expires, preemptedFrom,
			nowStr, boolInt(preempt))
		if err != nil {
			return err
		}

		if n, _ := res.RowsAffected(); n == 1 {
			out, err = scanLock(tx.QueryRowContext(ctx,
				`SELECT `+lockColumns+` FROM takeover_locks WHERE node_id = ?`, nodeID))
			return err
		}


		if existing == nil {
			return fmt.Errorf("%w: could not take control of this node", spi.ErrConflict)
		}
		return fmt.Errorf("%w: %s has been in control since %s (expires %s)",
			spi.ErrConflict, existing.Holder,
			existing.AcquiredAt.Format(time.RFC3339),
			existing.ExpiresAt.Format(time.RFC3339))
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}






func (s *lockStore) Renew(ctx context.Context, nodeID, sessionID string, ttl time.Duration) (*spi.TakeoverLock, error) {
	if err := checkTTL(ttl); err != nil {
		return nil, err
	}
	var out *spi.TakeoverLock
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		now := time.Now()
		res, err := tx.ExecContext(ctx, `
			UPDATE takeover_locks
			   SET expires_at = ?
			 WHERE node_id = ? AND session_id = ? AND expires_at > ?`,
			fmtTime(now.Add(ttl)), nodeID, sessionID, fmtTime(now))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return fmt.Errorf("%w: this takeover session no longer holds the node", spi.ErrConflict)
		}
		out, err = scanLock(tx.QueryRowContext(ctx,
			`SELECT `+lockColumns+` FROM takeover_locks WHERE node_id = ?`, nodeID))
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}





func (s *lockStore) Release(ctx context.Context, nodeID, sessionID string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM takeover_locks WHERE node_id = ? AND session_id = ?`, nodeID, sessionID)
		return err
	})
}






func (s *lockStore) Get(ctx context.Context, nodeID string) (*spi.TakeoverLock, error) {
	return scanLock(s.db.queryRow(ctx,
		`SELECT `+lockColumns+` FROM takeover_locks WHERE node_id = ?`, nodeID))
}
