// SPDX-License-Identifier: Apache-2.0

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)




var ErrNotAnObject = errors.New("core: a proposal is a JSON object")


type OutcomeError struct {
	Code        string         `json:"code"`
	Message     string         `json:"message"`
	Remediation []Remedy       `json:"remediation"`
	Detail      map[string]any `json:"detail"`
}


type Remedy struct {
	Text string `json:"text"`
}


const (
	CodePreconditionUnmet   = "PRECONDITION_UNMET"
	CodePlanChanged         = "PLAN_CHANGED"
	CodeInvalidProposal     = "INVALID_PROPOSAL"
	CodeUnknownAction       = "UNKNOWN_ACTION"
	CodeStateConflict       = "STATE_CONFLICT"
	CodeIdempotencyConflict = "IDEMPOTENCY_CONFLICT"
)

func invalid(field, reason string) *OutcomeError {
	return &OutcomeError{
		Code: CodeInvalidProposal, Message: field + ": " + reason,
		Remediation: []Remedy{{Text: "Fix " + field + " and propose again."}},
		Detail:      map[string]any{"field": field, "reason": reason},
	}
}




var knownConstraints = map[string]bool{"max_cost": true, "max_downtime_ms": true, "not_after": true}









func Validate(p map[string]any, interactionOwned func(id string) bool) (ActionDecl, *OutcomeError) {
	if p["kind"] != "proposal" {
		return ActionDecl{}, invalid("kind", `must be "proposal"`)
	}
	if v, ok := p["contract_version"].(string); !ok || majorOf(v) != majorOf(ContractVersion) {
		return ActionDecl{}, invalid("contract_version", "must be a "+majorOf(ContractVersion)+".x semver")
	}
	if p["source"] != "action-proposal" {
		return ActionDecl{}, invalid("source", `must be "action-proposal" (delegated-approval is reserved for Phase 2)`)
	}
	if k, ok := p["idempotency_key"].(string); !ok || k == "" {
		return ActionDecl{}, invalid("idempotency_key", "required")
	}
	if _, ok := p["via"].(map[string]any); !ok {
		return ActionDecl{}, invalid("via", "required, an object {planner, planner_version, session, client}")
	}
	action, ok := p["action"].(map[string]any)
	name, _ := action["name"].(string)
	version, _ := action["version"].(string)
	if !ok || name == "" || version == "" {
		return ActionDecl{}, invalid("action", "required, {name, version}")
	}
	decl, ok := resolveAction(name, version)
	if !ok {
		available := []string{}
		if d, known := lookupAction(name); known {
			available = append(available, d.Version)
		}
		return ActionDecl{}, &OutcomeError{
			Code: CodeUnknownAction, Message: fmt.Sprintf("%s version %s is not an action this Core declares", name, version),
			Remediation: []Remedy{{Text: "Use a declared action and version."}},
			Detail:      map[string]any{"action": name, "requested_version": version, "available_versions": available},
		}
	}
	if decl.TargetKind != "none" {
		t, ok := p["target"].(map[string]any)
		id, _ := t["id"].(string)
		if !ok || t["kind"] != decl.TargetKind || id == "" {
			return decl, invalid("target", fmt.Sprintf("required, {kind: %q, id}", decl.TargetKind))
		}
	}
	params, ok := p["parameters"].(map[string]any)
	if !ok {
		return decl, invalid("parameters", "required, an object")
	}


	if field, reason := checkSpecs(decl.Params, params); field != "" {
		return decl, invalid(field, reason)
	}

	if decl.Check != nil {
		if field, reason := decl.Check(params); field != "" {
			return decl, invalid(field, reason)
		}
	}
	if !decl.Effects.ReadOnlyEffects() {
		effects, ok := p["expected_effects"].([]any)
		if !ok || len(effects) == 0 {
			return decl, invalid("expected_effects", "required for an action that is not read-only")
		}
		for i, e := range effects {
			m, ok := e.(map[string]any)
			class, _ := m["class"].(string)
			if !ok || class == "" {
				return decl, invalid(fmt.Sprintf("expected_effects[%d].class", i), "required")
			}









			if !KnownConsequenceClass(class) {
				return decl, invalid(fmt.Sprintf("expected_effects[%d].class", i),
					fmt.Sprintf("%q is not a consequence class; this build knows %s", class, consequenceClassList()))
			}
		}
	}
	if c, present := p["constraints"]; present {
		m, ok := c.(map[string]any)
		if !ok {
			return decl, invalid("constraints", "an object")
		}
		for k := range m {
			if !knownConstraints[k] {
				return decl, invalid("constraints."+k, "not a constraint this Core knows; an unknown constraint is refused, never ignored")
			}
		}
		if v, present := m["not_after"]; present {
			s, _ := v.(string)
			if _, err := time.Parse(time.RFC3339, s); err != nil {
				return decl, invalid("constraints.not_after", "an RFC 3339 time")
			}
		}
		if v, present := m["max_downtime_ms"]; present {
			if n, ok := v.(json.Number); !ok || n.String() == "" || n.String()[0] == '-' {
				return decl, invalid("constraints.max_downtime_ms", "a non-negative integer of milliseconds")
			}
		}
		if v, present := m["max_cost"]; present {
			mc, ok := v.(map[string]any)
			amount, _ := mc["amount"].(string)
			currency, _ := mc["currency"].(string)
			if !ok || amount == "" || currency == "" {
				return decl, invalid("constraints.max_cost", "{amount: decimal string, currency}")
			}
		}
	}
	if v, present := p["note"]; present {
		if _, ok := v.(string); !ok {
			return decl, invalid("note", "a string")
		}
	}
	if v, present := p["interaction"]; present {
		id, _ := v.(string)
		if id == "" || !interactionOwned(id) {
			return decl, invalid("interaction", "not an interaction Core issued to this actor")
		}
	}
	return decl, nil
}



