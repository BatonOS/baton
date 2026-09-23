// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/batonos/baton/core/control-api/internal/skills"
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)










func resourceEnvelope(r spi.Resource, withBody bool) map[string]any {
	strOrNull := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	rawOrNull := func(s string) any {
		if s == "" {
			return nil
		}
		return json.RawMessage(s)
	}

	detail := map[string]any{}
	if r.Detail != "" {
		_ = json.Unmarshal([]byte(r.Detail), &detail)
	}



	delete(detail, "tags")
	if withBody && r.Body != "" {
		switch r.Type {
		case "template":
			detail["yaml"] = r.Body
		default:
			detail["body"] = r.Body
		}
	}












	tags := []any{}
	if r.Tags != "" {
		_ = json.Unmarshal([]byte(r.Tags), &tags)
	}
	return map[string]any{
		"kind":        "Resource",
		"resource_id": r.ResourceID,
		"network_id":  strOrNull(r.NetworkID),
		"type":        r.Type,
		"name":        r.Name,
		"version":     r.Version,
		"folder":      r.Folder,
		"visibility":  r.Visibility,
		"publisher":   strOrNull(r.Publisher),
		"hash":        strOrNull(r.Hash),
		"source":      strOrNull(r.Source),
		"signature":   strOrNull(r.Signature),




		"origin":      rawOrNull(r.Origin),
		"license":     strOrNull(r.License),
		"compatibility":        rawOrNull(r.Compatibility),
		"permissions_required": rawOrNull(r.PermissionsRequired),
		"tags":                 tags,
		"detail":               detail,
		"created_at":           r.CreatedAt,
		"updated_at":           r.UpdatedAt,

		"review":               r.Review,
		"reviewed_by":          r.ReviewedBy,
		"reviewed_at":          r.ReviewedAt,
		"review_reason":        r.ReviewReason,
		"error":                nil,
	}
}

func resourceListEnvelope(rs []spi.Resource) map[string]any {
	items := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		items = append(items, resourceEnvelope(r, false))
	}
	return map[string]any{"kind": "ResourceList", "items": items}
}



type resourcePublishBody struct {
	Type                string          `json:"type"`
	Name                string          `json:"name"`
	Version             string          `json:"version"`
	Folder              string          `json:"folder"`
	Visibility          string          `json:"visibility"`
	Hash                string          `json:"hash"`
	Source              string          `json:"source"`




	Origin              json.RawMessage `json:"origin"`
	License             string          `json:"license"`
	Body                string          `json:"body"`
	Detail              json.RawMessage `json:"detail"`
	Compatibility       json.RawMessage `json:"compatibility"`
	PermissionsRequired json.RawMessage `json:"permissions_required"`
	Tags                json.RawMessage `json:"tags"`
}

















var resourceTypes = map[string]bool{
	"skill": true, "knowledge": true, "policy": true,
	"workflow": true, "template": true, "other": true,
	"plugin": true,
}






var resourceVisibilities = map[string]bool{
	"private": true, "network": true, "public": true,
}







var publicResourceTypes = map[string]bool{
	"template": true, "skill": true,
}



var typeFolders = map[string]string{
	"skill": "Skills/", "knowledge": "Knowledge/", "policy": "Policies/",
	"workflow": "Workflow/", "template": "Templates/", "other": "Other/",
}







func rootFolder(resType, folder string) (string, bool) {
	root := typeFolders[resType]
	folder = strings.TrimLeft(strings.TrimSpace(folder), "/")
	if folder == "" {
		return root, true
	}
	if strings.HasPrefix(folder, root) {
		return folder, true
	}
	for t, f := range typeFolders {
		if t != resType && strings.HasPrefix(folder, f) {
			return "", false
		}
	}
	return root + folder, true
}


