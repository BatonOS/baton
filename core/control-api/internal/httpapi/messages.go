// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)





const MaxMessageTTL = 30 * 24 * time.Hour




const MaxMessageTypeBytes = 128







const DefaultMessageTTL = 7 * 24 * time.Hour



const MaxPayloadBytes = 512 << 10


















const (
	MaxAttachmentBytesLocal        = 10 << 20
	MaxAttachmentBytesCrossNetwork = 2 << 20
)





func attachmentLimit(localNetwork, destNetwork string) int64 {




	if destNetwork == "" || destNetwork == localNetwork {
		return MaxAttachmentBytesLocal
	}
	return MaxAttachmentBytesCrossNetwork
}




func attachmentRefusal(name string, size, limit int64, crossNetwork bool) string {
	where := "within this network"
	if crossNetwork {
		where = "across networks"
	}
	return fmt.Sprintf("attachment %s is %d bytes, and the limit %s is %d",
		name, size, where, limit)
}





type attachmentRef struct {
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
}

type sendRequest struct {
	To string `json:"to"`












	ContentType string `json:"content_type"`





	Type string `json:"type,omitempty"`

	Body []byte `json:"body"`
	TTL  string `json:"ttl"`





	MessageID string `json:"message_id,omitempty"`




	Attachments []attachmentRef `json:"attachments,omitempty"`







	ReplyTo string `json:"reply_to,omitempty"`

	For string `json:"for,omitempty"`
}
















func routeFor(a *API, ident *spi.Identity) string {
	if ident == nil || ident.NodeID == "" {
		return "unplaced"
	}
	if a.Hub == nil {
		return "offline"
	}
	if _, ok := a.Hub.Get(ident.NodeID); !ok {
		return "offline"
	}
	return "connected"
}




type messageView struct {
	MessageID string `json:"message_id"`





	Sender        string `json:"sender"`
	Recipient     string `json:"recipient"`
	SourceNetwork string `json:"source_network"`





	SourceAddress      string `json:"source_address,omitempty"`
	DestinationNetwork string `json:"destination_network"`
	State              string `json:"state"`



	ThreadID string `json:"thread_id"`
	ReplyTo  string `json:"reply_to,omitempty"`

	For string `json:"for,omitempty"`

	Type string `json:"type,omitempty"`



	Box string `json:"box"`







	InboxPolicy string `json:"inbox_policy"`




	HeldReason string    `json:"held_reason"`




	Via         string `json:"via,omitempty"`
	ViaIsolated *bool  `json:"via_isolated,omitempty"`
	CreatedAt  time.Time `json:"created_at"`









	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
	ContentType string     `json:"content_type,omitempty"`
	PayloadSize int        `json:"payload_size"`









	Attachments []channel.AttachmentRef `json:"attachments,omitempty"`
}



const CodeMessageIDTaken = "MESSAGE_ID_TAKEN"










func validMessageID(id string) bool {
	rest, ok := strings.CutPrefix(id, "msg_")
	if !ok {
		return false
	}
	_, err := uuid.Parse(rest)
	return err == nil
}











func newMessageID() string {
	return channel.NewMessageID()
}






func (a *API) localNetworkID(ctx context.Context) string {
	net, err := a.Store.Networks().Get(ctx, spi.DefaultTenant)
	if err != nil {
		return ""
	}
	return net.NetworkID
}





func envelopeView(m spi.Message, v policyVerdict) messageView {
	return messageView{
		InboxPolicy: v.Word, HeldReason: v.Reason,
		MessageID: m.MessageID,
		Sender:    m.SourceAgent, Recipient: m.DestinationAgent,
		SourceNetwork: m.SourceNetwork, SourceAddress: m.SourceAddress,
		DestinationNetwork: m.DestinationNetwork,
		State:              string(m.State), CreatedAt: m.CreatedAt, DeliveredAt: deliveredAt(m),
		ThreadID: m.ThreadID, ReplyTo: m.ReplyTo, Type: m.Type,
		Via: m.Via, ViaIsolated: viaIsolated(m), For: m.For,
		Box: boxOf(m), ExpiresAt: m.ExpiresAt,
		ContentType: m.ContentType, PayloadSize: m.PayloadSize,
	}
}



