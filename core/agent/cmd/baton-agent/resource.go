// SPDX-License-Identifier: Apache-2.0

package main





























import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/batonos/baton/core/agent/internal/identity"
	"github.com/batonos/baton/core/agent/internal/inbox"
	"github.com/batonos/baton/core/agent/internal/plugins"
)





const LibraryFormat = "baton-adopted/1"












func runResource(args []string) int {











	if len(args) == 0 {
		resourceUsage()
		return exitConfigError
	}
	sub := args[0]
	fs := flag.NewFlagSet("baton-resource "+sub, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", envOr("BATON_DATA_DIR", "/var/lib/baton"), "node data directory")
	asJSON := fs.Bool("json", false, "machine-readable output")
	typ := fs.String("type", "", "publish: what kind of thing this is")
	name := fs.String("name", "", "publish: what the company will call it")
	scope := fs.String("scope", "", "publish: private|network — required, no default")
	pluginID := fs.String("plugin", "", "skills sync: the facility whose skills to re-link (required)")
	fs.Usage = resourceUsage









	if err := fs.Parse(args[1:]); err != nil {
		return exitConfigError
	}
	var positional []string
	for fs.NArg() > 0 {
		positional = append(positional, fs.Arg(0))
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return exitConfigError
		}
	}

	switch sub {
	case "adopt":
		return runResourceAdopt(positional, *dataDir, *asJSON)






	case "publish":
		return runResourcePublish(positional, *typ, *name, *scope, *dataDir, *asJSON)





	case "contract":
		return printContract(inbox.New(*dataDir), "")




	case "skills":
		return runResourceSkills(positional, *pluginID, *dataDir, *asJSON)

	case "":
		resourceUsage()
		return exitConfigError
	default:
		fmt.Fprintf(os.Stderr, "baton-resource: unknown subcommand %q\n", sub)
		resourceUsage()
		return exitConfigError
	}
}

func resourceUsage() {
	fmt.Fprint(os.Stderr, `baton-resource — take a copy of something the company shares

  baton-resource adopt <id>            take it by resource id
  baton-resource adopt <type>/<name>   take it by what it is and what it is called
  baton-resource publish <path> --type T --name N --scope private|network
                                       hand something to the company. <path> is
                                       under public/ in your workspace, and
                                       --scope has no default on purpose.
  baton-resource contract              what this node offers you (the same
                                       document baton-inbox contract prints)
  baton-resource skills sync --plugin <id>
                                       a facility grew a skill at run time: ask the
                                       node daemon to link it (and say what it did:
                                       linked N / refused M of K, each with a reason)

Adopted copies land under .baton/portable/library/<type>/<name> in your
workspace, with a `+"`<name>.origin.json`"+` beside them saying where each came
from and at what hash. They are PINNED: the copy does not change when the
company's version does.
`)
}



type adoptResult struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Binding string `json:"binding"`
}

func runResourceAdopt(args []string, dataDir string, asJSON bool) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "baton-resource adopt: name one thing to take\n"+
			"  baton-resource adopt knowledge/onboarding\n"+
			"  baton-resource adopt 13e2b2368d19e0282103cfa5a538bfcb\n")
		return exitConfigError
	}
	ref := args[0]




	ws := os.Getenv("BATON_WORKSPACE")
	if ws == "" {
		fmt.Fprint(os.Stderr, "baton-resource adopt: this runtime has no workspace (BATON_WORKSPACE is not set)\n"+
			"  An adopted copy has to land somewhere. A node template declares that place as\n"+
			"  adapter.workspace.mountPath; without one there is nowhere for this to go.\n")
		return exitConfigError
	}

	client, master, code := nodeFace(dataDir)
	if client == nil {
		return code
	}

	res, code := resolveResource(client, master, ref)
	if res == nil {
		return code
	}

	out, code := landAdopted(ws, res)
	if code != exitOK {
		return code
	}
	if asJSON {
		raw, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(raw))
		return exitOK
	}
	fmt.Printf("%s\n", out.Path)
	fmt.Fprintf(os.Stderr, "pinned at %s\n", out.Hash)
	return exitOK
}