func (a *API) handleResourcePublish(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.requireRole(w, r, console.RoleSuperAdmin)
	if !ok {
		return
	}
	var b resourcePublishBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		a.fail(w, r, http.StatusBadRequest, "INVALID_BODY", "could not read the resource", "Send a JSON body with at least type and name.", nil)
		return
	}
	if !resourceTypes[b.Type] {
		a.fail(w, r, http.StatusBadRequest, "INVALID_TYPE", "unknown resource type", "type is one of skill, knowledge, policy, workflow, template, other, plugin.", nil)
		return
	}
	if msg, rem, bad := refuseFacilityWithoutBytes(b.Type, b.Hash); bad {
		a.fail(w, r, http.StatusBadRequest, "INVALID_HASH", msg, rem, nil)
		return
	}
	if strings.TrimSpace(b.Name) == "" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_NAME", "a resource needs a name", "Give --name.", nil)
		return
	}
	vis := b.Visibility
	if vis == "" {
		vis = "network"
	}
	if !resourceVisibilities[vis] {
		a.fail(w, r, http.StatusBadRequest, "INVALID_VISIBILITY", "unknown visibility "+vis,
			"visibility is one of private, network, public.", nil)
		return
	}
	if vis == "public" && !publicResourceTypes[b.Type] {
		a.fail(w, r, http.StatusBadRequest, "INVALID_PUBLIC_TYPE",
			"only a template or a skill may be published to the public directory",
			"Public sharing is templates and skills only (operator 2026-08-20). Publish a "+b.Type+" with --scope network or --scope private.", nil)
		return
	}
	folder, ok := rootFolder(b.Type, b.Folder)
	if !ok {
		a.fail(w, r, http.StatusBadRequest, "INVALID_FOLDER",
			"a "+b.Type+" resource must live under "+typeFolders[b.Type],
			"Browsing by folder is browsing by type: each resource type has its own top-level folder, so a folder path must sit under its type's folder. Give a subpath under "+typeFolders[b.Type]+", or omit --folder.", nil)
		return
	}
	origin, oerr := validOrigin(b.Origin)
	if oerr != "" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_ORIGIN", oerr,
			"An origin says where a fetched copy came from: network, resource_id and fetched_at are all required; network_id and hash may be null. Omit it entirely for a resource written here.", nil)
		return
	}
	_, netID := a.networkNameAndID(r.Context(), spi.DefaultTenant)
	res := spi.Resource{
		Origin: origin,
		NetworkID: netID, Type: b.Type, Name: b.Name, Version: b.Version,
		Folder: folder, Visibility: vis,













		Publisher: principal.Subject, Hash: b.Hash,
		Source: b.Source, License: b.License, Body: b.Body,
		Detail:              string(b.Detail),
		Compatibility:       string(b.Compatibility),
		PermissionsRequired: string(b.PermissionsRequired),
		Tags:                string(b.Tags),
	}
	out, err := a.Store.Resources().Put(r.Context(), spi.DefaultTenant, res)
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, resourceEnvelope(*out, true))
}