func viaIsolated(m spi.Message) *bool {
	if m.Via == "" {
		return nil
	}
	v := m.ViaIsolated
	return &v
}







func envelopeViewWith(ctx context.Context, a *API, m spi.Message) messageView {
	v := envelopeView(m, a.policyVerdictFor(ctx, m))
	v.Attachments = attachmentRefs(ctx, a, m.MessageID)
	return v
}



type policyVerdict struct {
	Word   string
	Reason string
}






























func (a *API) policyVerdictFor(ctx context.Context, m spi.Message) policyVerdict {
	ident, err := a.Store.Identities().Resolve(ctx, spi.DefaultTenant, m.DestinationAgent)
	if err != nil {
		return policyVerdict{Word: "unknown"}
	}
	if ident.InboxPolicy.Allows(m.SourceNetwork, m.SourceAgent, a.localNetworkID(ctx)) {





		if ident.InboxPolicy.HoldsChannel(m.Via) {
			if m.ViaIsolated {
				return policyVerdict{Word: "held", Reason: "channel"}
			}
			return policyVerdict{Word: "held", Reason: "channel (unverified)"}
		}
		return policyVerdict{Word: "allowed"}
	}




	if ident.InboxPolicy.ActOn == spi.InboxActOnNobody {
		return policyVerdict{Word: "held", Reason: "nobody"}
	}
	return policyVerdict{Word: "held", Reason: "sender-not-allowed"}
}








func (a *API) handleMessageSend(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	var req sendRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.To == "" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			"a message needs a recipient", "Address it to an identity: --to @reviewer.", nil)
		return
	}
	if len(req.Body) == 0 {
		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			"a message needs a body", "Pass --text or --file.", nil)
		return
	}
	if len(req.Body) > MaxPayloadBytes {
		a.fail(w, r, http.StatusRequestEntityTooLarge, "OUTPUT_TOO_LARGE",
			"the message body is larger than the channel carries",
			"Put it in the workspace and send a reference. The control channel is not a file transfer mechanism.",
			map[string]any{"limit_bytes": MaxPayloadBytes, "got_bytes": len(req.Body)})
		return
	}
	if len(req.Type) > MaxMessageTypeBytes {
		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			"type is longer than an envelope field carries",
			"128 bytes at most. It is a label, not a place for content.",
			map[string]any{"limit_bytes": MaxMessageTypeBytes, "got_bytes": len(req.Type)})
		return
	}

	ctx := r.Context()




	if looksLikeAgentAddress(req.To) {
		a.sendAcrossNetworks(w, r, req)
		return
	}




	ident, err := a.Store.Identities().Resolve(ctx, spi.DefaultTenant, req.To)
	if errors.Is(err, spi.ErrNotFound) {
		a.fail(w, r, http.StatusNotFound, channel.ErrIdentityNotFound,
			"no agent is registered under that name",
			"List them with `baton agents`, or bind one with `baton identity bind`.", nil)
		return
	}
	if err != nil {
		a.failInternal(w, r, err)
		return
	}




	ttl := DefaultMessageTTL
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil || d <= 0 {
			a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
				"ttl is not a duration this system reads",
				"Use Go duration syntax: 90m, 12h, 168h.", nil)
			return
		}
		if d > MaxMessageTTL {
			a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
				"ttl is longer than retention allows",
				"The ceiling is 720h (30 days). Retention is finite by default.",
				map[string]any{"max": MaxMessageTTL.String(), "got": req.TTL})
			return
		}
		ttl = d
	}





	messageID := req.MessageID
	if messageID == "" {
		messageID = newMessageID()
	} else if !validMessageID(messageID) {
		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			"message_id is not in the form msg_<uuid>",
			"Omit it and one will be minted, or reuse the id of the send you are retrying.", nil)
		return
	}









	network := a.localNetworkID(r.Context())
	if network == "" {
		a.fail(w, r, http.StatusInternalServerError, "NO_NETWORK_IDENTITY",
			"this control plane could not read its own network identity",
			"This is a server-side fault, not a problem with the request; retry, and check the control plane's logs if it persists.", nil)
		return
	}

	msg := &spi.Message{
		MessageID:          messageID,
		TenantID:           spi.DefaultTenant,
		SourceAgent:        p.Subject,
		SourceNetwork:      network,
		DestinationAgent:   req.To,
		DestinationNetwork: network,
		CreatedAt:          time.Now().UTC(),
		ReplyTo:            req.ReplyTo,
		Type:               req.Type,
		For:                req.For,
		State:              spi.MessageUnread,
		ExpiresAt:          time.Now().UTC().Add(ttl),
		ContentType:        req.ContentType,
		Payload:            req.Body,
	}
	if msg.ContentType == "" {
		msg.ContentType = "text/plain"
	}




	manifest, aerr := a.resolveManifest(ctx, req.Attachments, msg.DestinationNetwork)
	if aerr != nil {
		aerr.write(a, w, r)
		return
	}

	duplicate := false
	if err := a.Store.Messages().Enqueue(ctx, msg); errors.Is(err, spi.ErrConflict) {






		if msg.SourceAgent != p.Subject {





			_, _ = a.Log.Append(ctx, audit.Record{
				Event: "message.id_taken", Actor: p.Subject, ActorType: "user",
				Action: "send", Target: messageID, Result: "refused",
				SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
			})
			a.fail(w, r, http.StatusConflict, CodeMessageIDTaken,
				"that message id belongs to another sender",
				"Message ids are unique across the network. Use a fresh one, or "+
					"omit it and one will be minted.", nil)
			return
		}
		duplicate = true
	} else if err != nil {
		a.failStore(w, r, err)
		return
	}

	if !duplicate && len(manifest) > 0 {




		if err := a.Store.Attachments().Attach(ctx, msg.MessageID, manifest); err != nil {
			a.failStore(w, r, err)
			return
		}
	}

	if duplicate {



		a.writeJSON(w, http.StatusAccepted, map[string]any{
			"message":   envelopeViewWith(r.Context(), a, *msg),
			"delivered": !msg.DeliveredAt.IsZero(),
			"route":     routeFor(a, ident),
			"duplicate": true,
		})
		return
	}



	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "message.accepted", Actor: p.Subject, ActorType: "user",
		Action: "send", Target: req.To, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r),
		Detail: map[string]any{
			"message_id": msg.MessageID,
			"bytes":      len(req.Body),
		},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}


	delivered := a.tryDeliver(ctx, ident, msg)




	a.writeJSON(w, http.StatusAccepted, map[string]any{
		"message": envelopeViewWith(r.Context(), a, *msg),



		"delivered": delivered,



		"route": routeFor(a, ident),



		"duplicate": false,
	})
}






