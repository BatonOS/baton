// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)







var consoleCommands = map[string]struct {
	MinRole  console.Role
	Mutating bool
	Summary  string
}{
	"status":       {console.RoleViewer, false, "state machine, inflight calls, certificate expiry"},
	"capabilities": {console.RoleViewer, false, "what this node offers and whether it is enabled"},
	"tasks":        {console.RoleViewer, false, "inflight and recent invocations"},



	"skills.show": {console.RoleViewer, false, "skill: what this node's copy of a package says right now"},



	"pause":   {console.RoleOperator, true, "node: refuse new capability calls; the runtime keeps working"},
	"resume":  {console.RoleOperator, true, "node: accept capability calls again"},
	"restart": {console.RoleOperator, true, "node: reconnect the control-plane channel; neither the container nor the runtime restarts"},









	"runtime.pause":   {console.RoleOperator, true, "runtime: SIGSTOP the runtime; it stops executing and holds its state"},
	"runtime.resume":  {console.RoleOperator, true, "runtime: SIGCONT a stopped runtime"},
	"runtime.restart": {console.RoleOperator, true, "runtime: stop the runtime and let the supervisor start it again"},
}


const consoleTimeout = 10 * time.Second

type consoleRequest struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args"`
	Reason  string         `json:"reason"`
}







func (a *API) handleConsole(w http.ResponseWriter, r *http.Request) {
	p, ok := principalFrom(r)
	if !ok {
		a.fail(w, r, http.StatusUnauthorized, CodeUnauthorized,
			"no client certificate was presented",
			"Use the admin certificate written by `baton setup master` (or `baton agent create`, which founds one).", nil)
		return
	}

	var req consoleRequest
	if !a.decodeJSON(w, r, &req) {
		return
	}

	spec, known := consoleCommands[req.Command]
	if !known {
		a.fail(w, r, http.StatusUnprocessableEntity, CodeInvalidRequest,
			"console command "+req.Command+" does not exist",
			"Available: status, capabilities, tasks, pause, resume, restart. "+
				"The console verb set is closed by design; for anything else use `baton shell`.",
			map[string]any{"available": consoleCommandNames()})
		return
	}
	if !p.Role.Satisfies(spec.MinRole) {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"this credential may not run "+req.Command,
			"This command needs the "+string(spec.MinRole)+" role.",
			map[string]any{"have": string(p.Role), "need": string(spec.MinRole)})
		return
	}

	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	if spec.Mutating && a.Store.ReadOnly() {
		a.fail(w, r, http.StatusForbidden, CodeReadOnlyMirror,
			"this control plane is a read-only mirror",
			"Run management commands against the primary.", nil)
		return
	}

	ctx := r.Context()
	receipt, err := a.Log.Append(ctx, audit.Record{
		Event:     "console." + req.Command,
		Actor:     p.Subject,
		ActorType: "user",
		Action:    req.Command,
		Target:    node.NodeID,
		Result:    "pending",
		SourceIP:  clientIP(r),
		NodeID:    node.NodeID,
		RequestID: requestIDFrom(r),
		At:        time.Now(),
		Detail: map[string]any{
			"reason": req.Reason, "mutating": spec.Mutating,
			"display_name": node.DisplayName,
		},
	})
	if err != nil {
		a.failInternal(w, r, err)
		return
	}

	started := time.Now()
	result, output, data, err := a.runConsole(ctx, node, req, requestIDFrom(r))
	duration := time.Since(started)



	a.Log.System(ctx, "console.result", map[string]any{
		"node_id": node.NodeID, "command": req.Command, "result": result,
		"audit_event_id": receipt.EventID, "duration_ms": duration.Milliseconds(),
	})

	if err != nil && result == "error" {
		a.fail(w, r, http.StatusServiceUnavailable, CodeNodeOffline, err.Error(),
			"Check the node with `baton status "+node.DisplayName+"`.",
			map[string]any{"audit_event_id": receipt.EventID})
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"result":         result,
		"output":         output,
		"data":           data,
		"audit_event_id": receipt.EventID,
		"duration_ms":    duration.Milliseconds(),
	})
}

func (a *API) runConsole(ctx context.Context, node *spi.Node, req consoleRequest, requestID string) (
	result, output string, data map[string]any, err error) {
	conn, live := a.Hub.Get(node.NodeID)
	if !live {
		return "error", "", nil, errors.New("node " + node.DisplayName +
			" is enrolled but has no live control channel")
	}

	reply, err := conn.Request(ctx, channel.TypeConsoleRequest, channel.ConsoleRequest{
		RequestID: requestID,
		Command:   req.Command,
		Args:      req.Args,
	}, consoleTimeout)
	if errors.Is(err, channel.ErrTimeout) {


		return "timeout", "", nil, nil
	}
	if err != nil {
		return "error", "", nil, err
	}

	var resp channel.ConsoleResponse
	if err := reply.Decode(&resp); err != nil {
		return "error", "", nil, err
	}
	return resp.Result, resp.Output, resp.Data, nil
}

func consoleCommandNames() []string {
	out := make([]string, 0, len(consoleCommands))
	for name := range consoleCommands {
		out = append(out, name)
	}
	return out
}
