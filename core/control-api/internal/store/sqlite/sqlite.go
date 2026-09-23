// SPDX-License-Identifier: Apache-2.0












package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
	_ "modernc.org/sqlite"
)




//go:embed migrations/*.sql
var Migrations embed.FS


const timeFormat = "2006-01-02T15:04:05Z"

func fmtTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeFormat, s)
}


func nullTime(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := parseTime(ns.String)
	if err != nil {
		return nil
	}
	return &t
}

func timePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}






type writeReq struct {
	fn   func(*sql.Tx) error
	raw  func(*sql.DB) error
	done chan error
}


type DB struct {
	path string







	handles sync.RWMutex


	read *sql.DB

	write *sql.DB

	dsnFor func(mode string) string

	writes   chan writeReq
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup





	writable *atomic.Bool




	tx *sql.Tx
}


type Options struct {

	Path string


	ReadOnly bool
}


func Open(ctx context.Context, opts Options) (*DB, error) {
	if opts.Path == "" {
		return nil, errors.New("sqlite: path required")
	}

	memory := opts.Path == ":memory:" || strings.HasPrefix(opts.Path, "file::memory:")




	dsn := func(mode string) string {
		if memory {


			return "file:batonmem?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
		}
		q := []string{
			"_pragma=busy_timeout(5000)",
			"_pragma=foreign_keys(1)",
			"_pragma=journal_mode(WAL)",
			"_pragma=synchronous(NORMAL)",
			"_pragma=cache_size(-16000)",
		}
		if mode != "" {
			q = append(q, "mode="+mode)
		}
		return "file:" + opts.Path + "?" + strings.Join(q, "&")
	}

	readDSN := dsn("ro")
	if memory || opts.ReadOnly {




		readDSN = dsn("")
	}

	read, err := sql.Open("sqlite", readDSN)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open read pool: %w", err)
	}
	read.SetMaxOpenConns(4)

	write, err := sql.Open("sqlite", dsn(""))
	if err != nil {
		read.Close()
		return nil, fmt.Errorf("sqlite: open write conn: %w", err)
	}

	write.SetMaxOpenConns(1)
	write.SetConnMaxLifetime(0)

	if err := write.PingContext(ctx); err != nil {
		read.Close()
		write.Close()
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}

	db := &DB{
		path:     opts.Path,
		read:     read,
		write:    write,
		dsnFor:   dsn,
		writes:   make(chan writeReq),
		stop:     make(chan struct{}),
		writable: new(atomic.Bool),
	}
	db.writable.Store(!opts.ReadOnly)

	db.wg.Add(1)
	go db.writer()

	return db, nil
}

func (d *DB) readHandle() *sql.DB {
	d.handles.RLock()
	defer d.handles.RUnlock()
	return d.read
}

func (d *DB) writeHandle() *sql.DB {
	d.handles.RLock()
	defer d.handles.RUnlock()
	return d.write
}






func (d *DB) Reopen(ctx context.Context) error {
	newRead, err := sql.Open("sqlite", d.dsnFor(""))
	if err != nil {
		return fmt.Errorf("sqlite: reopen read pool: %w", err)
	}
	newRead.SetMaxOpenConns(4)

	newWrite, err := sql.Open("sqlite", d.dsnFor(""))
	if err != nil {
		newRead.Close()
		return fmt.Errorf("sqlite: reopen write connection: %w", err)
	}
	newWrite.SetMaxOpenConns(1)
	newWrite.SetConnMaxLifetime(0)

	if err := newWrite.PingContext(ctx); err != nil {
		newRead.Close()
		newWrite.Close()
		return fmt.Errorf("sqlite: ping after reopen: %w", err)
	}

	d.handles.Lock()
	oldRead, oldWrite := d.read, d.write
	d.read, d.write = newRead, newWrite
	d.handles.Unlock()


	_ = oldRead.Close()
	_ = oldWrite.Close()
	return nil
}



func (d *DB) writer() {
	defer d.wg.Done()
	for {
		select {
		case <-d.stop:
			return
		case req := <-d.writes:
			if req.raw != nil {
				req.done <- req.raw(d.writeHandle())
				continue
			}
			req.done <- d.runTx(req.fn)
		}
	}
}

func (d *DB) runTx(fn func(*sql.Tx) error) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()



	tx, err := d.writeHandle().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit: %w", err)
	}
	return nil
}






func (d *DB) exec(ctx context.Context, fn func(*sql.Tx) error) error {
	if !d.writable.Load() {
		return spi.ErrReadOnly
	}
	return d.execPrivileged(ctx, fn)
}







func (d *DB) execPrivileged(ctx context.Context, fn func(*sql.Tx) error) error {
	if d.tx != nil {
		return fn(d.tx)
	}
	done := make(chan error, 1)
	select {
	case d.writes <- writeReq{fn: fn, done: done}:
	case <-ctx.Done():
		return ctx.Err()
	case <-d.stop:
		return errors.New("sqlite: store is closing")
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():


		return ctx.Err()
	}
}