func (a *API) handleResourceListOwner(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	rs, err := a.Store.Resources().List(r.Context(), spi.DefaultTenant,
		r.URL.Query().Get("type"), r.URL.Query().Get("visibility"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	env := resourceListEnvelope(rs)











	counts := map[string]int{}
	if byReview, err := a.Store.Resources().CountsByReview(r.Context(), spi.DefaultTenant); err == nil {
		counts = byReview
	}
	env["counts"] = counts
	a.writeJSON(w, http.StatusOK, env)
}


func (a *API) handleResourceGetOwner(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleViewer); !ok {
		return
	}
	res, err := a.Store.Resources().Get(r.Context(), spi.DefaultTenant, r.PathValue("id"), r.URL.Query().Get("type"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, resourceEnvelope(*res, true))
}


func (a *API) handleResourceDelete(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	if err := a.Store.Resources().Delete(r.Context(), spi.DefaultTenant, r.PathValue("id")); err != nil {
		a.failStore(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}


func (a *API) handleResourceMove(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	var b struct {
		Folder string `json:"folder"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		a.fail(w, r, http.StatusBadRequest, "INVALID_BODY", "could not read the move", "Send {\"folder\": \"…\"}.", nil)
		return
	}



	existing, err := a.Store.Resources().Get(r.Context(), spi.DefaultTenant, r.PathValue("id"), "")
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	folder, ok := rootFolder(existing.Type, b.Folder)
	if !ok {
		a.fail(w, r, http.StatusBadRequest, "INVALID_FOLDER",
			"a "+existing.Type+" resource must live under "+typeFolders[existing.Type],
			"A move re-folders within the type: a folder path only organises resources and must stay under the type's folder; identity is the resource_id. Give a subpath under "+typeFolders[existing.Type]+".", nil)
		return
	}
	if err := a.Store.Resources().Move(r.Context(), spi.DefaultTenant, r.PathValue("id"), folder); err != nil {
		a.failStore(w, r, err)
		return
	}
	res, err := a.Store.Resources().Get(r.Context(), spi.DefaultTenant, r.PathValue("id"), "")
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, resourceEnvelope(*res, false))
}





func (a *API) handleResourceTag(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	var b struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		a.fail(w, r, http.StatusBadRequest, "INVALID_BODY", "could not read the tags", "Send {\"tags\": [\"…\"]}.", nil)
		return
	}
	if b.Tags == nil {
		b.Tags = []string{}
	}
	raw, _ := json.Marshal(b.Tags)
	if err := a.Store.Resources().SetTags(r.Context(), spi.DefaultTenant, r.PathValue("id"), string(raw)); err != nil {
		a.failStore(w, r, err)
		return
	}
	res, err := a.Store.Resources().Get(r.Context(), spi.DefaultTenant, r.PathValue("id"), "")
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, resourceEnvelope(*res, false))
}












func (a *API) handlePublicResourceList(w http.ResponseWriter, r *http.Request) {
	rs, err := a.Store.Resources().List(r.Context(), spi.DefaultTenant, r.URL.Query().Get("type"), "public")
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	a.writeJSON(w, http.StatusOK, resourceListEnvelope(rs))
}


func (a *API) handlePublicResourceGet(w http.ResponseWriter, r *http.Request) {
	res, err := a.Store.Resources().Get(r.Context(), spi.DefaultTenant, r.PathValue("id"), r.URL.Query().Get("type"))
	if err != nil || res.Visibility != "public" {
		a.fail(w, r, http.StatusNotFound, "NOT_FOUND", "no such public resource", "Only resources published --scope public are served here.", nil)
		return
	}
	a.writeJSON(w, http.StatusOK, resourceEnvelope(*res, true))
}





func (a *API) handleAgentResourceList(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}
	rs, err := a.Store.Resources().List(r.Context(), node.TenantID, r.URL.Query().Get("type"), "")
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	shared := rs[:0]
	for _, res := range rs {
		if res.Visibility != "private" && memberMaySee(res) {
			shared = append(shared, res)
		}
	}
	a.writeJSON(w, http.StatusOK, resourceListEnvelope(shared))
}


func (a *API) handleAgentResourceGet(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}
	res, err := a.Store.Resources().Get(r.Context(), node.TenantID, r.PathValue("id"), r.URL.Query().Get("type"))
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	if res.Visibility == "private" || !memberMaySee(*res) {
		a.fail(w, r, http.StatusNotFound, "NOT_FOUND", "no such resource", "It may be private to the owner.", nil)
		return
	}
	a.writeJSON(w, http.StatusOK, resourceEnvelope(*res, true))
}











type agentPublishBody struct {
	Type       string `json:"type"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	Body       string `json:"body"`
	Version    string `json:"version"`
	Publisher  string `json:"publisher"`
}






func (a *API) handleAgentPublish(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}















	publisher := node.DisplayName

	var b agentPublishBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		a.fail(w, r, http.StatusBadRequest, "INVALID_BODY", "could not read the resource",
			"Send a JSON body with at least type, name, visibility and body.", nil)
		return
	}







	if strings.TrimSpace(b.Publisher) != "" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_BODY",
			"a node may not state a publisher",
			"Who published is taken from your certificate. Remove the field — it is refused "+
				"rather than ignored so you can tell it did not take effect.", nil)
		return
	}

	if !resourceTypes[b.Type] {
		a.fail(w, r, http.StatusBadRequest, "INVALID_TYPE", "unknown resource type",
			"type is one of skill, knowledge, policy, workflow, template, other, plugin.", nil)
		return
	}











	if b.Type == "plugin" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_TYPE",
			"a node does not publish facilities",
			"A facility is a directory tree, and this face has no way to carry its bytes — it publishes what "+
				"the occupant wrote. An operator publishes a plugin from the machine that holds it.", nil)
		return
	}
	if strings.TrimSpace(b.Name) == "" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_NAME", "a resource needs a name", "Give a name.", nil)
		return
	}
	if strings.TrimSpace(b.Body) == "" {
		a.fail(w, r, http.StatusBadRequest, "INVALID_BODY", "a published resource needs a body",
			"Stage 1 publishes text. An empty body would occupy the name and share a blank.", nil)
		return
	}








	switch b.Visibility {
	case "private", "network":
	case "":
		a.fail(w, r, http.StatusBadRequest, "INVALID_VISIBILITY",
			"say whether this is private or shared with the network",
			"There is no default on this face. `public` means one thing as a directory and "+
				"another as an audience, and a default in that overlap would make forgetting "+
				"look like a decision.", nil)
		return
	case "public":



		a.fail(w, r, http.StatusForbidden, CodeForbidden,
			"a node may not publish to the public directory",
			"Public release also registers with the directory, which happens operator-side. "+
				"Half of it reported as success is worse than a refusal. Ask an operator.", nil)
		return
	default:
		a.fail(w, r, http.StatusBadRequest, "INVALID_VISIBILITY", "unknown visibility "+b.Visibility,
			"On this face visibility is private or network.", nil)
		return
	}












	if existing, err := a.Store.Resources().Get(r.Context(), node.TenantID, b.Name, b.Type); err == nil &&
		existing != nil && existing.Publisher != publisher {
		a.fail(w, r, http.StatusConflict, CodeNameConflict,
			"the name "+b.Type+"/"+b.Name+" already belongs to "+existing.Publisher,
			"Publishing over it would replace their resource and take its authorship. "+
				"Choose another name.", nil)
		return
	}

	folder, ok := rootFolder(b.Type, "")
	if !ok {
		a.fail(w, r, http.StatusInternalServerError, "INVALID_FOLDER",
			"no root folder for "+b.Type, "This is a server-side gap, not something you sent.", nil)
		return
	}












	sum := sha256.Sum256([]byte(b.Body))
	hash := "sha256:" + hex.EncodeToString(sum[:])







	result := "created"
	unchanged := false
	var priorReview string
	if prior, err := a.Store.Resources().Get(r.Context(), node.TenantID, b.Name, b.Type); err == nil && prior != nil {
		priorReview = prior.Review
		result = "updated"
		if prior.Hash == hash && prior.Visibility == b.Visibility {
			result = "unchanged"
			unchanged = true
		}
	}



















	review := ""
	if b.Visibility == "network" {
		review = spi.ReviewPending
		if unchanged {
			review = priorReview
		}
	}








	if review == spi.ReviewPending {
		result = "submitted"
	}

	_, netID := a.networkNameAndID(r.Context(), node.TenantID)
	out, err := a.Store.Resources().Put(r.Context(), node.TenantID, spi.Resource{
		NetworkID: netID, Type: b.Type, Name: b.Name, Version: b.Version,
		Folder: folder, Visibility: b.Visibility,
		Publisher: publisher, Body: b.Body, Hash: hash, Review: review,
	})
	if err != nil {
		a.failStore(w, r, err)
		return
	}
	env := resourceEnvelope(*out, true)




	env["result"] = result
	a.writeJSON(w, http.StatusOK, env)
}





















