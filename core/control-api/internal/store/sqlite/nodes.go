// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type nodeStore struct{ db *DB }




const nodeColumns = `node_id, tenant_id, display_name, roles, master_state, deployment, status, trust,
	platform, arch, agent_version, labels_json, enrolled_at, last_seen_at,
	last_heartbeat_seq, revoked_at, revoke_reason, created_at, updated_at`



const nodeSelectColumns = nodeColumns + `,
	host_address, label, owner_identity_id, inflight_calls, uptime_sec,
	runtime_name, runtime_type, runtime_image, runtime_enterable, runtime_template,
	runtime_state, runtime_status, runtime_restart_count, runtime_started_at,
	runtime_last_exit_code, runtime_oom_killed, runtime_last_error, runtime_probe,
	runtime_memory, runtime_skills_mount_path,
	suspended_at, suspend_reason, suspended_by, remote_shell,
	inbox_waiting, inbox_oldest_waiting_at`

func scanNode(sc interface{ Scan(...any) error }) (*spi.Node, error) {
	var (
		n          spi.Node
		labels     string



		ownerID    sql.NullString
		inboxWaiting sql.NullInt64
		inboxOldest  string
		lastSeen   sql.NullString
		revokedAt  sql.NullString
		enrolledAt string
		createdAt  string
		updatedAt  string

		runtimeStarted  sql.NullString
		runtimeExitCode sql.NullInt64
		runtimeEnter    int
		runtimeOOM      int
		runtimeProbe    string



		suspendedAt string

		remoteShell sql.NullInt64
	)
	var rolesJSON string
	err := sc.Scan(&n.NodeID, &n.TenantID, &n.DisplayName, &rolesJSON,
		&n.MasterState, &n.Deployment, &n.Status, &n.Trust,
		&n.Platform, &n.Arch, &n.AgentVersion, &labels, &enrolledAt, &lastSeen,
		&n.LastHeartbeatSeq, &revokedAt, &n.RevokeReason, &createdAt, &updatedAt,
		&n.HostAddress, &n.Label, &ownerID, &n.InflightCalls, &n.UptimeSec,
		&n.Runtime.Name, &n.Runtime.Type, &n.Runtime.Image, &runtimeEnter, &n.Runtime.Template,
		&n.Runtime.State, &n.Runtime.RuntimeStatus, &n.Runtime.RestartCount, &runtimeStarted,
		&runtimeExitCode, &runtimeOOM, &n.Runtime.LastError, &runtimeProbe,
		&n.Runtime.Memory, &n.Runtime.SkillsMountPath,
		&suspendedAt, &n.SuspendReason, &n.SuspendedBy, &remoteShell,
		&inboxWaiting, &inboxOldest)
	if remoteShell.Valid {
		b := remoteShell.Int64 != 0
		n.RemoteShell = &b
	}




	if inboxWaiting.Valid {
		info := spi.InboxInfo{Waiting: int(inboxWaiting.Int64)}
		info.OldestWaitingAt, _ = parseTime(inboxOldest)
		n.Inbox = &info
	}
	if p := spi.ProbeResult(runtimeProbe); p.Valid() {
		n.Runtime.Probe = p
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if labels != "" {
		_ = json.Unmarshal([]byte(labels), &n.Labels)
	}
	if err := decodeRoles(rolesJSON, &n.Roles); err != nil {
		return nil, err
	}
	n.EnrolledAt, _ = parseTime(enrolledAt)
	n.CreatedAt, _ = parseTime(createdAt)
	n.UpdatedAt, _ = parseTime(updatedAt)
	n.LastSeenAt = nullTime(lastSeen)
	n.RevokedAt = nullTime(revokedAt)
	if suspendedAt != "" {
		if t, err := parseTime(suspendedAt); err == nil {
			n.SuspendedAt = &t
		}
	}

	n.Runtime.Enterable = runtimeEnter != 0
	n.Runtime.OOMKilled = runtimeOOM != 0
	n.Runtime.StartedAt = nullTime(runtimeStarted)
	if runtimeExitCode.Valid {
		code := int(runtimeExitCode.Int64)
		n.Runtime.LastExitCode = &code
	}





	if n.Runtime.RuntimeStatus == "" {
		n.Runtime.RuntimeStatus = spi.StatusUnknown
	}



	n.OwnerIdentityID = ownerID.String
	return &n, nil
}

func (s *nodeStore) Create(ctx context.Context, n *spi.Node) error {
	labels, err := json.Marshal(orEmptyMap(n.Labels))
	if err != nil {
		return fmt.Errorf("sqlite: marshal labels: %w", err)
	}
	now := time.Now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.UpdatedAt = now
	if n.EnrolledAt.IsZero() {
		n.EnrolledAt = now
	}
	if n.TenantID == "" {
		n.TenantID = spi.DefaultTenant
	}
	if n.Status == "" {
		n.Status = spi.NodeStatusPending
	}
	if n.Trust == "" {
		n.Trust = spi.TrustUnverified
	}
	if n.Deployment == "" {



		n.Deployment = spi.DeploymentLocal
	}
	if len(n.Roles) == 0 {
		n.Roles = spi.Roles{spi.RoleAgent}
	}
	if n.Roles.Has(spi.RoleMaster) && n.MasterState == "" {
		n.MasterState = spi.MasterActive
	}

	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO nodes (`+nodeColumns+`, owner_identity_id)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			n.NodeID, n.TenantID, n.DisplayName, encodeRoles(n.Roles),
			n.MasterState, n.Deployment, n.Status, n.Trust,
			n.Platform, n.Arch, n.AgentVersion, string(labels), fmtTime(n.EnrolledAt),
			timePtr(n.LastSeenAt), n.LastHeartbeatSeq, timePtr(n.RevokedAt),
			n.RevokeReason, fmtTime(n.CreatedAt), fmtTime(n.UpdatedAt),








			nullIfEmpty(n.OwnerIdentityID))
		if isUniqueViolation(err) {


			return fmt.Errorf("%w: node name %q already exists", spi.ErrConflict, n.DisplayName)
		}
		return err
	})
}