func landAdopted(ws string, res map[string]any) (adoptResult, int) {
	body, _ := res["__body"].(string)
	typ, _ := res["type"].(string)
	name, _ := res["name"].(string)
	hash, _ := res["hash"].(string)
	id, _ := res["resource_id"].(string)
	publisher, _ := res["publisher"].(string)





	if body == "" {
		fmt.Fprintf(os.Stderr, "baton-resource adopt: %s/%s has no text body on this face\n"+
			"  Stage 1 adopts text. If this is a byte artifact, it needs a transfer this\n"+
			"  verb does not have yet — say so rather than writing an empty file.\n", typ, name)
		return adoptResult{}, exitConfigError
	}












	if hash == "" {
		fmt.Fprintf(os.Stderr, "baton-resource adopt: %s/%s carries no hash, so a pinned copy of it would pin nothing\n"+
			"  `pinned` means the bytes here are the bytes at a named hash. Without one this\n"+
			"  would write a copy whose provenance says it can be checked and cannot.\n", typ, name)
		return adoptResult{}, exitInternal
	}
	if typ == "" || name == "" {
		fmt.Fprint(os.Stderr, "baton-resource adopt: the control plane named neither a type nor a name for this\n"+
			"  Both decide where the copy lands. Writing it somewhere invented would put a file\n"+
			"  in a place nothing will look.\n")
		return adoptResult{}, exitInternal
	}

	dir := filepath.Join(ws, ".baton", "portable", "library", typ)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource adopt: make %s: %v\n", dir, err)
		return adoptResult{}, exitInternal
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource adopt: write %s: %v\n", path, err)
		return adoptResult{}, exitInternal
	}









	prov := map[string]any{
		"format":      LibraryFormat,
		"adopted_at":  time.Now().UTC().Format(time.RFC3339),
		"source":      "network",
		"resource_id": id,
		"type":        typ,
		"name":        name,























		"publisher":   publisher,
		"hash":        hash,
		"binding":     "pinned",
		"binding_means": "the bytes here are the bytes at that hash. If the company's " +
			"version moves, this copy does not — and this file is how you can tell.",
	}
	provRaw, _ := json.MarshalIndent(prov, "", "  ")
	if err := os.WriteFile(path+".origin.json", append(provRaw, '\n'), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource adopt: write provenance for %s: %v\n", path, err)
		return adoptResult{}, exitInternal
	}

	return adoptResult{Path: path, Hash: hash, Binding: "pinned"}, exitOK
}







func resolveResource(client *http.Client, master, ref string) (map[string]any, int) {
	base := strings.TrimRight(master, "/") + "/api/v1alpha1/agent/network-resources"

	if typ, name, ok := strings.Cut(ref, "/"); ok {
		list, code := getJSON(client, base+"?type="+typ)
		if list == nil {
			return nil, code
		}
		items, _ := list["items"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m != nil && m["name"] == name {
				id, _ := m["resource_id"].(string)
				return getResourceBody(client, base, id)
			}
		}



		fmt.Fprintf(os.Stderr, "baton-resource adopt: the company shares no %s called %q\n"+
			"  What it does share is projected into .baton/local/net/resources.json — read that\n"+
			"  rather than guessing names.\n", typ, name)
		return nil, exitConfigError
	}
	return getResourceBody(client, base, ref)
}

func getResourceBody(client *http.Client, base, id string) (map[string]any, int) {
	res, code := getJSON(client, base+"/"+id)
	if res == nil {
		return nil, code
	}



	if detail, ok := res["detail"].(map[string]any); ok {
		if b, ok := detail["body"].(string); ok {
			res["__body"] = b
		} else if y, ok := detail["yaml"].(string); ok {
			res["__body"] = y
		}
	}
	return res, exitOK
}

func getJSON(client *http.Client, url string) (map[string]any, int) {
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource: reach the control plane: %v\n", err)
		return nil, exitInternal
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &apiErr)





		if resp.StatusCode == http.StatusNotFound {
			fmt.Fprint(os.Stderr, "baton-resource adopt: the company shares nothing under that id\n"+
				"  Either it does not exist or it is not shared with nodes. Those are one answer\n"+
				"  here on purpose: a node that could tell them apart could map what it may not read.\n")
			return nil, exitConfigError
		}
		msg := apiErr.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		fmt.Fprintf(os.Stderr, "baton-resource: the control plane refused (%d %s): %s\n",
			resp.StatusCode, apiErr.Code, msg)
		return nil, exitInternal
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource: the control plane's answer is not JSON: %v\n", err)
		return nil, exitInternal
	}
	return out, exitOK
}



