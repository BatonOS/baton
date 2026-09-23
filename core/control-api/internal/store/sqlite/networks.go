// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type networkStore struct{ db *DB }








func (n *networkStore) EnsureIdentity(ctx context.Context, tenantID, displayName string) (*spi.Network, error) {
	if n.db.ReadOnly() {


		got, err := n.Get(ctx, tenantID)
		if err != nil {
			return nil, fmt.Errorf("network: standby has no identity yet: %w", err)
		}
		return got, nil
	}

	if got, err := n.Get(ctx, tenantID); err == nil {
		return got, nil
	} else if !errors.Is(err, spi.ErrNotFound) {
		return nil, err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("network: generate key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))

	rec := &spi.Network{
		NetworkID:    "net_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16],
		TenantID:     tenantID,
		DisplayName:  displayName,
		PublicKeyPEM: pubPEM,
		Fingerprint:  Fingerprint(pubPEM),
		CreatedAt:    time.Now().UTC(),
	}




	if err := n.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO networks
		  (network_id, tenant_id, display_name, public_key_pem, private_key_pem, fingerprint, created_at)
		SELECT ?, ?, ?, ?, ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM networks WHERE tenant_id = ?)`,
		rec.NetworkID, rec.TenantID, rec.DisplayName, rec.PublicKeyPEM, privPEM,
			rec.Fingerprint, rec.CreatedAt.Format(time.RFC3339), tenantID)
		return err
	}); err != nil {
		return nil, err
	}
	return n.Get(ctx, tenantID)
}






func (n *networkStore) Get(ctx context.Context, tenantID string) (*spi.Network, error) {
	var rec spi.Network
	var created, admission, changedAt string
	err := n.db.queryRow(ctx, `
		SELECT network_id, tenant_id, display_name, public_key_pem, fingerprint, created_at,
		       admission, admission_changed_at, admission_changed_by
		  FROM networks WHERE tenant_id = ?`, tenantID).
		Scan(&rec.NetworkID, &rec.TenantID, &rec.DisplayName, &rec.PublicKeyPEM,
			&rec.Fingerprint, &created,
			&admission, &changedAt, &rec.AdmissionChangedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rec.CreatedAt, _ = time.Parse(time.RFC3339, created)
	rec.Admission = spi.NetworkAdmission(admission)
	rec.AdmissionChangedAt, _ = time.Parse(time.RFC3339, changedAt)













	rec.Fingerprint = Fingerprint(rec.PublicKeyPEM)
	return &rec, nil
}




func (n *networkStore) SetAdmission(ctx context.Context, tenantID string, admission spi.NetworkAdmission, by string) error {
	if n.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	if admission != spi.AdmissionOpen && admission != spi.AdmissionClosed {
		return spi.ErrInvalid
	}
	return n.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE networks SET admission = ?, admission_changed_at = ?, admission_changed_by = ?
			  WHERE tenant_id = ?`,
			string(admission), time.Now().UTC().Format(time.RFC3339), by, tenantID)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}






func (n *networkStore) Sign(ctx context.Context, tenantID string, payload []byte) ([]byte, error) {
	var privPEM string
	err := n.db.queryRow(ctx,
		`SELECT private_key_pem FROM networks WHERE tenant_id = ?`, tenantID).Scan(&privPEM)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode([]byte(privPEM))
	if blk == nil {
		return nil, fmt.Errorf("network: stored private key is not PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("network: parse private key: %w", err)
	}
	ed, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("network: stored key is %T, not ed25519", key)
	}
	return ed25519.Sign(ed, payload), nil
}

func (n *networkStore) SetAvatar(ctx context.Context, tenantID, contentType string, data []byte) error {
	if n.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return n.db.exec(ctx, func(tx *sql.Tx) error {
		var networkID string
		if err := tx.QueryRowContext(ctx, `SELECT network_id FROM networks WHERE tenant_id = ?`, tenantID).Scan(&networkID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return spi.ErrNotFound
			}
			return err
		}
		if len(data) == 0 {
			_, err := tx.ExecContext(ctx, `DELETE FROM network_avatars WHERE network_id = ?`, networkID)
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO network_avatars (network_id, content_type, data, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(network_id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data, updated_at = excluded.updated_at`,
			networkID, contentType, data, time.Now().UTC().Format(time.RFC3339))
		return err
	})
}

func (n *networkStore) GetAvatar(ctx context.Context, tenantID string) (string, []byte, error) {
	var contentType string
	var data []byte
	err := n.db.queryRow(ctx, `
		SELECT a.content_type, a.data FROM network_avatars a
		  JOIN networks nw ON nw.network_id = a.network_id
		 WHERE nw.tenant_id = ?`, tenantID).Scan(&contentType, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return contentType, data, nil
}

func (n *networkStore) Rename(ctx context.Context, tenantID, displayName string) error {
	if n.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return n.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE networks SET display_name = ? WHERE tenant_id = ?`, displayName, tenantID)
		if err != nil {
			return err
		}
		if k, _ := res.RowsAffected(); k == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}

func (n *networkStore) PutAddress(ctx context.Context, networkID, address, source string) error {
	if n.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	err := n.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
		INSERT INTO network_addresses (network_id, address, source, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(network_id, address) DO UPDATE SET source = excluded.source`,
			networkID, address, source, time.Now().UTC().Format(time.RFC3339))
		return err
	})
	return err
}

func (n *networkStore) Addresses(ctx context.Context, networkID string) ([]spi.NetworkAddress, error) {
	rows, err := n.db.query(ctx,
		`SELECT address, source, created_at FROM network_addresses WHERE network_id = ? ORDER BY address`,
		networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []spi.NetworkAddress{}
	for rows.Next() {
		var a spi.NetworkAddress
		var created string
		if err := rows.Scan(&a.Address, &a.Source, &created); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (n *networkStore) PutEndpoint(ctx context.Context, networkID string, ep spi.NetworkEndpoint) error {
	if n.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	err := n.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
		INSERT INTO network_endpoints (network_id, address, port, protocol, priority, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(network_id, address, port) DO UPDATE SET
		  protocol = excluded.protocol, priority = excluded.priority, updated_at = excluded.updated_at`,
			networkID, ep.Address, ep.Port, ep.Protocol, ep.Priority,
			time.Now().UTC().Format(time.RFC3339))
		return err
	})
	return err
}







func (n *networkStore) ReplaceEndpoint(ctx context.Context, networkID string, ep spi.NetworkEndpoint) error {
	if n.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return n.db.exec(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM network_endpoints WHERE network_id = ?`, networkID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
		INSERT INTO network_endpoints (network_id, address, port, protocol, priority, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
			networkID, ep.Address, ep.Port, ep.Protocol, ep.Priority,
			time.Now().UTC().Format(time.RFC3339))
		return err
	})
}

func (n *networkStore) Endpoints(ctx context.Context, networkID string) ([]spi.NetworkEndpoint, error) {
	rows, err := n.db.query(ctx, `
		SELECT address, port, protocol, priority, updated_at
		  FROM network_endpoints WHERE network_id = ?
		 ORDER BY priority DESC, address`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []spi.NetworkEndpoint{}
	for rows.Next() {
		var e spi.NetworkEndpoint
		var updated string
		if err := rows.Scan(&e.Address, &e.Port, &e.Protocol, &e.Priority, &updated); err != nil {
			return nil, err
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, e)
	}
	return out, rows.Err()
}


























func Fingerprint(pemText string) string {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return ""
	}








	canon := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: block.Bytes})
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(canon))))
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}
