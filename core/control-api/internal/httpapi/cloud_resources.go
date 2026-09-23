// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/batonos/baton/core/control-api/internal/skills"
)




















const cloudFetchTimeout = 2 * time.Minute








type cloudDownloadTicket struct {
	DownloadURL string `json:"download_url"`
	ResourceID  string `json:"resource_id"`
	Version     string `json:"version"`
	Hash        string `json:"hash"`
}











func (a *API) handleAgentCloudResource(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireNode(w, r); !ok {
		return
	}
	network, resource := r.PathValue("network"), r.PathValue("resource")
	if network == "" || resource == "" {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"a cloud facility is named by its publisher's network and its resource id", "", nil)
		return
	}








	base := registryBase()
	if base == "" {
		a.fail(w, r, http.StatusPreconditionFailed, "CLOUD_NOT_CONNECTED",
			"this control plane has no directory address configured (BATON_CLOUD_URL)",
			"This path is open whenever the control plane is configured with a directory address "+
				"(BATON_CLOUD_URL); `baton cloud connect` does not set it. "+
				"Until then a facility can only come from what this company itself publishes.",
			nil)
		return
	}
	if a.SkillCache == nil {
		a.fail(w, r, http.StatusInternalServerError, CodeInternal,
			"this control plane has no archive store", "", nil)
		return
	}

	ticket, code, err := a.cloudTicket(r, base, network, resource)
	if err != nil {
		a.fail(w, r, http.StatusBadGateway, "CLOUD_UNREACHABLE",
			"the open directory could not be asked",
			"Nothing about this facility is known from here — this is not the same as it being absent. "+
				err.Error(), nil)
		return
	}
	switch code {
	case http.StatusOK:
	case http.StatusNotFound:
		a.fail(w, r, http.StatusNotFound, "NO_SUCH_RESOURCE",
			"the open directory does not list this facility",
			"Check the publisher's network id and the resource id.",
			map[string]any{"network_id": network, "resource_id": resource})
		return
	case http.StatusGone:











		a.fail(w, r, http.StatusGone, "RESOURCE_WITHDRAWN",
			"this facility was withdrawn from the open directory",
			"It was published and then taken down. This is not the same as it never having existed: "+
				"do not go looking for another copy — whoever withdrew it decided it should not be installed.",
			map[string]any{"network_id": network, "resource_id": resource})
		return
	case http.StatusConflict:
		a.fail(w, r, http.StatusConflict, "ARTIFACT_MISSING",
			"the directory lists this facility and its archive is not in the store",
			"The entry exists; the bytes do not. Nothing here can produce them — the publisher publishes again.",
			map[string]any{"network_id": network, "resource_id": resource})
		return
	default:
		a.fail(w, r, http.StatusBadGateway, "CLOUD_REFUSED",
			fmt.Sprintf("the open directory answered %d", code),
			"Retry, or check the directory.", map[string]any{"status": code})
		return
	}

	if !strings.HasPrefix(ticket.Hash, "sha256:") || ticket.DownloadURL == "" {
		a.fail(w, r, http.StatusBadGateway, "CLOUD_REFUSED",
			"the directory's answer names no usable archive",
			"A ticket carries `download_url` and a `sha256:` hash.", nil)
		return
	}






	n, err := a.cloudArchive(r, ticket)
	switch {
	case errors.Is(err, skills.ErrDigestMismatch):
		a.fail(w, r, http.StatusBadGateway, "INVALID_HASH",
			"the archive does not hash to what the directory says it is",
			"Nothing was stored. The directory's record and the bytes in its store disagree — "+
				"that is a publisher-side fault and cannot be fixed from here.",
			map[string]any{"declared": ticket.Hash})
		return
	case errors.Is(err, skills.ErrTooLarge):
		a.fail(w, r, http.StatusBadGateway, "ARCHIVE_TOO_LARGE",
			"the archive is larger than this control plane accepts", "", nil)
		return
	case err != nil:
		a.fail(w, r, http.StatusBadGateway, "CLOUD_UNREACHABLE",
			"the archive could not be fetched", err.Error(), nil)
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]any{
		"resource_id": ticket.ResourceID,
		"network_id":  network,
		"version":     ticket.Version,




		"hash":  ticket.Hash,
		"bytes": n,
	})
}










func (a *API) cloudTicket(r *http.Request, base, network, resource string) (cloudDownloadTicket, int, error) {
	var t cloudDownloadTicket
	url := fmt.Sprintf("%s/api/discovery/resources/%s/%s/download", base, network, resource)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return t, 0, err
	}
	res, err := (&http.Client{Timeout: registryTimeout}).Do(req)
	if err != nil {
		return t, 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return t, res.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&t); err != nil {
		return t, res.StatusCode, err
	}
	return t, res.StatusCode, nil
}



func (a *API) cloudArchive(r *http.Request, t cloudDownloadTicket) (int64, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, t.DownloadURL, nil)
	if err != nil {
		return 0, err
	}
	res, err := (&http.Client{Timeout: cloudFetchTimeout}).Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("the archive store answered %d", res.StatusCode)
	}
	return a.SkillCache.Put(t.Hash, res.Body)
}














func (a *API) handleAgentArchiveByDigest(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireNode(w, r); !ok {
		return
	}
	digest := r.PathValue("digest")
	if !strings.HasPrefix(digest, "sha256:") {
		a.fail(w, r, http.StatusBadRequest, CodeInvalidRequest,
			"an archive is addressed by its sha256", "Use `sha256:<64 hex characters>`.", nil)
		return
	}
	if a.SkillCache == nil {
		a.fail(w, r, http.StatusInternalServerError, CodeInternal,
			"this control plane has no archive store", "", nil)
		return
	}
	f, err := a.SkillCache.Open(digest)
	if err != nil {
		a.fail(w, r, http.StatusNotFound, "NOT_FOUND",
			"this control plane does not hold those bytes",
			"An archive arrives here when it is published, or when this control plane fetched it "+
				"from the open directory for a node. Ask for the facility first.",
			map[string]any{"hash": digest})
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("X-Baton-SHA256", digest)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}
