// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type eventStore struct{ db *DB }

const eventColumns = `seq, event_id, ts, tenant_id, category, event, actor, actor_type,
	acting_for, action, target, result, source_ip, node_id, request_id, trace_id,
	leader_epoch, detail_json, prev_hash, hash`

func scanEvent(sc interface{ Scan(...any) error }) (*spi.Event, error) {
	var (
		e      spi.Event
		ts     string
		detail string
		tenant string
	)
	err := sc.Scan(&e.Seq, &e.EventID, &ts, &tenant, &e.Category, &e.Event, &e.Actor,
		&e.ActorType, &e.ActingFor, &e.Action, &e.Target, &e.Result, &e.SourceIP,
		&e.NodeID, &e.RequestID, &e.TraceID, &e.LeaderEpoch, &detail, &e.PrevHash, &e.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.TS, _ = parseTime(ts)
	e.Detail = []byte(detail)
	return &e, nil
}







func chainHash(e *spi.Event, prevHash string) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0x1f})
		}
	}
	write(
		prevHash,
		e.EventID,
		fmtTime(e.TS),
		string(e.Category),
		e.Event,
		e.Actor,
		e.ActorType,
		e.ActingFor,
		e.Action,
		e.Target,
		e.Result,
		e.SourceIP,
		e.NodeID,
		e.RequestID,
		e.TraceID,
		fmt.Sprint(e.LeaderEpoch),
		string(e.Detail),
	)
	return hex.EncodeToString(h.Sum(nil))
}








func (s *eventStore) Append(ctx context.Context, e *spi.Event) (*spi.Event, error) {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	if e.Category == "" {
		e.Category = spi.CategorySystem
	}
	if e.LeaderEpoch == 0 {
		e.LeaderEpoch = 1
	}
	if len(e.Detail) == 0 {
		e.Detail = []byte("{}")
	}

	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		var prevHash string
		err := tx.QueryRowContext(ctx,
			`SELECT hash FROM events ORDER BY seq DESC LIMIT 1`).Scan(&prevHash)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		e.PrevHash = prevHash
		e.Hash = chainHash(e, prevHash)

		res, err := tx.ExecContext(ctx, `
			INSERT INTO events (event_id, ts, tenant_id, category, event, actor, actor_type,
				acting_for, action, target, result, source_ip, node_id, request_id, trace_id,
				leader_epoch, detail_json, prev_hash, hash)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			e.EventID, fmtTime(e.TS), spi.DefaultTenant, e.Category, e.Event, e.Actor,
			e.ActorType, e.ActingFor, e.Action, e.Target, e.Result, e.SourceIP,
			e.NodeID, e.RequestID, e.TraceID, e.LeaderEpoch, string(e.Detail),
			e.PrevHash, e.Hash)
		if err != nil {
			return err
		}
		seq, err := res.LastInsertId()
		if err != nil {
			return err
		}
		e.Seq = seq
		return nil
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}

func (s *eventStore) List(ctx context.Context, f spi.EventFilter) ([]spi.Event, error) {
	where := []string{"seq > ?"}
	args := []any{f.SinceSeq}
	if f.Category != "" {
		where = append(where, "category = ?")
		args = append(args, string(f.Category))
	}
	if f.Event != "" {
		where = append(where, "event = ?")
		args = append(args, f.Event)
	}
	if f.NodeID != "" {
		where = append(where, "node_id = ?")
		args = append(args, f.NodeID)
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	args = append(args, limit)

	rows, err := s.db.query(ctx,
		`SELECT `+eventColumns+` FROM events WHERE `+strings.Join(where, " AND ")+
			` ORDER BY seq LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []spi.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func (s *eventStore) Last(ctx context.Context) (*spi.Event, error) {
	return scanEvent(s.db.queryRow(ctx, `SELECT ` + eventColumns + ` FROM events ORDER BY seq DESC LIMIT 1`))
}









func (s *eventStore) VerifyChain(ctx context.Context, fromSeq int64) (int64, error) {
	rows, err := s.db.query(ctx,
		`SELECT `+eventColumns+` FROM events WHERE seq >= ? ORDER BY seq`, fromSeq)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var prevHash string
	first := true
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return 0, err
		}
		if first {



			prevHash = e.PrevHash
			first = false
		}
		if e.PrevHash != prevHash {
			return e.Seq, nil
		}
		if chainHash(e, prevHash) != e.Hash {
			return e.Seq, nil
		}
		prevHash = e.Hash
	}
	return 0, rows.Err()
}






func (s *eventStore) PruneBefore(ctx context.Context, unixSec int64) (int64, error) {
	var deleted int64
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`DELETE FROM events WHERE ts < ?`, fmtTime(time.Unix(unixSec, 0)))
		if err != nil {
			return err
		}
		deleted, _ = res.RowsAffected()
		return nil
	})
	return deleted, err
}

type snapshotStore struct{ db *DB }

func (s *snapshotStore) Record(ctx context.Context, snap *spi.Snapshot) error {
	if snap.TakenAt.IsZero() {
		snap.TakenAt = time.Now()
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO replication_snapshots (id, taken_at, size_bytes, sha256, applied_at)
			 VALUES (?,?,?,?,?)`,
			snap.ID, fmtTime(snap.TakenAt), snap.SizeBytes, snap.SHA256, timePtr(snap.AppliedAt))
		return err
	})
}

func (s *snapshotStore) Latest(ctx context.Context) (*spi.Snapshot, error) {
	var (
		snap    spi.Snapshot
		takenAt string
		applied sql.NullString
	)
	err := s.db.queryRow(ctx,
		`SELECT id, taken_at, size_bytes, sha256, applied_at
		   FROM replication_snapshots ORDER BY taken_at DESC LIMIT 1`).
		Scan(&snap.ID, &takenAt, &snap.SizeBytes, &snap.SHA256, &applied)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	snap.TakenAt, _ = parseTime(takenAt)
	snap.AppliedAt = nullTime(applied)
	return &snap, nil
}
