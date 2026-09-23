// SPDX-License-Identifier: Apache-2.0

package core

import (
	"encoding/json"
	"fmt"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)



type ExportArtifact struct {

	Name string



	Item  string
	Bytes []byte
}
























var ExportedItems = []string{
	"proposal",
	"decision-context",
	"plan",
	"approval",
	"transaction",
	"record",
	"outcome",
	"receipt",
}











func ExportTransaction(t *spi.Transaction) ([]ExportArtifact, error) {
	if t == nil {
		return nil, fmt.Errorf("export: no transaction")
	}
	out := make([]ExportArtifact, 0, len(ExportedItems))
	add := func(name, item string, v any) error {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Errorf("export %s of %s: %w", item, t.ID, err)
		}
		out = append(out, ExportArtifact{Name: name, Item: item, Bytes: append(raw, '\n')})
		return nil
	}




	out = append(out, ExportArtifact{
		Name: "01-proposal.json", Item: "proposal",
		Bytes: append(append(json.RawMessage(nil), t.Proposal...), '\n'),
	})



	if t.Plan != nil {
		body, err := planBody(t.Plan.Steps, t.Plan.Detail)
		if err != nil {
			return nil, fmt.Errorf("export plan of %s: %w", t.ID, err)
		}
		body["plan_digest"] = t.Plan.PlanDigest
		if err := add("03-plan.json", "plan", body); err != nil {
			return nil, err
		}
	}
	if err := add("05-approval.json", "approval", t.Approval); err != nil {
		return nil, err
	}

	if err := add("06-record.json", "record", RecordOf(t)); err != nil {
		return nil, err
	}
	if err := add("09-outcome.json", "outcome", OutcomeOf(t)); err != nil {
		return nil, err
	}


	if r, ok := ReceiptOf(t); ok {
		if err := add("10-receipt.json", "receipt", r); err != nil {
			return nil, err
		}
	}
	return out, nil
}
