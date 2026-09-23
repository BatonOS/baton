// SPDX-License-Identifier: Apache-2.0

package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)


var (
	ErrNodeNotConnected = errors.New("channel: node is not connected")
	ErrTimeout          = errors.New("channel: peer did not answer in time")
	ErrSeqViolation     = errors.New("channel: sequence violation")
)


const HeartbeatInterval = 15 * time.Second







const MissedHeartbeatsBeforeOffline = 3


type Conn struct {
	NodeID   string
	Role     string
	ws       *websocket.Conn
	hub      *Hub
	lastSeq  int64


	writeMu  sync.Mutex
	pending  sync.Map


	ptySubs  sync.Map
	closed   chan struct{}
	closeOne sync.Once
}


func (c *Conn) SubscribePty(session string) chan Frame {
	ch := make(chan Frame, 256)
	c.ptySubs.Store(session, ch)
	return ch
}


func (c *Conn) UnsubscribePty(session string) {
	if v, ok := c.ptySubs.LoadAndDelete(session); ok {
		close(v.(chan Frame))
	}
}



func (c *Conn) RoutePty(session string, f Frame) {
	if v, ok := c.ptySubs.Load(session); ok {
		select {
		case v.(chan Frame) <- f:
		default:
		}
	}
}


type Hub struct {
	mu    sync.RWMutex
	conns map[string]*Conn



	OnSeqViolation func(nodeID string, got, want int64)
}


func NewHub() *Hub { return &Hub{conns: map[string]*Conn{}} }






func (h *Hub) Register(c *Conn) {
	h.mu.Lock()
	previous, existed := h.conns[c.NodeID]
	h.conns[c.NodeID] = c
	h.mu.Unlock()

	if existed && previous != c {
		previous.Close(CloseNormal, "replaced by a newer connection")
	}
}



func (h *Hub) Unregister(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if current, ok := h.conns[c.NodeID]; ok && current == c {
		delete(h.conns, c.NodeID)
	}
}


func (h *Hub) Get(nodeID string) (*Conn, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.conns[nodeID]
	return c, ok
}


func (h *Hub) Connected() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]string, 0, len(h.conns))
	for id := range h.conns {
		out = append(out, id)
	}
	return out
}


func (h *Hub) CloseAll(reason string) {
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.conns = map[string]*Conn{}
	h.mu.Unlock()

	for _, c := range conns {
		c.Close(CloseNormal, reason)
	}
}


func NewConn(ws *websocket.Conn, nodeID, role string, hub *Hub) *Conn {
	ws.SetReadLimit(MaxFrameBytes)
	return &Conn{NodeID: nodeID, Role: role, ws: ws, hub: hub, closed: make(chan struct{})}
}


func (c *Conn) Send(ctx context.Context, f Frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(raw) > MaxFrameBytes {
		return fmt.Errorf("channel: frame is %d bytes, over the %d limit", len(raw), MaxFrameBytes)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()



	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, raw)
}


func (c *Conn) Request(ctx context.Context, typ string, payload any, timeout time.Duration) (Frame, error) {
	msgID := uuid.NewString()
	f, err := NewFrame(typ, msgID, payload)
	if err != nil {
		return Frame{}, err
	}

	reply := make(chan Frame, 1)
	c.pending.Store(msgID, reply)
	defer c.pending.Delete(msgID)

	if err := c.Send(ctx, f); err != nil {
		return Frame{}, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case got := <-reply:
		return got, nil
	case <-timer.C:
		return Frame{}, ErrTimeout
	case <-c.closed:
		return Frame{}, ErrNodeNotConnected
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	}
}









func (c *Conn) Read(ctx context.Context) (Frame, error) {
	typ, raw, err := c.ws.Read(ctx)
	if err != nil {
		return Frame{}, err
	}
	if typ != websocket.MessageText {
		return Frame{}, fmt.Errorf("channel: expected a text frame, got %v", typ)
	}

	var f Frame
	if err := json.Unmarshal(raw, &f); err != nil {
		return Frame{}, fmt.Errorf("channel: malformed frame: %w", err)
	}
	if f.V != EnvelopeVersion {
		return Frame{}, fmt.Errorf("channel: unsupported envelope version %d (this build speaks %d)",
			f.V, EnvelopeVersion)
	}


	if reply, ok := c.pending.Load(f.MsgID); ok && f.MsgID != "" {
		if ch, ok := reply.(chan Frame); ok {
			select {
			case ch <- f:
			default:
			}
		}
		return f, nil
	}

	if f.Seq > 0 {
		want := c.lastSeq + 1
		switch {
		case f.Seq <= c.lastSeq:
			if c.hub != nil && c.hub.OnSeqViolation != nil {
				c.hub.OnSeqViolation(c.NodeID, f.Seq, want)
			}
			return f, fmt.Errorf("%w: got seq %d, expected %d", ErrSeqViolation, f.Seq, want)
		case f.Seq > want:
			if c.hub != nil && c.hub.OnSeqViolation != nil {
				c.hub.OnSeqViolation(c.NodeID, f.Seq, want)
			}
			c.lastSeq = f.Seq
		default:
			c.lastSeq = f.Seq
		}
	}
	return f, nil
}


func (c *Conn) LastSeq() int64 { return c.lastSeq }


func (c *Conn) SetLastSeq(seq int64) { c.lastSeq = seq }


func (c *Conn) Close(code int, reason string) {
	c.closeOne.Do(func() {
		close(c.closed)
		if c.hub != nil {
			c.hub.Unregister(c)
		}


		if len(reason) > 120 {
			reason = reason[:120]
		}
		_ = c.ws.Close(websocket.StatusCode(code), reason)
	})
}


func (c *Conn) Done() <-chan struct{} { return c.closed }