func (a *API) tryDeliver(ctx context.Context, ident *spi.Identity, msg *spi.Message) bool {
	if ident.NodeID == "" {
		return false
	}












	if !ident.InboxPolicy.Allows(msg.SourceNetwork, msg.SourceAgent, a.localNetworkID(ctx)) ||
		ident.InboxPolicy.HoldsChannel(msg.Via) {
		return false
	}













	if !msg.ExpiresAt.IsZero() && !msg.ExpiresAt.After(time.Now()) {
		return false
	}
	conn, live := a.Hub.Get(ident.NodeID)
	if !live {
		return false
	}







	if _, busy := a.deliveriesInFlight.LoadOrStore(msg.MessageID, struct{}{}); busy {
		row, err := a.Store.Messages().Get(ctx, msg.MessageID)
		return err == nil && !row.DeliveredAt.IsZero()
	}
	defer a.deliveriesInFlight.Delete(msg.MessageID)





	if row, err := a.Store.Messages().Get(ctx, msg.MessageID); err == nil && !row.DeliveredAt.IsZero() {
		return true
	}

	frame := channel.MessageDeliver{
		MessageID:   msg.MessageID,
		SourceAgent: msg.SourceAgent, SourceNetwork: msg.SourceNetwork,
		SourceAddress:    msg.SourceAddress,
		DestinationAgent: msg.DestinationAgent, DestinationNetwork: msg.DestinationNetwork,
		ThreadID: msg.ThreadID, ReplyTo: msg.ReplyTo, Type: msg.Type,
		Via: msg.Via, ViaIsolated: viaIsolated(*msg), For: msg.For,
		CreatedAt: msg.CreatedAt.Format(time.RFC3339), ExpiresAt: msg.ExpiresAt.Format(time.RFC3339),
		ContentType: msg.ContentType, PayloadSize: len(msg.Payload), Payload: msg.Payload,



		Attachments: attachmentRefs(ctx, a, msg.MessageID),
	}
	reply, err := conn.Request(ctx, channel.TypeMessageDeliver, frame, 10*time.Second)
	if err != nil {
		return false
	}
	var ack channel.MessageAck
	if err := reply.Decode(&ack); err != nil || ack.Reason != "" {
		return false
	}

	if err := a.Store.Messages().MarkDelivered(ctx, msg.MessageID); err != nil {








		if !errors.Is(err, spi.ErrNotFound) {
			return false
		}
		row, gerr := a.Store.Messages().Get(ctx, msg.MessageID)
		return gerr == nil && !row.DeliveredAt.IsZero()
	}
	return true
}







