// SPDX-License-Identifier: Apache-2.0

package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/batonos/baton/core/agent/internal/nodeface"
)






type Assigned struct {
	SkillID string `json:"skill_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Via     string `json:"via"`






	CodeID string `json:"code_id,omitempty"`
}








type Syncer struct {



	MountPath string








	StateDir  string
	MasterURL string
	Client    *http.Client
	Logger    *slog.Logger
}



var ErrNoMountPath = errors.New("skills: this runtime declares no adapter.skills.mountPath")





func (s *Syncer) Sync(ctx context.Context) (int, error) {
	assigned, err := s.fetchList(ctx)
	if err != nil {
		return 0, err
	}
	if len(assigned) > 0 && s.MountPath == "" {



		names := make([]string, 0, len(assigned))
		for _, a := range assigned {
			names = append(names, a.Name)
		}
		return 0, fmt.Errorf("%w: cannot place %s", ErrNoMountPath, strings.Join(names, ", "))
	}
	if err := os.MkdirAll(s.StateDir, 0o700); err != nil {
		return 0, err
	}

	changed := 0
	keep := map[string]bool{}
	for _, a := range assigned {
		keep[a.SkillID] = true
		have := s.recorded(a.SkillID)
		if have.Digest == a.SHA256 {













			if have.CodeID == a.CodeID {
				continue
			}
			if err := s.restamp(a); err != nil {
				s.log().Warn("could not write this skill's code", "skill", a.Name, "error", err)
				continue
			}
			changed++
			continue
		}
		if err := s.install(ctx, a); err != nil {



			s.log().Error("install skill", "skill", a.Name, "error", err)
			continue
		}
		changed++
	}

	changed += s.removeUnassigned(keep)
	return changed, nil
}

func (s *Syncer) fetchList(ctx context.Context) ([]Assigned, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.MasterURL+nodeface.APIBase+"/agent/skills", nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("skills: control plane answered %s", resp.Status)
	}
	var body struct {
		Items []Assigned `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Items, nil
}








func (s *Syncer) install(ctx context.Context, a Assigned) error {
	dir, err := s.dirFor(a.Name)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s%s/agent/skills/%s/archive", s.MasterURL, nodeface.APIBase, a.SkillID), nil)
	if err != nil {
		return err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("skills: fetching %s: %s", a.Name, resp.Status)
	}
	archive, err := io.ReadAll(io.LimitReader(resp.Body, MaxTotalBytes+1))
	if err != nil {
		return err
	}
	if int64(len(archive)) > MaxTotalBytes {
		return fmt.Errorf("%w: %s", ErrTooLarge, a.Name)
	}
	if err := VerifyHash(archive, a.SHA256); err != nil {
		return err
	}




	staging := dir + ".incoming"
	_ = os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	n, err := Extract(archive, staging)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.Rename(staging, dir); err != nil {
		return err
	}








	if a.CodeID != "" {
		if err := s.stampCode(dir, a.CodeID); err != nil {
			s.log().Warn("skill unpacked but its code could not be written",
				"skill", a.Name, "error", err)
		}
	}

	if err := s.record(a.SkillID, state{Digest: a.SHA256, Name: a.Name, CodeID: a.CodeID}); err != nil {
		return err
	}
	s.log().Info("skill installed", "skill", a.Name, "version", a.Version,
		"files", n, "via", a.Via, "path", dir)
	return nil
}








func (s *Syncer) removeUnassigned(keep map[string]bool) int {
	entries, err := os.ReadDir(s.StateDir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		id := e.Name()
		if keep[id] {
			continue
		}
		name := s.recorded(id).Name
		if name != "" {
			if dir, err := s.dirFor(name); err == nil {
				if err := os.RemoveAll(dir); err != nil {
					s.log().Error("remove skill", "skill", name, "error", err)
					continue
				}
				s.log().Info("skill removed", "skill", name, "path", dir)
			}
		}
		_ = os.Remove(filepath.Join(s.StateDir, id))
		removed++
	}
	return removed
}







func (s *Syncer) dirFor(name string) (string, error) {
	root, err := filepath.Abs(s.MountPath)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, filepath.FromSlash(name))
	if dir == root || !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: skill name %q", ErrEscapes, name)
	}
	return dir, nil
}








type state struct {
	Digest string `json:"digest"`
	Name   string `json:"name"`




	CodeID string `json:"code_id"`
}



































func (s *Syncer) recorded(skillID string) state {
	b, err := os.ReadFile(filepath.Join(s.StateDir, skillID))
	if err != nil {
		return state{}
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return state{}
	}
	if st.Name != "" && st.Digest != "" {
		dir, derr := s.dirFor(st.Name)
		if derr != nil {



			st.Digest = ""
			return st
		}
		if _, serr := os.Stat(dir); serr != nil {
			st.Digest = ""
		}
	}
	return st
}

func (s *Syncer) record(skillID string, st state) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.StateDir, skillID), b, 0o600)
}

func (s *Syncer) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}








func (s *Syncer) restamp(a Assigned) error {
	dir, err := s.dirFor(a.Name)
	if err != nil {
		return err
	}
	if a.CodeID == "" {
		path := filepath.Join(dir, "SKILL.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(StripCode(string(raw))), 0o644); err != nil {
			return err
		}
	} else if err := s.stampCode(dir, a.CodeID); err != nil {
		return err
	}
	return s.record(a.SkillID, state{Digest: a.SHA256, Name: a.Name, CodeID: a.CodeID})
}










func (s *Syncer) stampCode(dir, codeID string) error {
	path := filepath.Join(dir, "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("no SKILL.md to stamp: %w", err)
	}
	out, err := InjectCode(raw, codeID)
	if err != nil {
		return err
	}


	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}



	after, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if ContentDigest(after) != ContentDigest(raw) {
		return fmt.Errorf("stamping moved content_digest (%s -> %s)",
			ContentDigest(raw), ContentDigest(after))
	}
	return nil
}

