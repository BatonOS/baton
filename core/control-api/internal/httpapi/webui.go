// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)









//go:embed all:assets
var webAssets embed.FS






func (a *API) webUIHandler(mux *http.ServeMux) http.Handler {
	sub, err := fs.Sub(webAssets, "assets")
	if err != nil {
		a.Logger.Warn("no embedded web console", "error", err)
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		a.Logger.Info("web console not built into this binary; run `make build-web`")
		return a.missingConsoleHandler(mux)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {












		if apiPath(r.URL.Path) {
			a.refuseUnrouted(mux, w, r)
			return
		}



		if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}





func (a *API) missingConsoleHandler(mux *http.ServeMux) http.Handler {
	const page = `<!doctype html><meta charset="utf-8"><title>BATON</title>
<style>body{font:15px/1.6 ui-sans-serif,system-ui,sans-serif;margin:4rem auto;max-width:34rem;padding:0 1.5rem}
code{background:#f4f4f5;padding:.15em .4em;border-radius:3px}</style>
<h1>BATON control plane</h1>
<p>The API is running. The web console was not built into this binary.</p>
<p>Build it with <code>make build-web</code> and rebuild, or use the CLI:
<code>baton status</code>, <code>baton node list</code>, <code>baton events</code>.</p>`

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiPath(r.URL.Path) {
			a.refuseUnrouted(mux, w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(page))
	})
}







func apiPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/")
}




var probedMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

















func (a *API) refuseUnrouted(mux *http.ServeMux, w http.ResponseWriter, r *http.Request) {
	var allowed []string
	for _, m := range probedMethods {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" {
			allowed = append(allowed, m)
		}
	}
	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		a.fail(w, r, http.StatusMethodNotAllowed, CodeMethodNotAllowed,
			r.URL.Path+" does not take "+r.Method+"; it takes "+strings.Join(allowed, ", "),
			"The path is right; the method is not. Check it against packages/api-contracts/openapi.yaml.", nil)
		return
	}
	a.fail(w, r, http.StatusNotFound, CodeEndpointMissing,
		"no route on this server serves "+r.URL.Path+" (asked for "+strings.Join(probedMethods, ", ")+")",
		"This server serves "+BasePath+". Check the path against packages/api-contracts/openapi.yaml — or this control plane is older than the client that asked.", nil)
}
