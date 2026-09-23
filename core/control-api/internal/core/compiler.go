// SPDX-License-Identifier: Apache-2.0

package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)



const (
	ExecutorLocalProvider = "provider:local"
	ExecutorCore          = "core"






	ExecutorAppPrefix = "app:"
)





const WhenOutsideWorkspace = "outside-workspace"


















func dispatchedToAnExecutor(executor string) bool { return executor != ExecutorCore }


















type PlacementTraits struct {

	Priced bool

	OnCurrentHost bool
}










const PlacementLocal = "prov_local"

var placementTraits = map[string]PlacementTraits{

	PlacementLocal: {Priced: false, OnCurrentHost: true},
}
















func effectiveConsequences(decl ActionDecl, p map[string]any, facts []GovernanceFact) ([]map[string]any, []GovernanceFact) {
	out := make([]map[string]any, 0, len(decl.Consequences))
	var used []GovernanceFact
	seen := map[string]bool{}
	for _, c := range decl.Consequences {
		holds, computed, leaned := conditionHolds(c.When, decl, p, facts)
		for _, f := range leaned {
			if !seen[f.Name] {
				seen[f.Name] = true
				used = append(used, f)
			}
		}
		if !holds {
			continue
		}
		rendered := c.Rendered
		if !computed {
			rendered += " — and this build cannot check that condition, so it is treated as holding"
		}
		out = append(out, map[string]any{"class": c.Class, "rendered": rendered})
	}
	return out, used
}














func conditionHolds(when string, decl ActionDecl, p map[string]any, facts []GovernanceFact) (holds, computed bool, used []GovernanceFact) {
	switch when {
	case WhenAlways:
		return true, true, nil
	case WhenOutsideWorkspace:
		boundary, okB := factNamed(facts, FactWorkspaceBoundary)
		dest, okD := factNamed(facts, FactDestinationResolved)
		if !okB || !okD {



			return true, false, nil
		}
		return !underBoundary(dest.Value, boundary.Value), true, []GovernanceFact{boundary, dest}
	case WhenLeavesCurrentHost:
		if t, ok := traitsOf(decl, p); ok {
			return !t.OnCurrentHost, true, nil
		}
		return true, false, nil
	case WhenProviderPriced:
		if t, ok := traitsOf(decl, p); ok {
			return t.Priced, true, nil
		}
		return true, false, nil
	case WhenChangesPricedRsrcs:




		return true, false, nil
	default:





		return true, false, nil
	}
}



func traitsOf(decl ActionDecl, p map[string]any) (PlacementTraits, bool) {
	if decl.PlacementKey == "" {
		return PlacementTraits{}, false
	}
	params, _ := p["parameters"].(map[string]any)
	name, _ := params[decl.PlacementKey].(string)
	t, ok := placementTraits[name]
	return t, ok
}






















var ErrNotCompilable = fmt.Errorf("core: no compiler for this action")






func Compile(txID string, p map[string]any, facts []GovernanceFact) (*spi.TxPlan, error) {
	a, _ := p["action"].(map[string]any)
	name, _ := a["name"].(string)




	decl, ok := lookupAction(name)
	if !ok || decl.Steps == nil {
		return nil, fmt.Errorf("%w: %q", ErrNotCompilable, name)
	}
	return compileWith(decl, txID, p, facts)
}





func compileWith(decl ActionDecl, txID string, p map[string]any, facts []GovernanceFact) (*spi.TxPlan, error) {
	steps := decl.Steps(p)

	consequences, used := effectiveConsequences(decl, p, facts)
	body := map[string]any{"effective_consequences": consequences}





	if len(used) > 0 {
		body["governance_facts"] = used
	}
















	if decl.DeclarationSHA != "" {
		body["declaration_sha256"] = decl.DeclarationSHA
	}


















	if decl.Declarer.Kind != "" {
		body["declared_by"] = decl.Declarer
	}
	detail, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	if err := atMostOneDispatchedExecutor(steps); err != nil {
		return nil, err
	}
	digest, err := planDigest(steps, detail)
	if err != nil {
		return nil, err
	}
	for i := range steps {
		steps[i].OperationID = operationID(txID, digest, steps[i].Index)
		steps[i].Status = spi.StepNotStarted
	}
	return &spi.TxPlan{PlanDigest: digest, Steps: steps, Detail: detail}, nil
}


