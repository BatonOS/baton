// SPDX-License-Identifier: Apache-2.0

package agent










import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/batonos/baton/core/agent/internal/inbox"
)











func (a *Agent) uploadAttachments(ctx context.Context, paths []string) ([]inbox.Attachment, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(a.cfg.MasterURL, "/")

	out := make([]inbox.Attachment, 0, len(paths))
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {



			return nil, fmt.Errorf("attachment %s: %w", p, err)
		}
		st, serr := f.Stat()
		if serr != nil {
			f.Close()
			return nil, fmt.Errorf("attachment %s: %w", p, serr)
		}

		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost,
			base+"/api/v1alpha1/agent/messages/attachments", f)
		if rerr != nil {
			f.Close()
			return nil, rerr
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = st.Size()

		resp, derr := client.Do(req)
		f.Close()
		if derr != nil {
			return nil, fmt.Errorf("attachment %s: %w", filepath.Base(p), derr)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {



			return nil, fmt.Errorf("attachment %s: %s", filepath.Base(p), strings.TrimSpace(string(body)))
		}
		var stored struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		}
		if jerr := json.Unmarshal(body, &stored); jerr != nil || stored.SHA256 == "" {
			return nil, fmt.Errorf("attachment %s: the control plane answered without a digest", filepath.Base(p))
		}

		out = append(out, inbox.Attachment{



			Name:        filepath.Base(p),
			ContentType: guessContentType(p),
			Size:        stored.Size,
			SHA256:      stored.SHA256,
		})
	}
	return out, nil
}







func guessContentType(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