func (a *API) drainInbox(ctx context.Context, nodeID string) {
	idents, err := a.Store.Identities().List(ctx, spi.DefaultTenant)
	if err != nil {
		a.Logger.Warn("drain inbox: list identities", "node_id", nodeID, "error", err)
		return
	}

	localNetwork := a.localNetworkID(ctx)

	for i := range idents {
		if idents[i].NodeID != nodeID {
			continue
		}





		if idents[i].InboxPolicy.ActOn == spi.InboxActOnNobody {
			continue
		}



		delivered, halted := a.drainOne(ctx, &idents[i], localNetwork)
		if delivered > 0 || halted {






			a.Logger.Info("drained queued messages",
				"identity", idents[i].Name, "node_id", nodeID,
				"count", delivered, "complete", !halted)
		}
	}
}
















func (a *API) drainOne(ctx context.Context, ident *spi.Identity, localNetwork string) (delivered int, halted bool) {
	const page = 100
	for {













		queued, err := a.Store.Messages().List(ctx, spi.DefaultTenant, spi.MessageFilter{
			Recipient: ident.Name, Undelivered: true, Limit: page, WithPayload: true,
			Source: ident.InboxPolicy.SourceFilter(localNetwork),




			UnexpiredAt: time.Now(),
		})
		if err != nil {
			a.Logger.Warn("drain inbox: list messages", "identity", ident.Name, "error", err)
			return delivered, true
		}
		for j := range queued {
			if !a.tryDeliver(ctx, ident, &queued[j]) {











				return delivered, true
			}
			delivered++
		}


		if len(queued) < page {
			return delivered, false
		}
	}
}




func deliveredAt(m spi.Message) *time.Time {
	if m.DeliveredAt.IsZero() {
		return nil
	}
	t := m.DeliveredAt
	return &t
}






func boxOf(m spi.Message) string {
	if m.DeletedAt.IsZero() {
		return "inbox"
	}
	return "deleted"
}







func (a *API) handleMessageDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	id := r.PathValue("message_id")
	ctx := r.Context()

	if err := a.Store.Messages().Delete(ctx, id); err != nil {
		if errors.Is(err, spi.ErrNotFound) {
			a.fail(w, r, http.StatusNotFound, "NOT_FOUND", "no such message",
				"List them with `baton inbox`.", nil)
			return
		}
		a.failStore(w, r, err)
		return
	}
	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "message.deleted", Actor: p.Subject, ActorType: "user",
		Action: "delete", Target: id, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r),
		Detail: map[string]any{"message_id": id},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"message_id": id, "box": "deleted"})
}












func (a *API) handleMessageMarkAllRead(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	ctx := r.Context()
	to := r.URL.Query().Get("to")

	n, err := a.Store.Messages().MarkAllRead(ctx, spi.DefaultTenant, to)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "inbox.mark_all_read", Actor: p.Subject, ActorType: "user",
		Action: "mark-all-read", Target: to, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r),
		Detail: map[string]any{"marked": n},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}
	unread, err := a.Store.Messages().CountUnread(ctx, spi.DefaultTenant, to)
	if err != nil {
		a.failInternal(w, r, err)
		return
	}


	a.writeJSON(w, http.StatusOK, map[string]any{"marked": n, "unread": unread})
}

