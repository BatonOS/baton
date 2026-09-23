// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)










type identityStore struct{ db *DB }

const identityColumns = `identity_id, tenant_id, name, node_id, created_at, bound_at,
	inbox_act_on, inbox_allow_senders, inbox_allow_networks, inbox_channels`

func scanIdentity(sc interface{ Scan(...any) error }) (*spi.Identity, error) {
	var (
		i       spi.Identity
		nodeID  sql.NullString
		created string
		bound   string
	)
	var actOn, senders, networks, channels string
	err := sc.Scan(&i.IdentityID, &i.TenantID, &i.Name, &nodeID, &created, &bound,
		&actOn, &senders, &networks, &channels)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	i.NodeID = nodeID.String
	i.CreatedAt, _ = parseTime(created)
	i.BoundAt, _ = parseTime(bound)
	i.InboxPolicy = readInboxPolicy(actOn, senders, networks, channels)
	return &i, nil
}









func readInboxPolicy(actOn, senders, networks, channels string) spi.InboxPolicy {
	p := spi.InboxPolicy{
		ActOn:         spi.InboxActOn(actOn),
		AllowSenders:  splitPolicyList(senders),
		AllowNetworks: splitPolicyList(networks),
		Channels:      spi.InboxChannels(channels),
		Set:           actOn != "",
	}
	if !p.Set {
		p.ActOn = spi.InboxActOnEveryone
	}
	if p.Channels == "" {
		p.Channels = spi.InboxChannelsAllow
	}
	return p
}




func splitPolicyList(v string) []string {
	out := []string{}
	for _, part := range strings.Split(v, "\n") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}




func writePolicyList(v []string) string {
	keep := make([]string, 0, len(v))
	for _, s := range v {
		if s != "" {
			keep = append(keep, s)
		}
	}
	return strings.Join(keep, "\n")
}















































var identityNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

func validIdentityName(name string) error {
	if identityNamePattern.MatchString(name) {
		return nil
	}
	if strings.Contains(name, ".") {
		return fmt.Errorf("%w: an agent name may not contain a dot: it is what separates the network from the agent in <network>.<agent>@<domain>", spi.ErrInvalid)
	}













	if strings.Contains(name, "@") {
		return fmt.Errorf("%w: an agent name may not contain @: it is what marks a sender as belonging to ANOTHER network, and a recipient has no other way to tell", spi.ErrInvalid)
	}
	return fmt.Errorf("%w: an agent name is lower-case letters, digits and hyphens, starting with a letter or digit", spi.ErrInvalid)
}

func (s *identityStore) Bind(ctx context.Context, tenantID, name, nodeID string) (*spi.Identity, error) {
	if err := validIdentityName(name); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var node any
	if nodeID != "" {
		node = nodeID
	}
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO identities (identity_id, tenant_id, name, node_id, created_at, bound_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(tenant_id, name) DO UPDATE SET
				node_id  = excluded.node_id,
				bound_at = excluded.bound_at`,
			"id_"+tenantID+"_"+name, tenantID, name, node, fmtTime(now), fmtTime(now))
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Resolve(ctx, tenantID, name)
}

func (s *identityStore) Ensure(ctx context.Context, tenantID, name string) (*spi.Identity, error) {
	if err := validIdentityName(name); err != nil {
		return nil, err
	}



	now := time.Now().UTC()
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO identities (identity_id, tenant_id, name, node_id, created_at, bound_at)
			VALUES (?, ?, ?, NULL, ?, '')
			ON CONFLICT(tenant_id, name) DO NOTHING`,
			"id_"+tenantID+"_"+name, tenantID, name, fmtTime(now))
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Resolve(ctx, tenantID, name)
}

func (s *identityStore) Resolve(ctx context.Context, tenantID, name string) (*spi.Identity, error) {
	return scanIdentity(s.db.queryRow(ctx,
		`SELECT `+identityColumns+` FROM identities WHERE tenant_id = ? AND name = ?`,
		tenantID, name))
}