func cloneSteps(p map[string]any) []spi.TxStep {
	target, _ := p["target"].(map[string]any)
	src, _ := target["id"].(string)
	params, _ := p["parameters"].(map[string]any)
	dst, _ := params["name"].(string)
	onto, _ := params["to_placement"].(string)






















	return []spi.TxStep{
		{Index: 0, Name: "capture", Executor: ExecutorLocalProvider, Summary: "capture the portable state of " + src},
		{Index: 1, Name: "place", Executor: ExecutorLocalProvider, Summary: "create workspace " + dst + " on " + onto},
		{Index: 2, Name: "enroll", Executor: ExecutorLocalProvider, Summary: dst + " applies to join this network"},
		{Index: 3, Name: "admit", Executor: ExecutorCore, Summary: "admit " + dst + " to this network"},
		{Index: 4, Name: "materialize", Executor: ExecutorLocalProvider, Summary: "write the captured state of " + src + " into " + dst},




		{Index: 5, Name: "await_enrolled", Executor: ExecutorCore, Summary: "wait until " + dst + " has joined this network"},
	}
}



















func editAndTestSteps(p map[string]any) []spi.TxStep {
	target, _ := p["target"].(map[string]any)
	ws, _ := target["id"].(string)
	params, _ := p["parameters"].(map[string]any)
	path, _ := params["path"].(string)
	content, _ := params["content"].(string)
	before, _ := params["expected_sha_before"].(string)
	testPath, _ := params["test_path"].(string)
	after := sha256.Sum256([]byte(content))
	return []spi.TxStep{
		{Index: 0, Name: "file.write", Executor: ExecutorLocalProvider,
			Summary: fmt.Sprintf("write %s in %s (%s → sha256:%s)", path, ws, before, hex.EncodeToString(after[:]))},
		{Index: 1, Name: "test.run", Executor: ExecutorLocalProvider, Summary: fmt.Sprintf("run %s in %s", testPath, ws)},
	}
}












type PlanStep struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	Executor string `json:"executor"`
	Summary  string `json:"summary"`
}





func planBody(steps []spi.TxStep, detail json.RawMessage) (map[string]any, error) {
	var fields map[string]any
	if err := json.Unmarshal(detail, &fields); err != nil {
		return nil, err
	}
	for _, k := range []string{"steps", "plan_digest"} {
		if _, taken := fields[k]; taken {
			return nil, fmt.Errorf("core: plan detail may not carry %q: it names a field of the plan itself", k)
		}
	}
	ps := make([]PlanStep, len(steps))
	for i, s := range steps {
		ps[i] = PlanStep{Index: s.Index, Name: s.Name, Executor: s.Executor, Summary: s.Summary}
	}
	fields["steps"] = ps
	return fields, nil
}


func planDigest(steps []spi.TxStep, detail json.RawMessage) (string, error) {
	body, err := planBody(steps, detail)
	if err != nil {
		return "", err
	}
	canon, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}













func operationID(txID, digest string, index int) string {
	h := sha256.New()
	h.Write([]byte(txID))
	h.Write([]byte{0})
	h.Write([]byte(digest))
	h.Write([]byte{0})
	fmt.Fprintf(h, "%d", index)
	return "op_" + hex.EncodeToString(h.Sum(nil))[:32]
}








func inspectSteps(p map[string]any) []spi.TxStep {
	target, _ := p["target"].(map[string]any)
	ws, _ := target["id"].(string)
	return []spi.TxStep{
		{Index: 0, Name: "runtime.read", Executor: ExecutorLocalProvider,
			Summary: "read the live placement, lifecycle state and resources of " + ws},
	}
}










func createSteps(p map[string]any) []spi.TxStep {
	params, _ := p["parameters"].(map[string]any)
	name, _ := params["name"].(string)
	harness, _ := params["harness"].(string)
	placement, _ := params["placement"].(string)




	return []spi.TxStep{
		{Index: 0, Name: "provision", Executor: ExecutorLocalProvider,
			Summary: fmt.Sprintf("create an empty workspace %s on %s, running %s", name, placement, harness)},
	}
}






























func atMostOneDispatchedExecutor(steps []spi.TxStep) error {
	var seen string
	for _, s := range steps {
		if s.Executor == ExecutorCore {
			continue
		}
		if seen == "" {
			seen = s.Executor
			continue
		}
		if s.Executor != seen {
			return fmt.Errorf("core: a plan may hand steps to one dispatched executor, and this one names %q and %q: "+
				"a transaction pins ONE executor instance, so reconciliation would read the second one's steps as never sent",
				seen, s.Executor)
		}
	}
	return nil
}








func factsOfPlan(plan *spi.TxPlan) []GovernanceFact {
	if plan == nil || len(plan.Detail) == 0 {
		return nil
	}
	var body struct {
		Facts []GovernanceFact `json:"governance_facts"`
	}
	if err := json.Unmarshal(plan.Detail, &body); err != nil {
		return nil
	}
	return body.Facts
}

