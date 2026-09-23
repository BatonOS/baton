// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"

	"github.com/batonos/baton/core/agent/internal/inbox"
	"github.com/batonos/baton/core/agent/internal/protocol"
	"github.com/batonos/baton/core/agent/internal/supervisor"
)













func (a *Agent) deliverMessage(ctx context.Context, conn *protocol.Conn, msgID string, m protocol.MessageDeliver) error {
	ack := protocol.MessageAck{MessageID: m.MessageID}






	put := inbox.New(a.cfg.DataDir).Put
	if m.For != "" {
		st, ok := "", false
		if a.sup != nil {
			st, ok = a.sup.ServiceStatus(m.For)
		}
		if !ok || (st != "running" && st != "unknown") {
			ack.Reason = "no facility " + m.For + " running on this node"
			a.logger.Warn("delivery refused: no such facility running",
				"message_id", m.MessageID, "for", m.For, "status", st)
			return conn.Reply(ctx, protocol.TypeMessageAck, msgID, ack)
		}
		put = inbox.FlatMailbox(supervisor.ServiceInbox(a.cfg.DataDir, m.For)).Put
	}
	err := put(inbox.Message{
		MessageID:   m.MessageID,




		Sender:      addr(m.SourceAgent, m.SourceAddress, m.SourceNetwork, m.DestinationNetwork),
		Recipient:   m.DestinationAgent,



		ThreadID:    m.ThreadID,
		ReplyTo:     m.ReplyTo,
		Type:        m.Type,
		Via:         m.Via,
		ViaIsolated: m.ViaIsolated,
		For:         m.For,
		CreatedAt:   m.CreatedAt,
		ExpiresAt:   m.ExpiresAt,
		ContentType: m.ContentType,
		PayloadSize: m.PayloadSize,
		Payload:     m.Payload,



		Attachments: inboxAttachments(m.Attachments),
	})
	if err != nil {



		ack.Reason = "node could not store the message"
		a.logger.Error("store inbound message",
			"message_id", m.MessageID, "sender", m.SourceAgent, "error", err)
		return conn.Reply(ctx, protocol.TypeMessageAck, msgID, ack)
	}











	if a.sup != nil {
		a.sup.NotifyMail()
	}




	a.logger.Info("message received",
		"message_id", m.MessageID,
		"sender", m.SourceAgent,
		"source_network", m.SourceNetwork,
		"recipient", m.DestinationAgent,
		"bytes", m.PayloadSize)

	return conn.Reply(ctx, protocol.TypeMessageAck, msgID, ack)
}



















func addr(agent, address, from, to string) string {
	if from == "" || from == to {
		return agent
	}
	if address != "" {
		return address
	}
	return agent + "@" + from
}




func inboxAttachments(in []protocol.AttachmentRef) []inbox.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]inbox.Attachment, 0, len(in))
	for _, a := range in {
		out = append(out, inbox.Attachment{
			Name: a.Name, ContentType: a.ContentType, Size: a.Size, SHA256: a.SHA256,
		})
	}
	return out
}













func (a *Agent) mailWaiting() bool {
	box := inbox.New(a.cfg.DataDir)
	names, err := box.Mailboxes()
	if err != nil {
		a.logger.Warn("wake on mail: list mailboxes", "error", err)
		return false
	}
	for _, n := range names {
		mb, err := box.Mailbox(n)
		if err != nil {
			a.logger.Warn("wake on mail: open mailbox", "identity", n, "error", err)
			continue
		}
		msgs, err := mb.List()
		if err != nil {
			a.logger.Warn("wake on mail: list messages", "identity", n, "error", err)
			continue
		}
		if len(msgs) > 0 {
			return true
		}
	}
	return false
}














func (a *Agent) inboxState() *protocol.InboxState {
	box := inbox.New(a.cfg.DataDir)
	names, err := box.Mailboxes()
	if err != nil {
		a.logger.Warn("inbox state: list mailboxes", "error", err)
		return nil
	}
	st := &protocol.InboxState{}
	oldest := ""
	for _, n := range names {
		mb, err := box.Mailbox(n)
		if err != nil {
			a.logger.Warn("inbox state: open mailbox", "identity", n, "error", err)
			return nil
		}
		msgs, err := mb.List()
		if err != nil {
			a.logger.Warn("inbox state: list messages", "identity", n, "error", err)
			return nil
		}
		st.Waiting += len(msgs)
		for i := range msgs {






			if c := msgs[i].CreatedAt; c != "" && (oldest == "" || c < oldest) {
				oldest = c
			}
		}
	}
	st.OldestWaitingCreatedAt = oldest
	return st
}