func (a *API) handleMessageList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}


	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > 1000 {
			a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
				"limit must be an integer between 1 and 1000",
				"Omit it for the default of 100.", nil)
			return
		}
		limit = n
	}
	f := spi.MessageFilter{
		Recipient: r.URL.Query().Get("to"),
		State:     spi.MessageState(r.URL.Query().Get("state")),
		Limit:     limit,


		Deleted: r.URL.Query().Get("box") == "deleted",


		ThreadID: r.URL.Query().Get("thread"),




		NewestFirst: true,
	}
	if f.ThreadID != "" {
		f.NewestFirst = false
	}











	if f.Recipient != "" {
		if _, err := a.Store.Identities().Resolve(r.Context(), spi.DefaultTenant, f.Recipient); err != nil {
			if errors.Is(err, spi.ErrNotFound) {
				a.fail(w, r, http.StatusNotFound, channel.ErrIdentityNotFound,
					"no agent is registered under that name",
					"List them with `baton agents`. An agent that exists but has no mail answers with an empty list, not this.", nil)
				return
			}
			a.failInternal(w, r, err)
			return
		}
	}
	msgs, err := a.Store.Messages().List(r.Context(), spi.DefaultTenant, f)
	if err != nil {
		a.failInternal(w, r, err)
		return
	}




	unread, err := a.Store.Messages().CountUnread(r.Context(), spi.DefaultTenant, f.Recipient)
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	out := make([]messageView, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, envelopeViewWith(r.Context(), a, m))
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"messages": out, "unread": unread})
}









func (a *API) handleMessageGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	msg, err := a.Store.Messages().Get(r.Context(), r.PathValue("message_id"))
	if errors.Is(err, spi.ErrNotFound) {
		a.fail(w, r, http.StatusNotFound, "NOT_FOUND", "no such message",
			"List them with `baton inbox`.", nil)
		return
	}
	if err != nil {
		a.failInternal(w, r, err)
		return
	}


	msg.Payload = nil
	a.writeJSON(w, http.StatusOK, map[string]any{"message": envelopeViewWith(r.Context(), a, *msg)})
}

type identityView struct {
	Name      string    `json:"name"`
	NodeID    string    `json:"node_id,omitempty"`
	Bound     bool      `json:"bound"`
	CreatedAt time.Time `json:"created_at"`
}






func (a *API) handleIdentityList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	ids, err := a.Store.Identities().List(r.Context(), spi.DefaultTenant)
	if err != nil {
		a.failInternal(w, r, err)
		return
	}
	out := make([]identityView, 0, len(ids))
	for _, i := range ids {
		out = append(out, identityView{
			Name: i.Name, NodeID: i.NodeID, Bound: i.NodeID != "", CreatedAt: i.CreatedAt,
		})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{"identities": out})
}

type identityBindRequest struct {
	Name   string `json:"name"`
	NodeID string `json:"node_id"`
}







func (a *API) handleIdentityBind(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	var req identityBindRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_ARGUMENT",
			"an identity needs a name", "Give one: --name reviewer.", nil)
		return
	}

	ctx := r.Context()
	nodeID := req.NodeID













	if req.NodeID != "" {
















		n, err := a.Store.Nodes().GetByName(ctx, spi.DefaultTenant, req.NodeID)
		if errors.Is(err, spi.ErrNotFound) {
			n, err = a.Store.Nodes().Get(ctx, req.NodeID)
		}
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		if n.Status == spi.NodeStatusRevoked {
			a.fail(w, r, http.StatusConflict, "NODE_REVOKED",
				"that node is revoked, so nobody can be reached there",
				"Bind to a live node, or leave the identity unplaced — an identity with no "+
					"node is legitimate and keeps its name and its messages.", nil)
			return
		}












		nodeID = n.NodeID
	}

































	if nodeID != "" {
		others, lerr := a.Store.Identities().List(ctx, spi.DefaultTenant)
		if lerr != nil {
			a.failStore(w, r, lerr)
			return
		}
		for _, o := range others {
			if o.NodeID == nodeID && o.Name != req.Name {
				a.fail(w, r, http.StatusConflict, CodeNameConflict,
					"that node already holds the identity "+o.Name,
					"A node holds one agent. Unbind "+o.Name+" first, or pick another node.", nil)
				return
			}
		}



		node, nerr := a.Store.Nodes().Get(ctx, nodeID)
		if nerr != nil {
			a.failStore(w, r, nerr)
			return
		}









		ownerName := ""
		if node.OwnerIdentityID != "" {
			for _, o := range others {
				if o.IdentityID == node.OwnerIdentityID {
					ownerName = o.Name
					break
				}
			}
		}
		if ownerName != req.Name {
			a.fail(w, r, http.StatusConflict, CodeNameConflict,
				"that node is not owned by "+req.Name,
				"An identity can only be active on a node it owns. Ownership is fixed when the "+
					"node is built; changing it is destroy, build, clone.", nil)
			return
		}
	}

	ident, err := a.Store.Identities().Bind(ctx, spi.DefaultTenant, req.Name, nodeID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}

	event := "identity.bound"
	if req.NodeID == "" {
		event = "identity.unbound"
	}
	if _, err := a.Log.Append(ctx, audit.Record{
		Event: event, Actor: p.Subject, ActorType: "user",
		Action: "bind", Target: req.Name, Result: "ok",
		SourceIP: clientIP(r), NodeID: req.NodeID, RequestID: requestIDFrom(r),
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}















	if ident.NodeID != "" {
		go a.drainInbox(context.WithoutCancel(ctx), ident.NodeID)
	}

	a.writeJSON(w, http.StatusOK, identityView{
		Name: ident.Name, NodeID: ident.NodeID, Bound: ident.NodeID != "", CreatedAt: ident.CreatedAt,
	})
}
