func (d *DB) execRaw(ctx context.Context, fn func(*sql.DB) error) error {
	if d.tx != nil {
		return errors.New("sqlite: this statement cannot run inside a transaction")
	}
	done := make(chan error, 1)
	select {
	case d.writes <- writeReq{raw: fn, done: done}:
	case <-ctx.Done():
		return ctx.Err()
	case <-d.stop:
		return errors.New("sqlite: store is closing")
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}



func (d *DB) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if d.tx != nil {
		return d.tx.QueryContext(ctx, q, args...)
	}
	return d.readHandle().QueryContext(ctx, q, args...)
}

func (d *DB) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	if d.tx != nil {
		return d.tx.QueryRowContext(ctx, q, args...)
	}
	return d.readHandle().QueryRowContext(ctx, q, args...)
}


func (d *DB) WithTx(ctx context.Context, fn func(spi.Store) error) error {
	if d.tx != nil {


		return fn(d)
	}
	return d.exec(ctx, func(tx *sql.Tx) error {
		view := &DB{read: d.read, write: d.write, tx: tx, writable: d.writable}
		return fn(view)
	})
}






const migrationKeysOffMarker = "-- baton:foreign_keys=off"





















func (d *DB) runKeysOff(ctx context.Context, name string, body string, version int) error {
	if _, err := d.write.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("sqlite: %s: disable foreign keys: %w", name, err)
	}



	defer func() {
		_, _ = d.write.ExecContext(ctx, "PRAGMA foreign_keys = ON")
	}()

	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: %s: begin: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, body); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("sqlite: apply %s: %w", name, err)
	}

	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("sqlite: %s: foreign_key_check: %w", name, err)
	}



	var broken []string
	for rows.Next() {
		var table, parent sql.NullString
		var rowid, fkid sql.NullInt64
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			rows.Close()
			_ = tx.Rollback()
			return fmt.Errorf("sqlite: %s: reading foreign_key_check: %w", name, err)
		}
		if len(broken) < 5 {
			broken = append(broken, fmt.Sprintf("%s → %s", table.String, parent.String))
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		_ = tx.Rollback()
		return fmt.Errorf("sqlite: %s: reading foreign_key_check: %w", name, err)
	}
	rows.Close()
	if len(broken) > 0 {
		_ = tx.Rollback()
		return fmt.Errorf(
			"sqlite: %s left rows pointing at nothing (%s); rolled back — "+
				"the migration ran with foreign keys off and did not put them back consistent",
			name, strings.Join(broken, ", "))
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		version, fmtTime(time.Now())); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("sqlite: %s: record version: %w", name, err)
	}
	return tx.Commit()
}





