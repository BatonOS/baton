// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/control-api/internal/skills"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


const (
	CodeSkillDigestMismatch = "SKILL_DIGEST_MISMATCH"
	CodeSkillFetchFailed    = "SKILL_FETCH_FAILED"
	CodeSkillTargetInvalid  = "SKILL_TARGET_INVALID"





	CodeSkillNoMountPath = "SKILL_NO_MOUNT_PATH"
)

type skillView struct {
	SkillID string `json:"skill_id"`
	Name    string `json:"name"`
	Version string `json:"version"`



	SHA256    string `json:"sha256"`
	SourceURL string `json:"source_url"`
	SizeBytes int64  `json:"size_bytes"`
	CreatedAt string `json:"created_at"`




	Cached bool `json:"cached"`

	Installs []skillInstallView `json:"installs"`
}

type skillInstallView struct {
	TargetKind string `json:"target_kind"`
	Target     string `json:"target"`
	CreatedAt  string `json:"created_at"`
}

func (a *API) toSkillView(s spi.Skill, installs []spi.SkillInstall) skillView {
	v := skillView{
		SkillID:   s.SkillID,
		Name:      s.Name,
		Version:   s.Version,
		SHA256:    s.SHA256,
		SourceURL: s.SourceURL,
		SizeBytes: s.SizeBytes,
		CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339),
		Cached:    a.SkillCache != nil && a.SkillCache.Has(s.SHA256),
		Installs:  []skillInstallView{},
	}
	for _, in := range installs {
		v.Installs = append(v.Installs, skillInstallView{
			TargetKind: in.TargetKind,
			Target:     in.Target,
			CreatedAt:  in.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return v
}







func (a *API) handleSkillCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	var body struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		SourceURL string `json:"source_url"`
		SHA256    string `json:"sha256"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || body.SourceURL == "" || body.SHA256 == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"a skill needs a name, a source_url and a sha256",
			"The digest is not optional: it is the only check on what the source serves.", nil)
		return
	}
	if body.Version == "" {
		body.Version = "0.0.0"
	}

	size, err := a.SkillCache.Fetch(r.Context(), body.SourceURL, body.SHA256)
	switch {
	case errors.Is(err, skills.ErrDigestMismatch):
		a.fail(w, r, http.StatusBadRequest, CodeSkillDigestMismatch,
			"the source served bytes that do not match the declared digest",
			"Either the download was corrupted or the source is serving something else. "+
				"Check the digest against the directory before retrying.",
			map[string]any{"source_url": body.SourceURL, "declared": body.SHA256})
		return
	case errors.Is(err, skills.ErrDigestMalformed):
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"the digest is not sha256:<64 hex characters>",
			"Copy it from the directory listing exactly, including the sha256: prefix.", nil)
		return
	case err != nil:
		a.fail(w, r, http.StatusBadGateway, CodeSkillFetchFailed,
			"the archive could not be fetched",
			"The control plane fetches and checks skills so nodes do not have to: a node downloads them only "+
				"from the control plane, never from the source. "+
				"Check that this host can reach the source.",
			map[string]any{"source_url": body.SourceURL, "error": err.Error()})
		return
	}

	created, err := a.Store.Skills().Create(r.Context(), spi.Skill{
		TenantID:  p.Identity.TenantID,
		Name:      body.Name,
		Version:   body.Version,
		SHA256:    body.SHA256,
		SourceURL: body.SourceURL,
		SizeBytes: size,
	})
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.auditSkill(r, p.Subject, "skill.created", "create", created.SkillID)
	a.writeJSON(w, http.StatusCreated, a.toSkillView(*created, nil))
}

func (a *API) handleSkillList(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleViewer)
	if !ok {
		return
	}
	list, err := a.Store.Skills().List(r.Context(), p.Identity.TenantID)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	out := make([]skillView, 0, len(list))
	for _, s := range list {
		installs, err := a.Store.Skills().Installs(r.Context(), p.Identity.TenantID, s.SkillID)
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		out = append(out, a.toSkillView(s, installs))
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion": "baton.mailloop.dev/v1alpha1",
		"kind":       "SkillList",
		"items":      out,



		"next_cursor": "",
	})
}

func (a *API) handleSkillDelete(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	id := r.PathValue("skill_id")




	affected := a.nodesAffectedBySkill(r, p.Identity.TenantID, id)

	if err := a.Store.Skills().Delete(r.Context(), p.Identity.TenantID, id); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.auditSkill(r, p.Subject, "skill.deleted", "delete", id)
	a.nudgeSkills(r, affected)
	w.WriteHeader(http.StatusNoContent)
}


func (a *API) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	id := r.PathValue("skill_id")
	var body struct {
		TargetKind string `json:"target_kind"`
		Target     string `json:"target"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if body.TargetKind != spi.SkillTargetNode && body.TargetKind != spi.SkillTargetTemplate {
		a.fail(w, r, http.StatusBadRequest, CodeSkillTargetInvalid,
			"target_kind must be node or template",
			"A node install names one workspace; a template install names a scene "+
				"and reaches nodes created later.", nil)
		return
	}
	if strings.TrimSpace(body.Target) == "" {
		a.fail(w, r, http.StatusBadRequest, CodeSkillTargetInvalid,
			"target is empty",
			"Name the node or the template. An empty template would collect every "+
				"node whose spec declared no scene.", nil)
		return
	}
	if _, err := a.Store.Skills().Get(r.Context(), p.Identity.TenantID, id); err != nil {
		a.failStore(w, r, err)
		return
	}















	if body.TargetKind == spi.SkillTargetNode {
		node, err := a.resolveNode(r, body.Target)
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		body.Target = node.NodeID

























		if node.Runtime.Name != "" && node.Runtime.SkillsMountPath == "" {
			a.fail(w, r, http.StatusConflict, CodeSkillNoMountPath,
				"node "+node.DisplayName+" declares no adapter.skills.mountPath, so it has nowhere to put a skill",
				"Its Node Template has to say where skills go, for example:\n"+
					"  adapter:\n    skills:\n      mountPath: /workspace/.skills\n"+
					"BATON does not pick one, because another runtime reads another path. "+
					"Nothing was installed.", nil)
			return
		}
	}

	in, err := a.Store.Skills().Install(r.Context(), spi.SkillInstall{
		TenantID: p.Identity.TenantID, SkillID: id,
		TargetKind: body.TargetKind, Target: body.Target,
	})
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.auditSkill(r, p.Subject, "skill.installed", "install", id+" -> "+in.TargetKind+":"+in.Target)
	a.nudgeSkills(r, a.nodesAffectedBySkill(r, p.Identity.TenantID, id))

	a.writeJSON(w, http.StatusCreated, skillInstallView{
		TargetKind: in.TargetKind,
		Target:     in.Target,
		CreatedAt:  in.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (a *API) handleSkillUninstall(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	id := r.PathValue("skill_id")
	var body struct {
		TargetKind string `json:"target_kind"`
		Target     string `json:"target"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	affected := a.nodesAffectedBySkill(r, p.Identity.TenantID, id)

	if err := a.Store.Skills().Uninstall(r.Context(), p.Identity.TenantID, id,
		body.TargetKind, body.Target); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.auditSkill(r, p.Subject, "skill.uninstalled", "uninstall", id+" -> "+body.TargetKind+":"+body.Target)
	a.nudgeSkills(r, affected)
	w.WriteHeader(http.StatusNoContent)
}




func (a *API) auditSkill(r *http.Request, actor, event, action, target string) {
	if a.Log == nil {
		return
	}
	_, _ = a.Log.Append(r.Context(), audit.Record{
		Event: event, Actor: actor, ActorType: "user",
		Action: action, Target: target, Result: "ok",
		SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
	})
}








func (a *API) nodesAffectedBySkill(r *http.Request, tenantID, skillID string) []string {
	if a.Hub == nil {
		return nil
	}
	installs, err := a.Store.Skills().Installs(r.Context(), tenantID, skillID)
	if err != nil {
		return nil
	}
	nodes := map[string]bool{}
	for _, in := range installs {
		switch in.TargetKind {
		case spi.SkillTargetNode:
			nodes[in.Target] = true
		case spi.SkillTargetTemplate:
			list, _, err := a.Store.Nodes().List(r.Context(), spi.NodeFilter{TenantID: tenantID})
			if err != nil {
				continue
			}
			for _, n := range list {
				if n.Runtime.Template == in.Target {
					nodes[n.NodeID] = true
				}
			}
		}
	}
	out := make([]string, 0, len(nodes))
	for id := range nodes {
		out = append(out, id)
	}
	return out
}

func (a *API) nudgeSkills(r *http.Request, nodeIDs []string) {
	if a.Hub == nil {
		return
	}
	for _, id := range nodeIDs {
		conn, ok := a.Hub.Get(id)
		if !ok {
			continue
		}
		f, err := channel.NewFrame(channel.TypeChanged, "", channel.Changed{Kind: "skills"})
		if err != nil {
			continue
		}
		_ = conn.Send(r.Context(), f)
	}
}



type nodeSkillView struct {
	SkillID string `json:"skill_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`



	Via string `json:"via"`















	CodeID string `json:"code_id,omitempty"`
}





func (a *API) handleAgentSkillList(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}
	list, err := a.Store.Skills().ForNode(r.Context(), node.TenantID, node.NodeID, node.Runtime.Template)
	if err != nil {
		a.failStore(w, r, err)
		return
	}






	codes, err := a.Store.Skills().CodesForNode(r.Context(), node.NodeID)
	if err != nil {



		a.Logger.Warn("skill codes for node", "node_id", node.NodeID, "error", err)
		codes = map[string]string{}
	}
	out := make([]nodeSkillView, 0, len(list))
	for _, s := range list {
		out = append(out, nodeSkillView{
			SkillID: s.SkillID, Name: s.Name, Version: s.Version,
			SHA256: s.SHA256, Via: s.Via, CodeID: codes[s.SkillID],
		})
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion":  "baton.mailloop.dev/v1alpha1",
		"kind":        "NodeSkillList",
		"items":       out,
		"next_cursor": "",
	})
}











func (a *API) handleSkillNodeCode(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireRole(w, r, console.RoleOperator)
	if !ok {
		return
	}
	skillID := r.PathValue("skill_id")
	node, err := a.resolveNode(r, r.PathValue("node_id"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if _, err := a.Store.Skills().Get(r.Context(), p.Identity.TenantID, skillID); err != nil {
		a.failStore(w, r, err)
		return
	}

	if r.Method == http.MethodGet {
		codes, err := a.Store.Skills().CodesForNode(r.Context(), node.NodeID)
		if err != nil {
			a.failStore(w, r, err)
			return
		}











		a.writeJSON(w, http.StatusOK, map[string]any{
			"node_id":  node.NodeID,
			"skill_id": skillID,
			"code_id":  codes[skillID],
		})
		return
	}

	var body struct {
		CodeID string `json:"code_id"`
	}
	if !a.decodeJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.CodeID) == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"code_id is empty",
			"Recording an empty code would leave a row that reads as \"a code was issued\" while "+
				"naming none. A node with no code is the `no_code` state, which is the absence of "+
				"a row, not a row holding nothing.", nil)
		return
	}
	if err := a.Store.Skills().SetCode(r.Context(), node.NodeID, skillID, strings.TrimSpace(body.CodeID)); err != nil {
		a.failStore(w, r, err)
		return
	}
	a.auditSkill(r, p.Subject, "skill.code_issued", "code", skillID+" -> node:"+node.NodeID)





	a.nudgeSkills(r, []string{node.NodeID})
	w.WriteHeader(http.StatusNoContent)
}








func (a *API) handleAgentSkillArchive(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}
	id := r.PathValue("skill_id")

	assigned, err := a.Store.Skills().ForNode(r.Context(), node.TenantID, node.NodeID, node.Runtime.Template)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	var want *spi.InstalledSkill
	for i := range assigned {
		if assigned[i].SkillID == id {
			want = &assigned[i]
			break
		}
	}
	if want == nil {


		a.fail(w, r, http.StatusNotFound, CodeNotFound,
			"no such skill for this node",
			"A node can fetch only the skills installed on it or on its template.", nil)
		return
	}

	f, err := a.SkillCache.Open(want.SHA256)
	if errors.Is(err, skills.ErrNotCached) {



		if _, ferr := a.SkillCache.Fetch(r.Context(), want.SourceURL, want.SHA256); ferr != nil {
			a.fail(w, r, http.StatusBadGateway, CodeSkillFetchFailed,
				"the archive is not cached here and could not be re-fetched",
				"The cache is rebuildable from source_url + sha256. If the source is gone, "+
					"re-ingest the package with `baton skills add`.",
				map[string]any{"skill_id": id, "source_url": want.SourceURL})
			return
		}
		f, err = a.SkillCache.Open(want.SHA256)
	}
	if err != nil {
		a.fail(w, r, http.StatusInternalServerError, CodeInternal,
			"the cached archive could not be read", "", nil)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/zip")



	w.Header().Set("X-Baton-Skill-Digest", want.SHA256)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}


func (a *API) requireNode(w http.ResponseWriter, r *http.Request) (*spi.Node, bool) {
	p, ok := principalFrom(r)
	if !ok || !p.IsNode() {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"this endpoint requires a node certificate",
			"Nodes fetch their own skills; operators use the admin face.", nil)
		return nil, false
	}
	node, err := a.Store.Nodes().Get(r.Context(), p.Identity.Name)
	if err != nil {
		a.failStore(w, r, err)
		return nil, false
	}
	if node.Status == spi.NodeStatusRevoked {
		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"this node has been revoked", "Re-enroll with a fresh token.", nil)
		return nil, false
	}









	if node.SuspendedAt != nil {
		a.fail(w, r, http.StatusForbidden, CodeNodeSuspended,
			"this node is suspended", "An operator paused it; ask them to resume it.", nil)
		return nil, false
	}
	return node, true
}