func parse(body []byte) (map[string]any, string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var p map[string]any
	if err := dec.Decode(&p); err != nil || p == nil {
		return nil, "", ErrNotAnObject
	}





	canon, err := json.Marshal(p)
	if err != nil {
		return nil, "", ErrNotAnObject
	}
	sum := sha256.Sum256(canon)
	return p, "sha256:" + hex.EncodeToString(sum[:]), nil
}


type Deps struct {
	Transactions spi.TransactionStore
	Interactions spi.InteractionStore


	Nodes spi.NodeStore


	Grants spi.GrantStore
}












func Submit(ctx context.Context, d Deps, actor string, body []byte, now time.Time) (*spi.Transaction, bool, error) {
	p, digest, err := parse(body)
	if err != nil {
		return nil, false, err
	}
	for attempt := 0; ; attempt++ {
		t, created, err := submitOnce(ctx, d, actor, body, p, digest, now)



		if errors.Is(err, spi.ErrIdempotencyKeyHeld) && attempt == 0 {
			continue
		}
		return t, created, err
	}
}

func submitOnce(ctx context.Context, d Deps, actor string,
	body []byte, p map[string]any, digest string, now time.Time) (*spi.Transaction, bool, error) {
	txs, ints := d.Transactions, d.Interactions
	owned := func(id string) bool {
		i, err := ints.Get(ctx, id)
		return err == nil && i.Actor == actor
	}




	interactionFor := func() (string, error) {
		named, isString := p["interaction"].(string)
		if _, present := p["interaction"]; present {
			if isString && named != "" && owned(named) {
				return named, nil
			}
			return "", nil
		}
		id := "int_" + uuid.NewString()
		if err := ints.Create(ctx, &spi.Interaction{ID: id, Actor: actor, CreatedAt: now}); err != nil {
			return "", err
		}
		return id, nil
	}

	key, _ := p["idempotency_key"].(string)
	if key != "" {
		held, err := txs.GetByIdempotencyKey(ctx, actor, key)
		switch {
		case err == nil && held.ProposalDigest == digest:
			return held, false, nil
		case err == nil:

			interaction, err := interactionFor()
			if err != nil {
				return nil, false, err
			}
			t := draft(actor, body, digest, p, interaction)
			t.IdempotencyKey = ""


			if decl, ok := resolveAction(t.ActionName, t.ActionVersion); ok {
				t.ActionVersion = decl.Version
			}
			return reject(ctx, txs, t, now, &OutcomeError{
				Code: CodeIdempotencyConflict, Message: "this idempotency key was already used for a different proposal",
				Remediation: []Remedy{{Text: "Use a new idempotency key for a new proposal, or resend the original unchanged."}},
				Detail:      map[string]any{"transaction": held.ID},
			})
		case !errors.Is(err, spi.ErrNotFound):
			return nil, false, err
		}
	}

	decl, verr := Validate(p, owned)


	interaction, err := interactionFor()
	if err != nil {
		return nil, false, err
	}
	if verr != nil {
		t := draft(actor, body, digest, p, interaction)
		if decl.Name != "" {
			t.ActionVersion = decl.Version
		}
		return reject(ctx, txs, t, now, verr)
	}








	auth, err := authorize(ctx, d.Grants, actor, decl, now)
	if err != nil {
		return nil, false, err
	}



	authJSON, err := json.Marshal(map[string]any{
		"granted": auth.Granted, "required_grants": auth.RequiredGrants,
		"source": auth.Source, "subject": auth.Subject,
		"action": auth.Action, "checked_at": auth.CheckedAt.UTC(),
	})
	if err != nil {
		return nil, false, err
	}

	if occ, err := nameOccupant(ctx, d.Nodes, p); err != nil {
		return nil, false, err
	} else if occ != nil {
		t := draft(actor, body, digest, p, interaction)
		t.ActionVersion = decl.Version
		return reject(ctx, txs, t, now, &OutcomeError{
			Code:        CodePreconditionUnmet,
			Message:     fmt.Sprintf("a Workspace named %q already exists", occ.DisplayName),
			Remediation: []Remedy{{Text: "Choose another name for the new Workspace."}},
			Detail: map[string]any{
				"precondition": "parameters.name is not taken",
				"occupant":     map[string]any{"node_id": occ.NodeID, "name": occ.DisplayName, "status": string(occ.Status)},
			},
		})
	}






	if params, _ := p["parameters"].(map[string]any); hasFactEnum(decl.Params) {
		if ferr := checkFactEnums(decl.Params, params, factsFor(ctx, p)); ferr != nil {
			t := draft(actor, body, digest, p, interaction)
			t.ActionVersion = decl.Version
			return reject(ctx, txs, t, now, ferr)
		}
	}

	t := draft(actor, body, digest, p, interaction)
	t.ActionVersion = decl.Version
	t.Authorization = authJSON
	t.CreatedAt = now








	plan, err := Compile(t.ID, p, factsFor(ctx, p))
	if err != nil {



		return nil, false, err
	}
	t.Plan = plan
	got, err := txs.Create(ctx, t, spi.CreateOptions{CheckInteraction: true, CheckSamePlanOpen: true})
	switch {
	case err == nil:


		if _, err := applyPolicy(ctx, txs, got, p, now); err != nil {
			return nil, false, err
		}
		got, err = txs.Get(ctx, got.ID)
		return got, true, err
	case errors.Is(err, spi.ErrSamePlanOpen):











		return reject(ctx, txs, t, now, &OutcomeError{
			Code: CodeStateConflict, Message: "the same plan is already open on this action and target",
			Remediation: []Remedy{{Text: "Follow the transaction that is already open; its id is in detail.transaction."}},
			Detail:      map[string]any{"transaction": got.ID, "state": string(got.State), "plan_digest": got.Plan.PlanDigest},
		})
	case errors.Is(err, spi.ErrOpenOnInteraction):



		return reject(ctx, txs, t, now, &OutcomeError{
			Code: CodeStateConflict, Message: "a transaction with the same action and target is already open on this interaction",
			Remediation: []Remedy{{Text: "Follow the open transaction instead of proposing it again."}},
			Detail:      map[string]any{"transaction": got.ID, "state": string(got.State)},
		})
	default:
		return nil, false, err
	}
}