func (d *DB) Migrate(ctx context.Context, migrations fs.FS) error {
	entries, err := fs.Glob(migrations, "*.sql")
	if err != nil {
		return fmt.Errorf("sqlite: glob migrations: %w", err)
	}
	if len(entries) == 0 {
		entries, err = fs.Glob(migrations, "migrations/*.sql")
		if err != nil {
			return fmt.Errorf("sqlite: glob migrations: %w", err)
		}
	}
	sort.Strings(entries)

	for _, name := range entries {
		version, err := migrationVersion(name)
		if err != nil {
			return err
		}
		applied, err := d.migrationApplied(ctx, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := fs.ReadFile(migrations, name)
		if err != nil {
			return fmt.Errorf("sqlite: read %s: %w", name, err)
		}



		if strings.Contains(string(body), migrationKeysOffMarker) {
			if err := d.runKeysOff(ctx, name, string(body), version); err != nil {
				return err
			}
			continue
		}


		if err := d.execPrivileged(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("sqlite: apply %s: %w", name, err)
			}
			_, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
				version, fmtTime(time.Now()))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func migrationVersion(name string) (int, error) {
	base := filepath.Base(name)
	idx := strings.Index(base, "_")
	if idx <= 0 {
		return 0, fmt.Errorf("sqlite: migration %q must be named <version>_<description>.sql", base)
	}
	v, err := strconv.Atoi(base[:idx])
	if err != nil {
		return 0, fmt.Errorf("sqlite: migration %q has a non-numeric version: %w", base, err)
	}
	return v, nil
}

func (d *DB) migrationApplied(ctx context.Context, version int) (bool, error) {
	var n int
	err := d.queryRow(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&n)
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	err = d.queryRow(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, version).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}


func (d *DB) Driver() string { return "sqlite" }


func (d *DB) ReadOnly() bool { return !d.writable.Load() }


func (d *DB) Close() error {
	if d.tx != nil {
		return errors.New("sqlite: cannot close a transactional view")
	}
	d.stopOnce.Do(func() { close(d.stop) })
	d.wg.Wait()
	var errs []error
	if err := d.writeHandle().Close(); err != nil {
		errs = append(errs, err)
	}
	if err := d.readHandle().Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}




func (d *DB) Nodes() spi.NodeStore              { return &nodeStore{d} }
func (d *DB) Tokens() spi.TokenStore            { return &tokenStore{d} }
func (d *DB) Certs() spi.CertStore              { return &certStore{d} }
func (d *DB) Capabilities() spi.CapabilityStore { return &capabilityStore{d} }
func (d *DB) OperatorCertificates() spi.OperatorCertificateStore {
	return &operatorCertStore{d}
}
func (d *DB) Calls() spi.CallStore              { return &callStore{d} }
func (d *DB) Events() spi.EventStore            { return &eventStore{d} }
func (d *DB) Snapshots() spi.SnapshotStore      { return &snapshotStore{d} }
func (d *DB) Locks() spi.LockStore              { return &lockStore{d} }
func (d *DB) Identities() spi.IdentityStore     { return &identityStore{d} }
func (d *DB) Messages() spi.MessageStore        { return &messageStore{d} }
func (d *DB) Networks() spi.NetworkStore        { return &networkStore{d} }
func (d *DB) Integrations() spi.IntegrationStore { return &integrationStore{d} }
func (d *DB) Skills() spi.SkillStore             { return &skillStore{d} }
func (d *DB) Resources() spi.ResourceStore       { return &resourceStore{d} }
func (d *DB) AccessNonces() spi.AccessNonceStore     { return &accessNonceStore{d} }
func (d *DB) ReceivedGrants() spi.ReceivedGrantStore { return &receivedGrantStore{d} }
func (d *DB) JoinRequests() spi.JoinRequestStore { return &joinRequestStore{d} }
func (d *DB) Grants() spi.GrantStore             { return &grantStore{d} }
func (d *DB) Transactions() spi.TransactionStore { return &transactionStore{d} }
func (d *DB) Interactions() spi.InteractionStore { return &interactionStore{d} }


func (d *DB) ClusterValue(ctx context.Context, key string) (string, error) {
	var v string
	err := d.queryRow(ctx, `SELECT v FROM cluster_state WHERE k = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", spi.ErrNotFound
	}
	return v, err
}


func (d *DB) SetClusterValue(ctx context.Context, key, value string) error {
	return d.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO cluster_state (k, v, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT(k) DO UPDATE SET v = excluded.v, updated_at = excluded.updated_at`,
			key, value, fmtTime(time.Now()))
		return err
	})
}


func (d *DB) LeaderEpoch(ctx context.Context) (int64, error) {
	v, err := d.ClusterValue(ctx, "leader_epoch")
	if errors.Is(err, spi.ErrNotFound) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	n, perr := strconv.ParseInt(v, 10, 64)
	if perr != nil || n < 1 {
		return 1, nil
	}
	return n, nil
}








func (d *DB) Promote(ctx context.Context) (int64, error) {
	if d.writable.Load() {

		return d.LeaderEpoch(ctx)
	}
	var epoch int64
	err := d.execPrivileged(ctx, func(tx *sql.Tx) error {
		var cur string
		row := tx.QueryRowContext(ctx, `SELECT v FROM cluster_state WHERE k = 'leader_epoch'`)
		if serr := row.Scan(&cur); serr != nil && !errors.Is(serr, sql.ErrNoRows) {
			return serr
		}
		n, _ := strconv.ParseInt(cur, 10, 64)
		if n < 1 {
			n = 1
		}
		epoch = n + 1
		now := fmtTime(time.Now())
		if _, e := tx.ExecContext(ctx,
			`INSERT INTO cluster_state (k, v, updated_at) VALUES ('leader_epoch', ?, ?)
			 ON CONFLICT(k) DO UPDATE SET v = excluded.v, updated_at = excluded.updated_at`,
			strconv.FormatInt(epoch, 10), now); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx,
			`INSERT INTO cluster_state (k, v, updated_at) VALUES ('mode', 'primary', ?)
			 ON CONFLICT(k) DO UPDATE SET v = excluded.v, updated_at = excluded.updated_at`, now)
		return e
	})
	if err != nil {
		return 0, err
	}
	d.writable.Store(true)
	return epoch, nil
}




func (d *DB) SetWritable(w bool) { d.writable.Store(w) }







func (d *DB) Demote(ctx context.Context) error {
	if !d.writable.Load() {
		return nil
	}
	if err := d.execPrivileged(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx,
			`INSERT INTO cluster_state (k, v, updated_at) VALUES ('mode', 'mirror', ?)
			 ON CONFLICT(k) DO UPDATE SET v = excluded.v, updated_at = excluded.updated_at`, fmtTime(time.Now()))
		return e
	}); err != nil {
		return err
	}
	d.writable.Store(false)
	return nil
}


func (d *DB) Path() string { return d.path }

var _ spi.Store = (*DB)(nil)
