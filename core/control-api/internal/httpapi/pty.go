// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/pkg/spi/console"
)









const ptyLeaseTTL = 60 * time.Second








const (
	ptyPingInterval = 20 * time.Second
	ptyPingTimeout  = 10 * time.Second
)

func (a *API) handlePty(w http.ResponseWriter, r *http.Request) {

	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	node, err := a.resolveNode(r, r.URL.Query().Get("agent"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	conn, live := a.Hub.Get(node.NodeID)
	if !live {
		a.fail(w, r, http.StatusConflict, "NODE_OFFLINE", "the node is not connected", "It holds no channel right now; try when `baton status` shows it online.", nil)
		return
	}





	if node.RemoteShell == nil || !*node.RemoteShell {
		a.fail(w, r, http.StatusForbidden, "SHELL_NOT_PERMITTED", "the node does not allow a remote shell",
			"Its owner has not enabled it. `baton agent shell "+node.DisplayName+" on` turns it on.", nil)
		return
	}





















	if node.Runtime.State == "" {
		a.fail(w, r, http.StatusConflict, "NO_RUNTIME",
			"this node has not reported a runtime — it supervises none, or its first heartbeat has not arrived yet",
			"A node that just enrolled reports within its first heartbeat: retry shortly, and `baton node show` tells which reading is true. A node with no runtime: use `baton logs` for its output.", nil)
		return
	}
	if !node.Runtime.Enterable {
		a.fail(w, r, http.StatusConflict, "RUNTIME_NOT_ENTERABLE",
			"this runtime does not declare session: tty, so it has no terminal to enter",
			"Use `baton attach` to watch it, or `baton logs` for its output. "+
				"Set session: tty in the runtime spec if it should be enterable.", nil)
		return
	}

	takeover := r.URL.Query().Get("mode") == "takeover"
	session := "pty-" + uuid.NewString()



	writeAllowed := false
	if takeover {
		if _, err := a.Store.Locks().Acquire(r.Context(), node.NodeID, p.Subject, session, ptyLeaseTTL, false); err != nil {

			writeAllowed = false
		} else {
			writeAllowed = true
			defer a.Store.Locks().Release(context.Background(), node.NodeID, session)
		}
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols:       []string{"baton.pty.v1"},
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sub := conn.SubscribePty(session)
	defer conn.UnsubscribePty(session)


	if f, err := channel.NewFrame(channel.TypePtyOpen, "", channel.PtyOpen{Session: session, Cols: 80, Rows: 24}); err == nil {
		_ = conn.Send(ctx, f)
	}
	defer func() {
		if f, err := channel.NewFrame(channel.TypePtyClose, "", channel.PtyClose{Session: session, Reason: "operator left"}); err == nil {
			_ = conn.Send(context.Background(), f)
		}
	}()

	if takeover && !writeAllowed {
		writeControl(ctx, ws, map[string]any{"type": "error", "code": "HELD_BY_OTHER"})
	}


	if writeAllowed {
		go func() {
			t := time.NewTicker(ptyLeaseTTL / 2)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					_, _ = a.Store.Locks().Renew(ctx, node.NodeID, session, ptyLeaseTTL)
				}
			}
		}()
	}






	go func() {
		t := time.NewTicker(ptyPingInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				pctx, pcancel := context.WithTimeout(ctx, ptyPingTimeout)
				err := ws.Ping(pctx)
				pcancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()


	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case f, open := <-sub:
				if !open {
					return
				}
				switch f.Type {
				case channel.TypePtyData:
					var d channel.PtyData
					if f.Decode(&d) == nil {
						if raw, err := base64.StdEncoding.DecodeString(d.Data); err == nil {
							if ws.Write(ctx, websocket.MessageBinary, raw) != nil {
								return
							}
						}
					}
				case channel.TypePtyClose:
					writeControl(ctx, ws, map[string]any{"type": "close"})
					return
				}
			}
		}
	}()


	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if !writeAllowed {
				continue
			}
			if f, err := channel.NewFrame(channel.TypePtyData, "", channel.PtyData{Session: session, Data: base64.StdEncoding.EncodeToString(data)}); err == nil {
				_ = conn.Send(ctx, f)
			}
		} else {

			var ctl struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" && writeAllowed {
				if f, err := channel.NewFrame(channel.TypePtyResize, "", channel.PtyResize{Session: session, Cols: ctl.Cols, Rows: ctl.Rows}); err == nil {
					_ = conn.Send(ctx, f)
				}
			}
		}
	}
}

func writeControl(ctx context.Context, ws *websocket.Conn, v any) {
	if b, err := json.Marshal(v); err == nil {
		_ = ws.Write(ctx, websocket.MessageText, b)
	}
}