func (s *identityStore) List(ctx context.Context, tenantID string) ([]spi.Identity, error) {
	rows, err := s.db.query(ctx,
		`SELECT `+identityColumns+` FROM identities WHERE tenant_id = ? ORDER BY name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []spi.Identity{}
	for rows.Next() {
		i, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}









func (s *identityStore) SetInboxPolicy(ctx context.Context, tenantID, name string, p spi.InboxPolicy) (*spi.Identity, error) {
	switch p.ActOn {
	case spi.InboxActOnEveryone, spi.InboxActOnAllowlist, spi.InboxActOnNobody:
	default:
		return nil, fmt.Errorf("sqlite: inbox policy: unknown act_on %q", p.ActOn)
	}
	switch p.Channels {
	case spi.InboxChannelsAllow, spi.InboxChannelsHold:
	default:
		return nil, fmt.Errorf("sqlite: inbox policy: unknown channels %q", p.Channels)
	}








	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE identities SET inbox_act_on = ?, inbox_allow_senders = ?, inbox_allow_networks = ?, inbox_channels = ?
			  WHERE tenant_id = ? AND name = ?`,
			string(p.ActOn), writePolicyList(p.AllowSenders), writePolicyList(p.AllowNetworks), string(p.Channels),
			tenantID, name)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Resolve(ctx, tenantID, name)
}



type messageStore struct{ db *DB }



const envelopeColumns = `message_id, tenant_id, source_agent, source_network, source_address,
	destination_agent, destination_network, thread_id, reply_to, type, created_at, state,
	delivered_at, deleted_at, expires_at, content_type, payload_size, via, via_isolated, for_facility`

func scanEnvelope(sc interface{ Scan(...any) error }) (*spi.Message, error) {
	var (
		m         spi.Message
		state     string
		created   string
		delivered string
		deleted   string
		expires   string
		isolated  int
	)
	err := sc.Scan(&m.MessageID, &m.TenantID, &m.SourceAgent, &m.SourceNetwork, &m.SourceAddress,
		&m.DestinationAgent, &m.DestinationNetwork, &m.ThreadID, &m.ReplyTo, &m.Type, &created, &state,
		&delivered, &deleted, &expires, &m.ContentType, &m.PayloadSize, &m.Via, &isolated, &m.For)
	m.ViaIsolated = isolated != 0
	if errors.Is(err, sql.ErrNoRows) {
		return nil, spi.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.State = spi.MessageState(state)
	m.CreatedAt, _ = parseTime(created)
	m.DeliveredAt, _ = parseTime(delivered)
	m.DeletedAt, _ = parseTime(deleted)
	m.ExpiresAt, _ = parseTime(expires)
	return &m, nil
}

func (s *messageStore) Enqueue(ctx context.Context, m *spi.Message) error {
	if m.ExpiresAt.IsZero() {



		return errors.New("sqlite: a message needs an expiry; retention is finite by default")
	}
	if m.State == "" {


		m.State = spi.MessageUnread
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}







	if m.ReplyTo != "" {
		parent, err := s.Get(ctx, m.ReplyTo)
		if err != nil {
			if errors.Is(err, spi.ErrNotFound) {
				return fmt.Errorf("%w: reply_to names no message: %s", spi.ErrInvalid, m.ReplyTo)
			}
			return err
		}
		m.ThreadID = parent.ThreadID
	} else if m.ThreadID == "" {
		m.ThreadID = m.MessageID
	}
	m.PayloadSize = len(m.Payload)






	var existing *spi.Message
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO messages (message_id, tenant_id, source_agent, source_network, source_address,
				destination_agent, destination_network, thread_id, reply_to, type, created_at, state,
				expires_at, content_type, payload_size, payload, via, via_isolated, for_facility)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			m.MessageID, m.TenantID, m.SourceAgent, m.SourceNetwork, m.SourceAddress,
			m.DestinationAgent, m.DestinationNetwork, m.ThreadID, m.ReplyTo, m.Type,
			fmtTime(m.CreatedAt), string(m.State),
			fmtTime(m.ExpiresAt), m.ContentType, m.PayloadSize, m.Payload, m.Via, boolInt(m.ViaIsolated), m.For)
		if isUniqueViolation(err) {



			existing, err = scanEnvelope(tx.QueryRowContext(ctx,
				`SELECT `+envelopeColumns+` FROM messages WHERE message_id = ?`, m.MessageID))
			if err != nil {
				return err
			}
			return spi.ErrConflict
		}
		return err
	})
	if errors.Is(err, spi.ErrConflict) {





		*m = *existing
		return spi.ErrConflict
	}
	return err
}

