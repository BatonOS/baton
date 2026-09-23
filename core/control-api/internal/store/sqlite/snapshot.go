// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)














func (d *DB) SnapshotTo(path string) (sha256sum string, size int64, err error) {
	if d.tx != nil {
		return "", 0, errors.New("sqlite: cannot snapshot from inside a transaction")
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", 0, fmt.Errorf("sqlite: clear snapshot target: %w", err)
	}





	if err := d.execRaw(context.Background(), func(db *sql.DB) error {
		_, err := db.Exec(`VACUUM INTO ?`, path)
		return err
	}); err != nil {
		return "", 0, fmt.Errorf("sqlite: vacuum into %s: %w", path, err)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}







func (d *DB) RestoreFrom(path string) error {
	if d.writable.Load() {
		return errors.New("sqlite: refusing to restore over a writable primary database")
	}


	if err := replaceFile(path, d.path); err != nil {
		return err
	}




	if err := d.Reopen(context.Background()); err != nil {
		return err
	}







	return d.execPrivileged(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`INSERT INTO cluster_state (k, v, updated_at) VALUES ('mode', 'mirror', ?)
			 ON CONFLICT(k) DO UPDATE SET v = excluded.v, updated_at = excluded.updated_at`,
			fmtTime(time.Now()))
		return err
	})
}

func replaceFile(src, dst string) error {



	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dst + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("sqlite: remove %s%s: %w", dst, suffix, err)
		}
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("sqlite: replace database: %w", err)
	}
	return nil
}
