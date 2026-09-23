// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)




type resourceStore struct{ db *DB }

const resourceColumns = `resource_id, tenant_id, network_id, type, name, version, folder, visibility,
	publisher, hash, source, signature, origin, license,
	compatibility, permissions_required, detail, body, created_at, updated_at, tags,
	review, reviewed_by, reviewed_at, review_reason`

func scanResource(sc interface{ Scan(...any) error }) (*spi.Resource, error) {
	var r spi.Resource
	if err := sc.Scan(&r.ResourceID, &r.TenantID, &r.NetworkID, &r.Type, &r.Name, &r.Version, &r.Folder, &r.Visibility,
		&r.Publisher, &r.Hash, &r.Source, &r.Signature, &r.Origin, &r.License,
		&r.Compatibility, &r.PermissionsRequired, &r.Detail, &r.Body, &r.CreatedAt, &r.UpdatedAt, &r.Tags,
		&r.Review, &r.ReviewedBy, &r.ReviewedAt, &r.ReviewReason); err != nil {
		return nil, err
	}
	return &r, nil
}

func newResourceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *resourceStore) Put(ctx context.Context, tenantID string, r spi.Resource) (*spi.Resource, error) {
	if s.db.ReadOnly() {
		return nil, spi.ErrReadOnly
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if r.ResourceID == "" {
		r.ResourceID = newResourceID()
	}
	if r.Visibility == "" {
		r.Visibility = "network"
	}
	if r.Detail == "" {
		r.Detail = "{}"
	}
	if r.Tags == "" {
		r.Tags = "[]"
	}
	if r.CreatedAt == "" {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	err := s.db.exec(ctx, func(tx *sql.Tx) error {



































		var prior string
		switch err := tx.QueryRowContext(ctx,
			`SELECT review FROM resources WHERE tenant_id = ? AND type = ? AND name = ?`,
			tenantID, r.Type, r.Name).Scan(&prior); {
		case errors.Is(err, sql.ErrNoRows):

		case err != nil:
			return err
		case prior == spi.ReviewPending:
			return spi.ErrConflict
		}



		_, err := tx.ExecContext(ctx, `
			INSERT INTO resources (`+resourceColumns+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(tenant_id, type, name) DO UPDATE SET
			    version=excluded.version, folder=excluded.folder, visibility=excluded.visibility,
			    publisher=excluded.publisher, hash=excluded.hash, source=excluded.source,
			    signature=excluded.signature, origin=excluded.origin, license=excluded.license,
			    compatibility=excluded.compatibility, permissions_required=excluded.permissions_required,
			    detail=excluded.detail, body=excluded.body,
			    tags=excluded.tags, updated_at=excluded.updated_at,
			    -- A resubmission is a NEW application: whatever the last decision
			    -- was, it was about bytes that are no longer here. Carrying an
			    -- admitted verdict across a change of body would approve
			    -- something nobody read -- the same substitution the freeze
			    -- above refuses while an application is open.
			    --
			    -- ...but only when the review actually MOVES. The caller decides
			    -- that (it is the one that knows whether the bytes changed); the
			    -- store only makes sure a verdict never outlives the state it
			    -- was reached in.
			    --
			    -- Without the CASE, re-publishing IDENTICAL bytes would reset an
			    -- admitted resource to pending, and it would vanish from every
			    -- member until somebody clicked again -- a way to un-share
			    -- anything by sending it a second time. The freeze's own reason
			    -- decides this one: a verdict is stale because the bytes moved,
			    -- so bytes that did not move leave it standing.
			    review=excluded.review,
			    reviewed_by=CASE WHEN excluded.review = resources.review THEN resources.reviewed_by ELSE '' END,
			    reviewed_at=CASE WHEN excluded.review = resources.review THEN resources.reviewed_at ELSE '' END,
			    review_reason=CASE WHEN excluded.review = resources.review THEN resources.review_reason ELSE '' END`,
			r.ResourceID, tenantID, r.NetworkID, r.Type, r.Name, r.Version, r.Folder, r.Visibility,
			r.Publisher, r.Hash, r.Source, r.Signature, r.Origin, r.License,
			r.Compatibility, r.PermissionsRequired, r.Detail, r.Body, r.CreatedAt, r.UpdatedAt, r.Tags,
			r.Review, r.ReviewedBy, r.ReviewedAt, r.ReviewReason)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, tenantID, r.Name, r.Type)
}

func (s *resourceStore) List(ctx context.Context, tenantID, typ, visibility string) ([]spi.Resource, error) {
	q := `SELECT ` + resourceColumns + ` FROM resources WHERE tenant_id = ?`
	args := []any{tenantID}
	if typ != "" {
		q += ` AND type = ?`
		args = append(args, typ)
	}
	if visibility != "" {
		q += ` AND visibility = ?`
		args = append(args, visibility)
	}
	q += ` ORDER BY type, name`
	rows, err := s.db.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []spi.Resource{}
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *resourceStore) Get(ctx context.Context, tenantID, idOrName, typ string) (*spi.Resource, error) {


	r, err := scanResource(s.db.queryRow(ctx,
		`SELECT `+resourceColumns+` FROM resources WHERE tenant_id = ? AND resource_id = ?`, tenantID, idOrName))
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	q := `SELECT ` + resourceColumns + ` FROM resources WHERE tenant_id = ? AND name = ?`
	args := []any{tenantID, idOrName}
	if typ != "" {
		q += ` AND type = ?`
		args = append(args, typ)
	}
	r, err = scanResource(s.db.queryRow(ctx, q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	return r, err
}

func (s *resourceStore) Delete(ctx context.Context, tenantID, resourceID string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE tenant_id = ? AND resource_id = ?`, tenantID, resourceID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}

func (s *resourceStore) Move(ctx context.Context, tenantID, resourceID, folder string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE resources SET folder = ?, updated_at = ? WHERE tenant_id = ? AND resource_id = ?`,
			folder, now, tenantID, resourceID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}

func (s *resourceStore) SetTags(ctx context.Context, tenantID, resourceID, tags string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	if tags == "" {
		tags = "[]"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE resources SET tags = ?, updated_at = ? WHERE tenant_id = ? AND resource_id = ?`,
			tags, now, tenantID, resourceID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}









func (s *resourceStore) Decide(ctx context.Context, tenantID, resourceID, state, by, reason string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	if state != spi.ReviewAdmitted && state != spi.ReviewDenied {
		return fmt.Errorf("%w: review state %q", spi.ErrInvalid, state)
	}



	if state == spi.ReviewDenied && strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: a denial needs a reason", spi.ErrInvalid)
	}
	if strings.TrimSpace(by) == "" {
		return fmt.Errorf("%w: a verdict has to name who reached it", spi.ErrInvalid)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.db.exec(ctx, func(tx *sql.Tx) error {




		res, err := tx.ExecContext(ctx, `
			UPDATE resources
			   SET review = ?, reviewed_by = ?, reviewed_at = ?, review_reason = ?, updated_at = ?
			 WHERE tenant_id = ? AND resource_id = ? AND review = ?`,
			state, by, now, reason, now, tenantID, resourceID, spi.ReviewPending)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {







			return spi.ErrNotFound
		}
		return nil
	})
}



func (s *resourceStore) CountsByReview(ctx context.Context, tenantID string) (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.db.query(ctx, `SELECT review, COUNT(*) FROM resources WHERE tenant_id = ? GROUP BY review`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}