func (s *messageStore) List(ctx context.Context, tenantID string, f spi.MessageFilter) ([]spi.Message, error) {
	q := `SELECT ` + envelopeColumns + ` FROM messages WHERE tenant_id = ?`
	args := []any{tenantID}
	if f.Recipient != "" {
		q += ` AND destination_agent = ?`
		args = append(args, f.Recipient)
	}


	if f.ThreadID != "" {
		q += ` AND thread_id = ?`
		args = append(args, f.ThreadID)
	}
	if f.Deleted {
		q += ` AND deleted_at != ''`
	} else {
		q += ` AND deleted_at = ''`
	}
	if f.State != "" {
		q += ` AND state = ?`
		args = append(args, string(f.State))
	}
	if f.Undelivered {
		q += ` AND delivered_at = ''`
	}




	if !f.UnexpiredAt.IsZero() {
		q += ` AND expires_at > ?`
		args = append(args, fmtTime(f.UnexpiredAt))
	}













	if f.Source != nil {
		terms := []string{}
		for _, n := range f.Source.Networks {
			if n == "" {
				continue
			}
			terms = append(terms, `source_network = ?`)
			args = append(args, n)
		}
		if f.Source.LocalNetwork != "" {
			for _, sender := range f.Source.LocalSenders {
				if sender == "" {
					continue
				}
				terms = append(terms, `(source_network = ? AND source_agent = ?)`)
				args = append(args, f.Source.LocalNetwork, sender)
			}
		}
		if len(terms) == 0 {
			q += ` AND 1 = 0`
		} else {
			q += ` AND (` + strings.Join(terms, ` OR `) + `)`
		}
	}


	if f.NewestFirst {
		q += ` ORDER BY created_at DESC, message_id DESC`
	} else {
		q += ` ORDER BY created_at, message_id`
	}
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}

	rows, err := s.db.query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []spi.Message{}
	for rows.Next() {
		m, err := scanEnvelope(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}



	if f.WithPayload {
		for i := range out {
			full, err := s.Get(ctx, out[i].MessageID)
			if err != nil {
				return nil, err
			}
			out[i].Payload = full.Payload
		}
	}
	return out, nil
}

func (s *messageStore) Get(ctx context.Context, messageID string) (*spi.Message, error) {
	m, err := scanEnvelope(s.db.queryRow(ctx,
		`SELECT `+envelopeColumns+` FROM messages WHERE message_id = ?`, messageID))
	if err != nil {
		return nil, err
	}
	var payload []byte
	if err := s.db.queryRow(ctx,
		`SELECT payload FROM messages WHERE message_id = ?`, messageID).Scan(&payload); err != nil {
		return nil, err
	}
	m.Payload = payload
	return m, nil
}









func (s *messageStore) MarkDelivered(ctx context.Context, messageID string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE messages
			   SET delivered_at = ?
			 WHERE message_id = ? AND delivered_at = ''`,
			fmtTime(time.Now().UTC()), messageID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}






func (s *messageStore) MarkRead(ctx context.Context, messageID string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE messages SET state = ? WHERE message_id = ?`,
			string(spi.MessageRead), messageID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return spi.ErrNotFound
		}
		return nil
	})
}










func (s *messageStore) Delete(ctx context.Context, messageID string) error {
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE messages SET deleted_at = ?
			 WHERE message_id = ? AND deleted_at = ''`,
			fmtTime(time.Now().UTC()), messageID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {


			var one int
			if err := tx.QueryRowContext(ctx,
				`SELECT 1 FROM messages WHERE message_id = ?`, messageID).Scan(&one); err != nil {
				return spi.ErrNotFound
			}
		}
		return nil
	})
}






func (s *messageStore) MarkAllRead(ctx context.Context, tenantID, recipient string) (int, error) {
	var n int64
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		q := `UPDATE messages SET state = ? WHERE tenant_id = ? AND state = ? AND deleted_at = ''`
		args := []any{string(spi.MessageRead), tenantID, string(spi.MessageUnread)}
		if recipient != "" {
			q += ` AND destination_agent = ?`
			args = append(args, recipient)
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}






func (s *messageStore) CountUnread(ctx context.Context, tenantID, recipient string) (int, error) {


	q := `SELECT COUNT(*) FROM messages WHERE tenant_id = ? AND state = ? AND deleted_at = ''`
	args := []any{tenantID, string(spi.MessageUnread)}
	if recipient != "" {
		q += ` AND destination_agent = ?`
		args = append(args, recipient)
	}
	var n int
	if err := s.db.readHandle().QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}





func (s *messageStore) Expire(ctx context.Context, now int64) (int, error) {
	var n int64
	err := s.db.exec(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE messages SET payload = NULL, payload_size = 0
			 WHERE expires_at <= ? AND payload IS NOT NULL`,
			fmtTime(time.Unix(now, 0)))
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return int(n), err
}