func (a *API) senderFor(ctx context.Context, node *spi.Node, declared string) (string, channel.MessageSendAck) {
	bound, err := a.Store.Identities().List(ctx, spi.DefaultTenant)
	if err != nil {



		return "", channel.MessageSendAck{Error: "INTERNAL", Reason: "could not read this node's identities"}
	}














	mine := ""
	for _, id := range bound {
		if id.NodeID == node.NodeID {
			mine = id.Name
			break
		}
	}

	if declared != "" {
		if declared == mine {
			return mine, channel.MessageSendAck{}
		}



		return "", channel.MessageSendAck{
			Error:  "INVALID_ARGUMENT",
			Reason: fmt.Sprintf("%q is not an identity bound to this node", declared),
		}
	}

	if mine != "" {
		return mine, channel.MessageSendAck{}
	}



	return "node:" + node.DisplayName, channel.MessageSendAck{}
}

func (a *API) acceptFromNode(ctx context.Context, node *spi.Node, req channel.MessageSend) (string, channel.MessageSendAck) {
	if req.To == "" {
		return "", channel.MessageSendAck{
			Error: "INVALID_ARGUMENT", Reason: "a message needs a recipient",
		}
	}
	if len(req.Payload) == 0 {
		return "", channel.MessageSendAck{
			Error: "INVALID_ARGUMENT", Reason: "a message needs a body",
		}
	}
	if len(req.Payload) > MaxPayloadBytes {
		return "", channel.MessageSendAck{
			Error:  "OUTPUT_TOO_LARGE",
			Reason: "put it in the workspace and send a reference; this channel is not a file transfer",
		}
	}
	if len(req.Type) > MaxMessageTypeBytes {
		return "", channel.MessageSendAck{
			Error:  "INVALID_ARGUMENT",
			Reason: "type is longer than an envelope field carries (128 bytes); it is a label, not content",
		}
	}













	if looksLikeAgentAddress(req.To) {
		return a.nodeSendAcrossNetworks(ctx, node, req)
	}

	recipient, err := a.Store.Identities().Resolve(ctx, spi.DefaultTenant, req.To)
	if errors.Is(err, spi.ErrNotFound) {
		return "", channel.MessageSendAck{
			Error:  channel.ErrIdentityNotFound,
			Reason: "no agent is registered under that name",
		}
	}
	if err != nil {
		return "", channel.MessageSendAck{Error: "INTERNAL", Reason: "could not resolve the recipient"}
	}

	sender, senderAck := a.senderFor(ctx, node, req.Identity)
	if senderAck.Error != "" {
		return "", senderAck
	}





	network := a.localNetworkID(ctx)
	if network == "" {
		return "", channel.MessageSendAck{
			Error: "NO_NETWORK_IDENTITY", Reason: "the control plane could not read its network identity; retry",
		}
	}

	messageID := req.MessageID
	if messageID == "" {
		messageID = newMessageID()
	} else if !validMessageID(messageID) {
		return "", channel.MessageSendAck{
			Error:  "INVALID_ARGUMENT",
			Reason: "message_id is not in the form msg_<uuid>",
		}
	}

	msg := &spi.Message{
		MessageID:          messageID,
		TenantID:           spi.DefaultTenant,
		SourceAgent:        sender,
		SourceNetwork:      network,
		DestinationAgent:   req.To,
		DestinationNetwork: network,
		CreatedAt:          time.Now().UTC(),
		ReplyTo:            req.ReplyTo,
		Type:               req.Type,
		For:                req.For,


		Via:         req.Via,
		ViaIsolated: req.Via != "" && req.ViaIsolated,
		State:              spi.MessageUnread,
		ExpiresAt:          time.Now().UTC().Add(DefaultMessageTTL),
		ContentType:        req.ContentType,
		Payload:            req.Payload,
	}
	if msg.ContentType == "" {
		msg.ContentType = "text/plain"
	}



	manifest, merr := a.resolveManifest(ctx, nodeRefs(req.Attachments), msg.DestinationNetwork)
	if merr != nil {
		return "", channel.MessageSendAck{Error: merr.code, Reason: merr.message}
	}

	err = a.Store.Messages().Enqueue(ctx, msg)
	if errors.Is(err, spi.ErrConflict) {






		if msg.SourceAgent != sender {
			return "", channel.MessageSendAck{
				Error:  CodeMessageIDTaken,
				Reason: "that message id belongs to another sender",
			}
		}



		return msg.MessageID, channel.MessageSendAck{MessageID: msg.MessageID}
	}
	if errors.Is(err, spi.ErrInvalid) {




		return "", channel.MessageSendAck{
			Error: "INVALID_ARGUMENT", Reason: "reply_to names no message this control plane holds",
		}
	}
	if err != nil {



		return "", channel.MessageSendAck{Error: "READ_ONLY", Reason: "this control plane is a standby"}
	}



	if len(manifest) > 0 {
		if aerr := a.Store.Attachments().Attach(ctx, msg.MessageID, manifest); aerr != nil {
			return "", channel.MessageSendAck{Error: CodeInternal, Reason: "could not record the attachments"}
		}
	}



	_, _ = a.Log.Append(ctx, audit.Record{
		Event: "message.accepted", Actor: sender, ActorType: "node",
		Action: "send", Target: req.To, Result: "ok",
		Detail: map[string]any{"message_id": msg.MessageID, "bytes": len(req.Payload)},
	})

	a.tryDeliver(ctx, recipient, msg)
	return msg.MessageID, channel.MessageSendAck{MessageID: msg.MessageID}
}














