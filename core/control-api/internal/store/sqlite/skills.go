// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type skillStore struct{ db *DB }

const skillColumns = `skill_id, tenant_id, name, version, sha256, source_url, size_bytes, created_at`

func scanSkill(row interface{ Scan(...any) error }) (*spi.Skill, error) {
	var s spi.Skill
	var created string
	err := row.Scan(&s.SkillID, &s.TenantID, &s.Name, &s.Version, &s.SHA256,
		&s.SourceURL, &s.SizeBytes, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt, _ = parseTime(created)
	return &s, nil
}







func (s *skillStore) Create(ctx context.Context, in spi.Skill) (*spi.Skill, error) {
	if s.db.ReadOnly() {
		return nil, spi.ErrReadOnly
	}
	if existing, err := s.byDigest(ctx, in.TenantID, in.SHA256); err == nil {
		return existing, nil
	} else if !errors.Is(err, spi.ErrNotFound) {
		return nil, err
	}

	if in.SkillID == "" {
		in.SkillID = "skl_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	}
	now := time.Now().UTC()
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO skills (skill_id, tenant_id, name, version, sha256, source_url, size_bytes, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			in.SkillID, in.TenantID, in.Name, in.Version, in.SHA256,
			in.SourceURL, in.SizeBytes, fmtTime(now))
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, in.TenantID, in.SkillID)
}

func (s *skillStore) byDigest(ctx context.Context, tenantID, digest string) (*spi.Skill, error) {
	return scanSkill(s.db.queryRow(ctx,
		`SELECT `+skillColumns+` FROM skills WHERE tenant_id = ? AND sha256 = ?`,
		tenantID, digest))
}

func (s *skillStore) Get(ctx context.Context, tenantID, skillID string) (*spi.Skill, error) {
	return scanSkill(s.db.queryRow(ctx,
		`SELECT `+skillColumns+` FROM skills WHERE tenant_id = ? AND skill_id = ?`,
		tenantID, skillID))
}

func (s *skillStore) List(ctx context.Context, tenantID string) ([]spi.Skill, error) {
	rows, err := s.db.query(ctx,
		`SELECT `+skillColumns+` FROM skills WHERE tenant_id = ? ORDER BY name, version`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []spi.Skill{}
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sk)
	}
	return out, rows.Err()
}

func (s *skillStore) Delete(ctx context.Context, tenantID, skillID string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {



		res, err := tx.ExecContext(ctx,
			`DELETE FROM skills WHERE tenant_id = ? AND skill_id = ?`, tenantID, skillID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}






func (s *skillStore) Install(ctx context.Context, in spi.SkillInstall) (*spi.SkillInstall, error) {
	if s.db.ReadOnly() {
		return nil, spi.ErrReadOnly
	}
	if in.TargetKind != spi.SkillTargetNode && in.TargetKind != spi.SkillTargetTemplate {
		return nil, errors.New("store: skill install target must be node or template")
	}
	if in.InstallID == "" {
		in.InstallID = "sin_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	}
	now := time.Now().UTC()
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO skill_installs
			  (install_id, tenant_id, skill_id, target_kind, target, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (tenant_id, skill_id, target_kind, target) DO NOTHING`,
			in.InstallID, in.TenantID, in.SkillID, in.TargetKind, in.Target, fmtTime(now))
		return err
	})
	if err != nil {
		return nil, err
	}



	return scanInstall(s.db.queryRow(ctx, `
		SELECT install_id, tenant_id, skill_id, target_kind, target, created_at
		  FROM skill_installs
		 WHERE tenant_id = ? AND skill_id = ? AND target_kind = ? AND target = ?`,
		in.TenantID, in.SkillID, in.TargetKind, in.Target))
}

func scanInstall(row interface{ Scan(...any) error }) (*spi.SkillInstall, error) {
	var in spi.SkillInstall
	var created string
	err := row.Scan(&in.InstallID, &in.TenantID, &in.SkillID, &in.TargetKind, &in.Target, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	in.CreatedAt, _ = parseTime(created)
	return &in, nil
}

func (s *skillStore) Uninstall(ctx context.Context, tenantID, skillID, kind, target string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			DELETE FROM skill_installs
			 WHERE tenant_id = ? AND skill_id = ? AND target_kind = ? AND target = ?`,
			tenantID, skillID, kind, target)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}

func (s *skillStore) Installs(ctx context.Context, tenantID, skillID string) ([]spi.SkillInstall, error) {
	rows, err := s.db.query(ctx, `
		SELECT install_id, tenant_id, skill_id, target_kind, target, created_at
		  FROM skill_installs
		 WHERE tenant_id = ? AND skill_id = ?
		 ORDER BY target_kind, target`, tenantID, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []spi.SkillInstall{}
	for rows.Next() {
		in, err := scanInstall(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rows.Err()
}












func (s *skillStore) SetCode(ctx context.Context, nodeID, skillID, codeID string) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {



		_, err := tx.ExecContext(ctx, `
			INSERT INTO skill_codes (node_id, skill_id, code_id, issued_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(node_id, skill_id) DO UPDATE SET
				code_id = excluded.code_id, issued_at = excluded.issued_at`,
			nodeID, skillID, codeID, fmtTime(time.Now()))
		return err
	})
}

func (s *skillStore) CodesForNode(ctx context.Context, nodeID string) (map[string]string, error) {
	rows, err := s.db.query(ctx, `SELECT skill_id, code_id FROM skill_codes WHERE node_id = ?`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var skillID, codeID string
		if err := rows.Scan(&skillID, &codeID); err != nil {
			return nil, err
		}
		out[skillID] = codeID
	}
	return out, rows.Err()
}

func (s *skillStore) ForNode(ctx context.Context, tenantID, nodeID, template string) ([]spi.InstalledSkill, error) {
	rows, err := s.db.query(ctx, `
		SELECT `+prefixed(skillColumns, "s")+`,
		       MIN(CASE i.target_kind WHEN 'node' THEN 0 ELSE 1 END) AS via_rank
		  FROM skills s
		  JOIN skill_installs i ON i.skill_id = s.skill_id AND i.tenant_id = s.tenant_id
		 WHERE s.tenant_id = ?
		   AND ( (i.target_kind = 'node' AND i.target = ?)
		      OR (i.target_kind = 'template' AND i.target = ? AND ? <> '') )
		 GROUP BY s.skill_id
		 ORDER BY s.name, s.version`,
		tenantID, nodeID, template, template)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []spi.InstalledSkill{}
	for rows.Next() {
		var sk spi.Skill
		var created string
		var rank int


		if err := rows.Scan(&sk.SkillID, &sk.TenantID, &sk.Name, &sk.Version, &sk.SHA256,
			&sk.SourceURL, &sk.SizeBytes, &created, &rank); err != nil {
			return nil, err
		}
		sk.CreatedAt, _ = parseTime(created)
		via := spi.SkillTargetTemplate
		if rank == 0 {
			via = spi.SkillTargetNode
		}
		out = append(out, spi.InstalledSkill{Skill: sk, Via: via})
	}
	return out, rows.Err()
}
