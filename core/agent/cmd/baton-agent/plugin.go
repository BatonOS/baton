// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/batonos/baton/core/agent/internal/inbox"
	"github.com/batonos/baton/core/agent/internal/plugins"
)
































func runPlugin(args []string) int {
	if len(args) == 0 {
		pluginUsage()
		return exitConfigError
	}
	sub := args[0]
	rest := args[1:]
	asJSON := false
	for _, a := range rest {
		if a == "--json" {
			asJSON = true
		}
	}

	dataDir := envOr("BATON_DATA_DIR", "/var/lib/baton")

	switch sub {
	case "list":
		return runPluginList(dataDir, asJSON)
	case "adopt", "enable", "disable", "remove":
		return runPluginManage(dataDir, sub, rest, asJSON)
	case "contract":




		return printContract(inbox.New(dataDir), "")
	default:
		fmt.Fprintf(os.Stderr, "baton-plugin: unknown verb %q\n", sub)
		pluginUsage()
		return exitConfigError
	}
}

func pluginUsage() {
	fmt.Fprint(os.Stderr, ""+
		"baton-plugin — the facilities this office is fitted with\n"+
		"\n"+
		"  baton-plugin list [--json]        what is installed here, and how much each may do\n"+
		"  baton-plugin adopt <ref>          take one the company shares (an id, or <type>/<name>)\n"+
		"  baton-plugin disable <id>         take a facility out of use; its bytes and your files stay\n"+
		"  baton-plugin enable  <id>         put it back\n"+
		"  baton-plugin remove  <id>         give it back — the complete verb\n"+
		"  baton-plugin contract             the verbs and exit codes this node offers\n"+
		"\n"+
		"Each facility has a page at /opt/baton/plugins/<id>/PLUGIN.md saying what it is.\n")
}










type pluginListEntry struct {
	ID      string `json:"id"`
	Version string `json:"version"`





	Trust       string `json:"trust"`
	TrustReason string `json:"trust_reason"`





	Skills       []string `json:"skills,omitempty"`
	State        []string `json:"state,omitempty"`
	Hook         string   `json:"hook"`
	Instructions string   `json:"instructions"`
}

func runPluginList(dataDir string, asJSON bool) int {
	path := filepath.Join(dataDir, "plugins", plugins.ReportFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {





			fmt.Fprint(os.Stderr, "baton-plugin list: this node has no plugin report yet\n"+
				"  The daemon writes one every time it reconciles. Absent means it has not run\n"+
				"  since this container started — NOT that there are no facilities.\n")
			return exitInternal
		}
		fmt.Fprintf(os.Stderr, "baton-plugin list: could not read the plugin report: %v\n", err)
		return exitInternal
	}
	var rep plugins.Report
	if err := json.Unmarshal(raw, &rep); err != nil {
		fmt.Fprintf(os.Stderr, "baton-plugin list: the plugin report is not readable: %v\n"+
			"  It is written by the daemon; a report this build cannot parse is not one to guess at.\n", err)
		return exitInternal
	}

	out := make([]pluginListEntry, 0, len(rep.Plugins))
	for _, p := range rep.Plugins {
		e := pluginListEntry{
			ID: p.ID, Version: p.Version,
			Trust: string(p.Trust), TrustReason: p.TrustReason,
			Hook: p.Hook, Instructions: p.Instructions,
		}
		for _, s := range p.Skills {



			e.Skills = append(e.Skills, fmt.Sprintf("%s (%s)", s.Name, s.Source))
		}
		for _, st := range p.State {
			e.State = append(e.State, st.Path)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{"plugins": out})
		return exitOK
	}

	if len(out) == 0 {



		fmt.Println("no facilities are installed in this office")
		return exitOK
	}
	for _, e := range out {
		fmt.Printf("%s %s  trust=%s\n", e.ID, e.Version, e.Trust)
		if e.TrustReason != "" {
			fmt.Printf("    why: %s\n", e.TrustReason)
		}
		if len(e.Skills) > 0 {
			fmt.Printf("    skills: %s\n", strings.Join(e.Skills, ", "))
		}
		if e.Instructions != "" && e.Instructions != "none" {
			fmt.Printf("    instructions: %s\n", e.Instructions)
		}
		if e.Hook != "" && e.Hook != "none" {
			fmt.Printf("    hook: %s\n", e.Hook)
		}
	}
	return exitOK
}







const manageWait = 30 * time.Second

func runPluginManage(dataDir, verb string, rest []string, asJSON bool) int {



	if refuseIfSkeleton("baton-plugin "+verb) {
		return exitPrecondition
	}
	var id string
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") {
			id = a
			break
		}
	}
	if id == "" {
		fmt.Fprintf(os.Stderr, "baton-plugin %s: name the facility\n"+
			"  baton-plugin %s example.notepad\n"+
			"  `baton-plugin list` shows what is here.\n", verb, verb)
		return exitConfigError
	}

	stateDir := filepath.Join(dataDir, "plugins")
	done, err := plugins.SubmitManageRequest(stateDir, verb, id, manageWait)
	if err != nil {



		fmt.Fprintf(os.Stderr, "baton-plugin %s: %v\n", verb, err)
		return exitInternal
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(done)
	}

	if done.Refused != "" {
		if !asJSON {
			fmt.Fprintf(os.Stderr, "baton-plugin %s: %s\n", verb, done.Refused)
		}













		return exitPrecondition
	}
	if !asJSON {




		fmt.Printf("%s %s\n", done.Plugin, map[string]string{
			"adopt":   "is here and in use",
			"disable": "is out of use; its bytes and what it kept for you are untouched",
			"enable":  "is back in use",
			"remove":  "is gone, and so is what it put in your workspace",
		}[verb])
		for _, w := range done.Withdrawn {
			fmt.Printf("    %s\n", w)
		}
		if done.Summary != "" {
			fmt.Printf("    %s\n", done.Summary)
		}
	}
	return exitOK
}






















func refuseIfSkeleton(verb string) bool {
	if _, err := os.Stat("/etc/baton/runtime.yaml"); err == nil {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s: this node supervises no runtime, so it has no facilities to manage\n"+
		"  Its template named none — an office opened with nothing decided about what goes in it.\n"+
		"  The daemon IS running; it has nothing to reconcile. A runtime is chosen at create,\n"+
		"  from a template that names one.\n", verb)
	return true
}