func nodeFace(dataDir string) (*http.Client, string, int) {
	ids := identity.NewStore(dataDir)
	if !ids.Enrolled() {
		fmt.Fprint(os.Stderr, "baton-resource: this node holds no identity, so it cannot ask for anything\n"+
			"  Shared resources are read over the node face with the node's own certificate.\n")
		return nil, "", exitConfigError
	}
	id, err := ids.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource: read identity: %v\n", err)
		return nil, "", exitInternal
	}
	master := os.Getenv("BATON_MASTER_URL")
	if master == "" {
		master = id.EntryPoint
	}
	if master == "" {
		fmt.Fprint(os.Stderr, "baton-resource: this node has no control-plane address it can trust\n"+
			"  BATON_MASTER_URL is unset and no entry point was recorded at enrolment.\n")
		return nil, "", exitConfigError
	}
	client, err := nodeClient(ids)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource: %v\n", err)
		return nil, "", exitInternal
	}
	return client, master, exitOK
}














type publishReceipt struct {
	ResourceID string `json:"resource_id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	Hash       string `json:"hash"`
	Result     string `json:"result"`
}









type publishResult struct {
	ResourceID string `json:"resource_id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Scope      string `json:"scope"`
	Hash       string `json:"hash"`
	Result     string `json:"result"`
}
























func runResourcePublish(positional []string, typ, name, scope, dataDir string, asJSON bool) int {
	if len(positional) != 1 || typ == "" || name == "" {
		fmt.Fprint(os.Stderr, "baton-resource publish: name one file, a type and a name\n"+
			"  baton-resource publish public/onboarding.md --type knowledge --name onboarding --scope network\n")
		return exitConfigError
	}

	switch scope {
	case "private", "network":
	case "":
		fmt.Fprint(os.Stderr, "baton-resource publish: say --scope private or --scope network\n"+
			"  There is no default, and that is deliberate: `public` means one thing as a\n"+
			"  DIRECTORY (you are willing to give this out) and another as a SCOPE (anyone at\n"+
			"  all). A default in that overlap would make forgetting look like a decision.\n")
		return exitConfigError
	case "public":




		fmt.Fprint(os.Stderr, "baton-resource publish: this node cannot publish to --scope public\n"+
			"  Public release is not one operation: registering with the directory happens\n"+
			"  operator-side, and a node doing half of it and reporting success would tell you\n"+
			"  the world can see something it cannot. Ask an operator.\n")
		return exitConfigError
	default:
		fmt.Fprintf(os.Stderr, "baton-resource publish: %q is not a scope (private|network)\n", scope)
		return exitConfigError
	}

	ws := os.Getenv("BATON_WORKSPACE")
	if ws == "" {
		fmt.Fprint(os.Stderr, "baton-resource publish: this runtime has no workspace (BATON_WORKSPACE is not set)\n"+
			"  What you hand out comes from public/ in the workspace; without one there is\n"+
			"  nowhere for that to be.\n")
		return exitConfigError
	}
	body, code := readFromPublic(ws, positional[0])
	if code != exitOK {
		return code
	}

	client, master, code := nodeFace(dataDir)
	if client == nil {
		return code
	}



	req, _ := json.Marshal(map[string]any{
		"type": typ, "name": name, "visibility": scope, "body": string(body),
	})
	resp, err := client.Post(strings.TrimRight(master, "/")+"/api/v1alpha1/agent/network-resources",
		"application/json", strings.NewReader(string(req)))
	if err != nil {




		fmt.Fprintf(os.Stderr, "baton-resource publish: could not reach the company: %v\n"+
			"  Nothing was published. This is not queued — there is no recipient to wait for.\n", err)
		return exitInternal
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return reportPublishRefusal(os.Stderr, resp.StatusCode, raw)
	}

	var got publishReceipt
	_ = json.Unmarshal(raw, &got)
	out := publishResult{
		ResourceID: got.ResourceID, Type: got.Type, Name: got.Name,
		Scope: got.Visibility, Hash: got.Hash, Result: got.Result,
	}



	if asJSON {
		pretty, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(pretty))
		return exitOK
	}
	fmt.Printf("%s/%s %s (%s)\n", out.Type, out.Name, out.Result, out.Scope)
	fmt.Fprintf(os.Stderr, "recorded at %s\n", out.Hash)
	return exitOK
}