func (a *API) handleMessageRead(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	id := r.PathValue("message_id")
	ctx := r.Context()

	msg, err := a.Store.Messages().Get(ctx, id)
	if errors.Is(err, spi.ErrNotFound) {
		a.fail(w, r, http.StatusNotFound, "NOT_FOUND", "no such message",
			"List them with `baton inbox`.", nil)
		return
	}
	if err != nil {
		a.failInternal(w, r, err)
		return
	}















	if !msg.ExpiresAt.IsZero() && !time.Now().Before(msg.ExpiresAt) {
		a.fail(w, r, http.StatusGone, "EXPIRED",
			"this message's body is gone; the envelope is kept, the body is not",
			"Retention is finite by design.", nil)
		return
	}
	if len(msg.Payload) == 0 {




		a.failInternal(w, r, fmt.Errorf("message %s has no body and has not expired", msg.MessageID))
		return
	}

	if _, err := a.Log.Append(ctx, audit.Record{
		Event: "message.read", Actor: p.Subject, ActorType: "user",
		Action: "read", Target: msg.DestinationAgent, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r),
		Detail: map[string]any{"message_id": msg.MessageID, "bytes": len(msg.Payload)},
	}); err != nil {
		a.failInternal(w, r, err)
		return
	}













	if err := a.Store.Messages().MarkRead(ctx, msg.MessageID); err != nil {
		a.failStore(w, r, err)
		return
	}

	ct := msg.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(msg.Payload)
}