func draft(actor string, body []byte, digest string, p map[string]any, interaction string) *spi.Transaction {
	t := &spi.Transaction{
		ID: "tx_" + uuid.NewString(), Proposal: append(json.RawMessage(nil), body...), ProposalDigest: digest,
		Actor: actor, State: spi.TxProposed, InteractionID: interaction,
	}
	t.IdempotencyKey, _ = p["idempotency_key"].(string)
	if via, ok := p["via"].(map[string]any); ok {
		t.Via, _ = json.Marshal(via)
	}
	if a, ok := p["action"].(map[string]any); ok {
		t.ActionName, _ = a["name"].(string)
		t.ActionVersion, _ = a["version"].(string)
	}
	if tg, ok := p["target"].(map[string]any); ok {
		t.TargetKind, _ = tg["kind"].(string)




		t.TargetID, _ = tg["id"].(string)
	}
	return t
}



func reject(ctx context.Context, txs spi.TransactionStore, t *spi.Transaction, now time.Time, oe *OutcomeError) (*spi.Transaction, bool, error) {
	t.CreatedAt = now
	if _, err := txs.Create(ctx, t, spi.CreateOptions{}); err != nil {
		return nil, false, err
	}
	raw, err := json.Marshal(oe)
	if err != nil {
		return nil, false, err
	}
	if err := Transition(ctx, txs, spi.TxTransition{
		ID: t.ID, From: spi.TxProposed, To: spi.TxRejected, By: spi.TxByCore, At: now, Error: raw,
	}); err != nil {
		return nil, false, err
	}
	got, err := txs.Get(ctx, t.ID)
	return got, true, err
}

































func nameOccupant(ctx context.Context, nodes spi.NodeStore, p map[string]any) (*spi.Node, error) {
	params, _ := p["parameters"].(map[string]any)
	name, _ := params["name"].(string)
	if name == "" {
		return nil, nil
	}
	if nodes == nil {



		return nil, errors.New("core: no node registry to check the clone's name against")
	}
	n, err := nodes.GetByName(ctx, spi.DefaultTenant, name)
	if errors.Is(err, spi.ErrNotFound) {
		return nil, nil
	}
	return n, err
}