func readFromPublic(ws, rel string) ([]byte, int) {
	root := filepath.Join(ws, "public")



	candidate := filepath.Join(root, strings.TrimPrefix(filepath.Clean(rel), "public/"))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "baton-resource publish: %s is not in public/\n"+
				"  What you hand out is what you put in public/ — that is what the directory\n"+
				"  means. Put the file there first.\n", rel)
			return nil, exitConfigError
		}
		fmt.Fprintf(os.Stderr, "baton-resource publish: resolve %s: %v\n", rel, err)
		return nil, exitInternal
	}
	rootResolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource publish: there is no public/ in this workspace\n"+
			"  It is the sharing boundary; without it there is nothing this verb may read.\n")
		return nil, exitConfigError
	}
	if !isBeneath(resolved, rootResolved) {
		fmt.Fprintf(os.Stderr, "baton-resource publish: %s resolves outside public/\n"+
			"  public/ is the boundary, not the prefix of a path. A link that leaves it is a\n"+
			"  way to hand out something you never put in there.\n", rel)
		return nil, exitConfigError
	}
	body, err := os.ReadFile(resolved)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource publish: read %s: %v\n", rel, err)
		return nil, exitInternal
	}
	if len(body) == 0 {
		fmt.Fprintf(os.Stderr, "baton-resource publish: %s is empty\n"+
			"  Publishing nothing would occupy the name and hand the company a blank.\n", rel)
		return nil, exitConfigError
	}
	return body, exitOK
}

func isBeneath(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}



func reportPublishRefusal(w io.Writer, status int, raw []byte) int {
	var apiErr struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		Remediation string `json:"remediation"`
	}
	_ = json.Unmarshal(raw, &apiErr)
	switch {





	case status == http.StatusNotFound:
		fmt.Fprint(w, "baton-resource publish: this control plane does not accept publishing from nodes\n"+
			"  The route is absent, not the resource. That is a version difference between this\n"+
			"  node and the company, not something wrong with what you asked for.\n")
		return exitInternal
	case status == http.StatusConflict:
		fmt.Fprintf(w, "baton-resource publish: that name already belongs to somebody else\n"+
			"  %s\n"+
			"  Publishing over it would replace their row and report success. Pick another name.\n",
			strings.TrimSpace(apiErr.Message))
		return exitConfigError
	case status == http.StatusForbidden:
		fmt.Fprintf(w, "baton-resource publish: the company refused: %s\n  %s\n",
			strings.TrimSpace(apiErr.Message), strings.TrimSpace(apiErr.Remediation))
		return exitConfigError
	case status >= 400 && status < 500:
		fmt.Fprintf(w, "baton-resource publish: the company refused (%d %s): %s\n",
			status, apiErr.Code, strings.TrimSpace(apiErr.Message))
		return exitConfigError
	default:
		fmt.Fprintf(w, "baton-resource publish: the company answered %d %s: %s\n"+
			"  Nothing was published.\n", status, apiErr.Code, strings.TrimSpace(apiErr.Message))
		return exitInternal
	}
}











func runResourceSkills(args []string, pluginID, dataDir string, asJSON bool) int {



	if refuseIfSkeleton("baton-resource skills sync") {
		return exitPrecondition
	}
	if len(args) != 1 || args[0] != "sync" {
		fmt.Fprintln(os.Stderr, "baton-resource skills: the one verb is `sync` — baton-resource skills sync --plugin <id>")
		return exitConfigError
	}
	if pluginID == "" {
		fmt.Fprintln(os.Stderr, "baton-resource skills sync: --plugin <id> is required — the answer is about one facility")
		return exitConfigError
	}
	done, err := plugins.SubmitSyncRequest(filepath.Join(dataDir, "plugins"), pluginID, 15*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baton-resource skills sync: %v\n", err)
		return exitInternal
	}
	if asJSON {
		raw, _ := json.MarshalIndent(done, "", "  ")
		fmt.Println(string(raw))
		if done.Error != "" {
			return exitInternal
		}
		return exitOK
	}
	if done.Error != "" {
		fmt.Fprintf(os.Stderr, "baton-resource skills sync: the daemon could not reconcile: %s\n", done.Error)
		return exitInternal
	}
	if !done.Found {
		fmt.Printf("%s: not a facility on this node (the daemon reconciled and found no plugin with that id)\n", pluginID)
		return exitEmpty
	}


	fmt.Printf("%s: linked %d / refused %d / unknown %d of %d\n", pluginID, done.Linked, done.Refused, done.Unknown, len(done.Skills))
	for _, sk := range done.Skills {
		line := fmt.Sprintf("  %-32s %-14s %s", sk.Name, sk.State, sk.Source)
		if sk.At != "" {
			line += "  " + sk.At
		}
		if sk.Why != "" {
			line += "  — " + sk.Why
		}
		fmt.Println(line)
	}
	return exitOK
}
