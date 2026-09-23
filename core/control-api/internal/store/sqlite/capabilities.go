// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type capabilityStore struct{ db *DB }

const capColumns = `id, node_id, name, version, risk, input_schema_json,
	output_schema_json, allow_from_json, health, registered_at, last_call_at`

func scanCapability(sc interface{ Scan(...any) error }) (*spi.Capability, error) {
	var (
		c            spi.Capability
		inSchema     string
		outSchema    string
		allowFrom    string
		registeredAt string
		lastCall     sql.NullString
	)
	err := sc.Scan(&c.ID, &c.NodeID, &c.Name, &c.Version, &c.Risk, &inSchema,
		&outSchema, &allowFrom, &c.Health, &registeredAt, &lastCall)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.InputSchema = []byte(inSchema)
	c.OutputSchema = []byte(outSchema)
	if allowFrom != "" {
		_ = json.Unmarshal([]byte(allowFrom), &c.AllowFrom)
	}
	c.RegisteredAt, _ = parseTime(registeredAt)
	c.LastCallAt = nullTime(lastCall)
	return &c, nil
}
















func (s *capabilityStore) Upsert(ctx context.Context, c *spi.Capability) error {
	allowFrom, err := json.Marshal(orEmptySlice(c.AllowFrom))
	if err != nil {
		return err
	}
	if c.RegisteredAt.IsZero() {
		c.RegisteredAt = time.Now()
	}
	if c.Health == "" {
		c.Health = "unknown"
	}

	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO capabilities (`+capColumns+`)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(node_id, name, version) DO UPDATE SET
				risk               = excluded.risk,
				input_schema_json  = excluded.input_schema_json,
				output_schema_json = excluded.output_schema_json,
				allow_from_json    = excluded.allow_from_json,
				health             = excluded.health,
				registered_at      = excluded.registered_at`,
			c.ID, c.NodeID, c.Name, c.Version, c.Risk,
			orEmptyJSON(c.InputSchema), orEmptyJSON(c.OutputSchema), string(allowFrom),
			c.Health, fmtTime(c.RegisteredAt), timePtr(c.LastCallAt))
		return err
	})
}

func (s *capabilityStore) Get(ctx context.Context, id string) (*spi.Capability, error) {
	return scanCapability(s.db.queryRow(ctx,
		`SELECT `+capColumns+` FROM capabilities WHERE id = ?`, id))
}



func (s *capabilityStore) Resolve(ctx context.Context, nodeID, name, version string) (*spi.Capability, error) {
	if version != "" {
		return scanCapability(s.db.queryRow(ctx,
			`SELECT `+capColumns+` FROM capabilities
			  WHERE node_id = ? AND name = ? AND version = ?`, nodeID, name, version))
	}
	return scanCapability(s.db.queryRow(ctx,
		`SELECT `+capColumns+` FROM capabilities
		  WHERE node_id = ? AND name = ? ORDER BY version DESC LIMIT 1`, nodeID, name))
}

func (s *capabilityStore) ListByNode(ctx context.Context, nodeID string) ([]spi.Capability, error) {
	return s.list(ctx, `SELECT `+capColumns+` FROM capabilities WHERE node_id = ? ORDER BY name`, nodeID)
}

func (s *capabilityStore) List(ctx context.Context, tenantID string) ([]spi.Capability, error) {
	return s.list(ctx, `
		SELECT `+prefixed(capColumns, "c")+`
		  FROM capabilities c JOIN nodes n ON n.node_id = c.node_id
		 WHERE n.tenant_id = ? ORDER BY c.name`, orDefault(tenantID, spi.DefaultTenant))
}

func (s *capabilityStore) list(ctx context.Context, q string, args ...any) ([]spi.Capability, error) {
	rows, err := s.db.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []spi.Capability
	for rows.Next() {
		c, err := scanCapability(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func (s *capabilityStore) SetHealth(ctx context.Context, id, health string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE capabilities SET health = ? WHERE id = ?`, health, id)
		return err
	})
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func orEmptySlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyJSON(b []byte) string {
	if len(b) == 0 {
		return "{}"
	}
	return string(b)
}


func prefixed(columns, alias string) string {
	out := make([]byte, 0, len(columns)*2)
	for i, part := range splitAndTrim(columns) {
		if i > 0 {
			out = append(out, ',', ' ')
		}
		out = append(out, alias...)
		out = append(out, '.')
		out = append(out, part...)
	}
	return string(out)
}

func splitAndTrim(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := trimSpace(s[start:i])
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && isSpace(s[i]) {
		i++
	}
	for j > i && isSpace(s[j-1]) {
		j--
	}
	return s[i:j]
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }
