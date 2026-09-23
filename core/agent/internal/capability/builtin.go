// SPDX-License-Identifier: Apache-2.0

package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
)






func RegisterBuiltins(r *Registry, started time.Time) {
	r.Register(Capability{
		Name:        "sys.echo",
		Version:     "1.0.0",
		Description: "Return the supplied message. Proves the full request path end to end.",
		Risk:        RiskLow,
		Inputs: json.RawMessage(`{
			"type": "object",
			"properties": {"message": {"type": "string"}},
			"required": ["message"]
		}`),
		Outputs: json.RawMessage(`{
			"type": "object",
			"properties": {"message": {"type": "string"}}
		}`),
		AllowFrom:  []string{"local"},
		TimeoutSec: 10,
	}, echoHandler)

	r.Register(Capability{
		Name:        "sys.hostinfo",
		Version:     "1.0.0",
		Description: "Report hostname, platform, uptime, and free disk on the data volume.",
		Risk:        RiskLow,
		Inputs:      json.RawMessage(`{"type": "object"}`),
		Outputs: json.RawMessage(`{
			"type": "object",
			"properties": {
				"hostname":   {"type": "string"},
				"platform":   {"type": "string"},
				"arch":       {"type": "string"},
				"uptime_sec": {"type": "integer"},
				"disk_free_bytes": {"type": "integer"}
			}
		}`),
		AllowFrom:  []string{"local"},
		TimeoutSec: 10,
	}, hostInfoHandler(started))
}

func echoHandler(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Message string `json:"message"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInputInvalid, err)
		}
	}
	if in.Message == "" {



		return nil, fmt.Errorf("%w: message is required", ErrInputInvalid)
	}
	return json.Marshal(map[string]string{"message": in.Message})
}

func hostInfoHandler(started time.Time) Handler {
	return func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		hostname, err := os.Hostname()
		if err != nil {
			hostname = "unknown"
		}
		out := map[string]any{
			"hostname":   hostname,
			"platform":   runtime.GOOS,
			"arch":       runtime.GOARCH,
			"uptime_sec": int64(time.Since(started).Seconds()),
		}
		if free, err := diskFree("."); err == nil {
			out["disk_free_bytes"] = free
		}



		return json.Marshal(out)
	}
}

func diskFree(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	if stat.Bsize < 0 {
		return 0, errors.New("unexpected block size")
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