func (s *nodeStore) Get(ctx context.Context, nodeID string) (*spi.Node, error) {
	return scanNode(s.db.queryRow(ctx, `SELECT `+nodeSelectColumns+` FROM nodes WHERE node_id = ?`, nodeID))
}

func (s *nodeStore) GetByName(ctx context.Context, tenantID, name string) (*spi.Node, error) {
	if tenantID == "" {
		tenantID = spi.DefaultTenant
	}
	return scanNode(s.db.queryRow(ctx,
		`SELECT `+nodeSelectColumns+` FROM nodes WHERE tenant_id = ? AND display_name = ?`,
		tenantID, name))
}

func (s *nodeStore) List(ctx context.Context, f spi.NodeFilter) ([]spi.Node, string, error) {
	where := []string{"tenant_id = ?"}
	args := []any{orDefault(f.TenantID, spi.DefaultTenant)}
	if f.Role != "" {



		where = append(where, "roles LIKE ?")
		args = append(args, `%"`+string(f.Role)+`"%`)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Cursor != "" {


		where = append(where, "node_id > ?")
		args = append(args, f.Cursor)
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	args = append(args, limit+1)

	rows, err := s.db.query(ctx,
		`SELECT `+nodeSelectColumns+` FROM nodes WHERE `+strings.Join(where, " AND ")+
			` ORDER BY node_id LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []spi.Node
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var next string
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].NodeID
	}
	return out, next, nil
}







func (s *nodeStore) SetLabel(ctx context.Context, nodeID, label string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE nodes SET label = ?, updated_at = ? WHERE node_id = ?`,
			label, fmtTime(time.Now()), nodeID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}

func (s *nodeStore) SetHostAddress(ctx context.Context, nodeID, address string) error {
	if address == "" {
		return nil
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE nodes SET host_address = ?, updated_at = ? WHERE node_id = ?`,
			address, fmtTime(time.Now()), nodeID)
		return err
	})
}








func (s *nodeStore) NoteInstructionBlock(ctx context.Context, nodeID, pluginID, path, sha256 string) (bool, error) {
	first := false
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO node_instruction_blocks
			   (node_id, plugin_id, path, sha256, first_seen) VALUES (?, ?, ?, ?, ?)`,
			nodeID, pluginID, path, sha256, fmtTime(time.Now()))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		first = n > 0
		return nil
	})
	return first, err
}



func (s *nodeStore) ForgetInstructionBlock(ctx context.Context, nodeID, pluginID, path, sha256 string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM node_instruction_blocks
			  WHERE node_id = ? AND plugin_id = ? AND path = ? AND sha256 = ?`,
			nodeID, pluginID, path, sha256)
		return err
	})
}

func (s *nodeStore) UpdateHeartbeat(ctx context.Context, nodeID string, hb spi.Heartbeat) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		now := fmtTime(time.Now())




		res, err := tx.ExecContext(ctx, `
			UPDATE nodes
			   SET last_seen_at = ?, last_heartbeat_seq = ?, updated_at = ?,
			       inflight_calls = ?, uptime_sec = ?, remote_shell = ?,
			       status = CASE WHEN status = 'revoked' THEN status ELSE 'active' END
			 WHERE node_id = ?`,
			fmtTime(hb.At), hb.Seq, now, hb.InflightCalls, hb.UptimeSec, boolInt(hb.RemoteShell), nodeID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}





		if hb.Inbox != nil {
			var oldest any = ""
			if !hb.Inbox.OldestWaitingAt.IsZero() {
				oldest = fmtTime(hb.Inbox.OldestWaitingAt)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE nodes SET inbox_waiting = ?, inbox_oldest_waiting_at = ?
				 WHERE node_id = ?`, hb.Inbox.Waiting, oldest, nodeID); err != nil {
				return err
			}
		}





		if hb.Runtime == nil {
			return nil
		}
		rt := hb.Runtime

		var startedAt any
		if rt.StartedAt != nil {
			startedAt = fmtTime(*rt.StartedAt)
		}
		var exitCode any
		if rt.LastExitCode != nil {
			exitCode = *rt.LastExitCode
		}
		status := rt.RuntimeStatus
		if status == "" {


			status = spi.StatusUnknown
		}

		_, err = tx.ExecContext(ctx, `
			UPDATE nodes
			   SET runtime_state = ?, runtime_status = ?, runtime_restart_count = ?,
			       runtime_started_at = ?, runtime_last_exit_code = ?,
			       runtime_oom_killed = ?, runtime_last_error = ?,
			       runtime_probe = ?, runtime_memory = ?, updated_at = ?
			 WHERE node_id = ?`,
			rt.State, string(status), rt.RestartCount,
			startedAt, exitCode, boolInt(rt.OOMKilled), rt.LastError,
			string(rt.Probe), rt.Memory, now, nodeID)
		return err
	})
}