func memberMaySee(r spi.Resource) bool {
	return r.Review != spi.ReviewPending && r.Review != spi.ReviewDenied
}













func validOrigin(raw json.RawMessage) (string, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	var o struct {
		Via        string  `json:"via"`
		Network    string  `json:"network"`
		NetworkID  *string `json:"network_id"`
		ResourceID string  `json:"resource_id"`
		Hash       *string `json:"hash"`
		FetchedAt  string  `json:"fetched_at"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return "", "origin is not an object"
	}










	switch o.Via {
	case "member":



		if o.NetworkID == nil || strings.TrimSpace(*o.NetworkID) == "" {
			return "", "origin says via=member but carries no network_id — a member read binds an identity, so a missing one is a lost record, not an anonymous fetch"
		}
	case "public":




		if o.NetworkID != nil {
			return "", "origin says via=public but carries a network_id — an anonymous read binds no identity, and a self-reported one must not be spelled like a bound one"
		}
	case "":
		return "", "origin needs via — whether the bytes came from a member read or an anonymous public one"
	default:
		return "", "origin's via is not a kind of read: " + o.Via
	}
	switch {
	case strings.TrimSpace(o.Network) == "":
		return "", "origin needs the network reference the fetch was given"
	case strings.TrimSpace(o.ResourceID) == "":
		return "", "origin needs resource_id — the id this resource has where it came from"
	case strings.TrimSpace(o.FetchedAt) == "":
		return "", "origin needs fetched_at — when the bytes were obtained"
	}



	if _, err := time.Parse(time.RFC3339, o.FetchedAt); err != nil {
		return "", "origin's fetched_at is not an RFC3339 timestamp: " + o.FetchedAt
	}
	return string(raw), ""
}



type resourceDecisionBody struct {
	Reason string `json:"reason"`
}












func (a *API) handleResourceDecide(state string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := a.requireRole(w, r, console.RoleSuperAdmin)
		if !ok {
			return
		}
		id := r.PathValue("id")
		var body resourceDecisionBody
		if r.ContentLength != 0 && !a.decodeJSON(w, r, &body) {
			return
		}




		before, err := a.Store.Resources().Get(r.Context(), spi.DefaultTenant, id, "")
		if err != nil && !errors.Is(err, spi.ErrNotFound) {
			a.failStore(w, r, err)
			return
		}

		switch err := a.Store.Resources().Decide(r.Context(), spi.DefaultTenant, id, state, p.Subject, body.Reason); {
		case errors.Is(err, spi.ErrNotFound):



			a.fail(w, r, http.StatusNotFound, CodeNotFound,
				"no open application with that id",
				"Either it was already decided, or it was deleted and re-published — a re-publish gets a NEW id, "+
					"and a verdict carrying the old one is refused rather than landing on bytes nobody read. "+
					"Re-read `GET /networks/self/resources?visibility=network` and decide the id it shows now.", nil)
			return
		case err != nil:
			a.failStore(w, r, err)
			return
		}

		verb := "admit"
		if state == spi.ReviewDenied {
			verb = "deny"
		}
		detail := map[string]any{"reason": body.Reason}
		if before != nil {
			detail["type"], detail["name"] = before.Type, before.Name
			detail["publisher"], detail["hash"] = before.Publisher, before.Hash
		}
		_, _ = a.Log.Append(r.Context(), audit.Record{
			Event: "resource." + verb, Actor: p.Subject, ActorType: "user",
			Action: verb, Target: id, Result: "ok",
			SourceIP: clientIP(r), RequestID: requestIDFrom(r), At: time.Now(),
			Detail: detail,
		})



		after, err := a.Store.Resources().Get(r.Context(), spi.DefaultTenant, id, "")
		if err != nil {
			a.failStore(w, r, err)
			return
		}
		a.writeJSON(w, http.StatusOK, resourceEnvelope(*after, true))
	}
}




























func (a *API) handleAgentResourceArchive(w http.ResponseWriter, r *http.Request) {
	node, ok := a.requireNode(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	res, err := a.Store.Resources().Get(r.Context(), node.TenantID, id, "")
	if err != nil {
		a.failStore(w, r, err)
		return
	}



	if res.Visibility == "private" || !memberMaySee(*res) {
		a.fail(w, r, http.StatusNotFound, "NOT_FOUND", "no such resource", "It may be private to the owner.", nil)
		return
	}
	if res.Hash == "" {
		a.fail(w, r, http.StatusNotFound, "NOT_FOUND",
			"this resource has no archive",
			"Only a resource published with bytes has one; a text resource carries its body in the record itself.",
			map[string]any{"resource_id": id, "type": res.Type})
		return
	}
	if a.SkillCache == nil {
		a.fail(w, r, http.StatusInternalServerError, CodeInternal,
			"this control plane has no archive store", "", nil)
		return
	}
	f, err := a.SkillCache.Open(res.Hash)
	if err != nil {




		a.fail(w, r, http.StatusNotFound, "NOT_FOUND",
			"the record is here and its archive is not",
			"A resource archive is not rebuildable from the record — the digest says what the bytes are, "+
				"and nothing says where they came from. Publish it again from the machine that has it.",
			map[string]any{"resource_id": id, "hash": res.Hash})
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/zip")



	w.Header().Set("X-Baton-SHA256", res.Hash)
	w.WriteHeader(http.StatusOK)




	_, _ = io.Copy(w, f)
}





















func refuseFacilityWithoutBytes(typ, hash string) (msg, remediation string, bad bool) {
	if typ != "plugin" || strings.TrimSpace(hash) != "" {
		return "", "", false
	}
	return "a facility is published with its bytes, and this record names none",
		"A `plugin` resource carries a directory tree, not a body: `hash` must be the sha256 of its archive, " +
			"and the archive must already be here. Publish it from the machine that has it.",
		true
}

















func (a *API) handleResourceArchiveUpload(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireRole(w, r, console.RoleSuperAdmin); !ok {
		return
	}
	if a.SkillCache == nil {
		a.fail(w, r, http.StatusInternalServerError, CodeInternal,
			"this control plane has no archive store", "", nil)
		return
	}
	digest := "sha256:" + r.PathValue("digest")
	n, err := a.SkillCache.Put(digest, r.Body)
	if err != nil {
		switch {
		case errors.Is(err, skills.ErrDigestMalformed):
			a.fail(w, r, http.StatusBadRequest, "INVALID_HASH",
				"that is not a sha256 digest",
				"The path is the digest the bytes must hash to: 64 lowercase hex characters.", nil)
		case errors.Is(err, skills.ErrDigestMismatch):



			a.fail(w, r, http.StatusBadRequest, "INVALID_HASH",
				"what arrived does not hash to the name it was sent under",
				err.Error(), nil)
		case errors.Is(err, skills.ErrTooLarge):
			a.fail(w, r, http.StatusRequestEntityTooLarge, "TOO_LARGE",
				"the archive is past this control plane's limit", err.Error(), nil)
		default:
			a.fail(w, r, http.StatusInternalServerError, CodeInternal,
				"the archive could not be stored", err.Error(), nil)
		}
		return
	}
	a.writeJSON(w, http.StatusOK, map[string]any{
		"kind": "ResourceArchive", "hash": digest, "size_bytes": n,
	})
}
