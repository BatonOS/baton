// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"

	"github.com/batonos/baton/core/agent/internal/inbox"
	"github.com/batonos/baton/core/agent/internal/protocol"
	"github.com/batonos/baton/core/agent/internal/supervisor"
)















func (a *Agent) drainOutbox(ctx context.Context, conn *protocol.Conn) {
	a.drainBox(ctx, conn, inbox.New(a.cfg.DataDir), "", false)
	if a.sup == nil {
		return
	}
	for id, isolation := range a.sup.ServiceIsolation() {
		box := inbox.WithOutbox(a.cfg.DataDir, supervisor.ServiceOutbox(a.cfg.DataDir, id))
		a.drainBox(ctx, conn, box, id, isolation == "isolated")
	}
}

func (a *Agent) drainBox(ctx context.Context, conn *protocol.Conn, box *inbox.Box, via string, isolated bool) {
	queued, err := box.Outgoing()
	if err != nil || len(queued) == 0 {
		return
	}

	for _, m := range queued {



		if a.outboxInflight(m.LocalID) {
			continue
		}





		if m.MessageID == "" {
			m.MessageID = protocol.NewMessageID()
			if err := box.AssignMessageID(m.LocalID, m.MessageID); err != nil {
				a.logger.Error("assign message id", "local_id", m.LocalID, "error", err)
				continue
			}
		}









		if len(m.AttachPaths) > 0 && len(m.Attachments) == 0 {
			uploaded, uerr := a.uploadAttachments(ctx, m.AttachPaths)
			if uerr != nil {




				a.logger.Warn("upload attachments", "local_id", m.LocalID, "error", uerr)
				continue
			}
			if rerr := box.RecordAttachments(m.LocalID, uploaded); rerr != nil {
				a.logger.Error("record attachments", "local_id", m.LocalID, "error", rerr)
				continue
			}
			m.Attachments = uploaded
		}




		a.setOutboxInflight(m.LocalID, box)
		err := conn.SendWithID(ctx, protocol.TypeMessageSend, m.LocalID, a.nextSeq(),
			protocol.MessageSend{
				To: m.To, ContentType: m.ContentType, Payload: m.Payload,
				MessageID: m.MessageID, ReplyTo: m.ReplyTo, Type: m.Type,




				Identity: m.Identity,
				Attachments: outboxRefs(m.Attachments),
				Via:         via, ViaIsolated: isolated, For: m.For,
			})
		if err != nil {


			a.setOutboxInflight(m.LocalID, nil)
			a.logger.Debug("outbox send deferred", "local_id", m.LocalID, "error", err)
			return
		}
		a.logger.Info("message sent", "local_id", m.LocalID, "message_id", m.MessageID,
			"to", m.To, "bytes", len(m.Payload))
	}
}






func (a *Agent) onMessageSendAck(localID string, ack protocol.MessageSendAck) {
	box := a.outboxOf(localID)
	a.setOutboxInflight(localID, nil)
	if box == nil {


		box = inbox.New(a.cfg.DataDir)
	}

	if ack.Error == "" {











		if err := box.MarkSent(localID, ack.MessageID); err != nil && !errors.Is(err, context.Canceled) {
			a.logger.Error("record sent message", "local_id", localID, "error", err)
		}
		a.logger.Info("message accepted", "local_id", localID, "message_id", ack.MessageID)
		return
	}
	a.logger.Info("message refused", "local_id", localID, "error", ack.Error)
	if err := box.NoteRefusal(localID, ack.Error+": "+ack.Reason); err != nil {
		a.logger.Error("record refusal", "local_id", localID, "error", err)
	}



	if err := box.MarkSent(localID, ""); err != nil && !errors.Is(err, context.Canceled) {
		a.logger.Error("record refused message", "local_id", localID, "error", err)
	}
}













func (a *Agent) outboxInflight(localID string) bool {
	return a.outboxOf(localID) != nil
}




func (a *Agent) outboxOf(localID string) *inbox.Box {
	a.outboxMu.Lock()
	defer a.outboxMu.Unlock()
	return a.outboxSending[localID]
}

func (a *Agent) setOutboxInflight(localID string, box *inbox.Box) {
	a.outboxMu.Lock()
	defer a.outboxMu.Unlock()
	if a.outboxSending == nil {
		a.outboxSending = map[string]*inbox.Box{}
	}
	if box != nil {
		a.outboxSending[localID] = box
	} else {
		delete(a.outboxSending, localID)
	}
}




func outboxRefs(in []inbox.Attachment) []protocol.AttachmentRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]protocol.AttachmentRef, 0, len(in))
	for _, a := range in {
		out = append(out, protocol.AttachmentRef{
			Name: a.Name, ContentType: a.ContentType, Size: a.Size, SHA256: a.SHA256,
		})
	}
	return out
}