func (s *nodeStore) SetMasterState(ctx context.Context, nodeID string, state spi.MasterState) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE nodes SET master_state = ? WHERE node_id = ?`, string(state), nodeID)
		return err
	})
}

func (s *nodeStore) SetRuntimeDecl(ctx context.Context, nodeID string, rt spi.RuntimeInfo) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE nodes
			   SET runtime_name = ?, runtime_type = ?, runtime_image = ?, runtime_template = ?,
			       runtime_enterable = ?, runtime_skills_mount_path = ?, updated_at = ?
			 WHERE node_id = ?`,
			rt.Name, rt.Type, rt.Image, rt.Template, boolInt(rt.Enterable),
			rt.SkillsMountPath, fmtTime(time.Now()), nodeID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *nodeStore) SetStatus(ctx context.Context, nodeID string, st spi.NodeStatus, reason string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		now := fmtTime(time.Now())
		var res sql.Result
		var err error
		if st == spi.NodeStatusRevoked {







			var displayName string
			if err := tx.QueryRowContext(ctx,
				`SELECT display_name FROM nodes WHERE node_id = ?`, nodeID).Scan(&displayName); err != nil {
				return err
			}
			tombstone := displayName + "@revoked-" + nodeID[:8]

			res, err = tx.ExecContext(ctx, `
				UPDATE nodes SET status = ?, revoked_at = ?, revoke_reason = ?,
				                 updated_at = ?, display_name = ?
				 WHERE node_id = ?`, st, now, reason, now, tombstone, nodeID)
		} else {


			res, err = tx.ExecContext(ctx, `
				UPDATE nodes SET status = ?, updated_at = ?
				 WHERE node_id = ? AND status != 'revoked'`, st, now, nodeID)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}







func (s *nodeStore) SetSuspended(ctx context.Context, nodeID string, suspended bool, reason, by string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		now := fmtTime(time.Now())
		at, why, who := "", "", ""
		if suspended {
			at, why, who = now, reason, by
		}



		res, err := tx.ExecContext(ctx, `
			UPDATE nodes SET suspended_at = ?, suspend_reason = ?, suspended_by = ?,
			                 updated_at = ?
			 WHERE node_id = ? AND status != 'revoked'`, at, why, who, now, nodeID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}






func (s *nodeStore) MarkStaleOffline(ctx context.Context, deadlineUnix, mirrorDeadlineUnix int64) ([]string, error) {
	cutoff := fmtTime(time.Unix(deadlineUnix, 0))



	mirrorCutoff := fmtTime(time.Unix(mirrorDeadlineUnix, 0))

	var changed []string
	rows, err := s.db.query(ctx, `
		SELECT node_id FROM nodes
		 WHERE status = 'active'
		   AND (last_seen_at IS NULL
		        OR (master_state = 'standby' AND last_seen_at < ?)
		        OR (master_state != 'standby' AND last_seen_at < ?))`, mirrorCutoff, cutoff)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		changed = append(changed, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, nil
	}

	err = s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			UPDATE nodes SET status = 'offline', updated_at = ?
			 WHERE status = 'active'
			   AND (last_seen_at IS NULL
			        OR (master_state = 'standby' AND last_seen_at < ?)
			        OR (master_state != 'standby' AND last_seen_at < ?))`,
			fmtTime(time.Now()), mirrorCutoff, cutoff)
		return err
	})
	if err != nil {
		return nil, err
	}
	return changed, nil
}

func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}



func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "constraint failed: UNIQUE")
}






func encodeRoles(rs spi.Roles) string {
	if len(rs) == 0 {
		return "[]"
	}
	b, err := json.Marshal(rs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeRoles(raw string, out *spi.Roles) error {
	if raw == "" {
		*out = nil
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}




func (s *nodeStore) Touch(ctx context.Context, displayName string, at time.Time) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE nodes SET last_seen_at = ?, status = 'active', updated_at = ?
			WHERE tenant_id = ? AND display_name = ? AND status != 'revoked'`,
			fmtTime(at), fmtTime(at), spi.DefaultTenant, displayName)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}

func (s *nodeStore) SetNodeAvatar(ctx context.Context, nodeID, contentType string, full, thumb []byte) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		if len(full) == 0 {
			_, err := tx.ExecContext(ctx, `DELETE FROM node_avatars WHERE node_id = ?`, nodeID)
			return err
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO node_avatars (node_id, content_type, full, thumb, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(node_id) DO UPDATE SET content_type = excluded.content_type, full = excluded.full, thumb = excluded.thumb, updated_at = excluded.updated_at`,
			nodeID, contentType, full, thumb, fmtTime(time.Now()))
		return err
	})
}

func (s *nodeStore) NodeAvatar(ctx context.Context, nodeID string) (string, []byte, []byte, error) {
	var ct string
	var full, thumb []byte
	err := s.db.queryRow(ctx, `SELECT content_type, full, thumb FROM node_avatars WHERE node_id = ?`, nodeID).Scan(&ct, &full, &thumb)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil, nil
	}
	if err != nil {
		return "", nil, nil, err
	}
	return ct, full, thumb, nil
}

func (s *nodeStore) NodeThumbs(ctx context.Context, tenantID string) (map[string]spi.NodeThumb, error) {
	rows, err := s.db.query(ctx, `
		SELECT a.node_id, a.content_type, a.thumb FROM node_avatars a
		  JOIN nodes n ON n.node_id = a.node_id
		 WHERE n.tenant_id = ?`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]spi.NodeThumb{}
	for rows.Next() {
		var id, ct string
		var thumb []byte
		if err := rows.Scan(&id, &ct, &thumb); err != nil {
			return nil, err
		}
		out[id] = spi.NodeThumb{ContentType: ct, Data: thumb}
	}
	return out, rows.Err()
}






func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
