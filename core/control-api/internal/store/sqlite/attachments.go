// SPDX-License-Identifier: Apache-2.0

package sqlite










import (
	"context"
	"database/sql"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)

type attachmentStore struct{ db *DB }

func (s *DB) Attachments() spi.AttachmentStore { return &attachmentStore{db: s} }







func (s *attachmentStore) Attach(ctx context.Context, messageID string, items []spi.Attachment) error {
	if s.db.ReadOnly() {
		return spi.ErrReadOnly
	}
	return s.db.exec(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM message_attachments WHERE message_id = ?`, messageID); err != nil {
			return err
		}
		for i, a := range items {



			if _, err := tx.ExecContext(ctx,
				`INSERT INTO message_attachments
				   (message_id, idx, name, content_type, size, sha256)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				messageID, i, a.Name, a.ContentType, a.Size, a.SHA256); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *attachmentStore) List(ctx context.Context, messageID string) ([]spi.Attachment, error) {
	rows, err := s.db.query(ctx,
		`SELECT message_id, idx, name, content_type, size, sha256
		   FROM message_attachments WHERE message_id = ? ORDER BY idx`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()


	out := []spi.Attachment{}
	for rows.Next() {
		var a spi.Attachment
		if err := rows.Scan(&a.MessageID, &a.Index, &a.Name, &a.ContentType, &a.Size, &a.SHA256); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}





func (s *attachmentStore) Referenced(ctx context.Context, sha256 string) (bool, error) {
	var n int
	err := s.db.queryRow(ctx,
		`SELECT COUNT(1) FROM message_attachments WHERE sha256 = ?`, sha256).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
