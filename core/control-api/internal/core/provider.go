// SPDX-License-Identifier: Apache-2.0

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)









type ProviderClient struct {
	socket string
	http   *http.Client











	who string
}




const providerStepTimeout = 30 * time.Minute



func NewProviderClient(socket string) *ProviderClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}
	return &ProviderClient{socket: socket, who: "the local Provider", http: &http.Client{Transport: tr, Timeout: providerStepTimeout}}
}




func NewAppClient(appID, socket string) *ProviderClient {
	c := NewProviderClient(socket)
	c.who = fmt.Sprintf("app %q", appID)
	return c
}


type ProviderAnswer struct {
	OperationID  string          `json:"operation_id"`
	Step         string          `json:"step"`
	Status       string          `json:"status"`
	Nonce        string          `json:"nonce"`
	Evidence     json.RawMessage `json:"evidence"`
	AnsweredFrom string          `json:"answered_from"`


	ProviderInstanceID string `json:"provider_instance_id"`
}



var ErrProviderNotReached = errors.New("core: the executor could not be reached; the step was not sent")




type ProviderRefused struct {

	Who     string
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ProviderRefused) Error() string {
	return fmt.Sprintf("%s refused the step (%d %s): %s", e.Who, e.Status, e.Code, e.Message)
}





func (p *ProviderClient) Execute(ctx context.Context, operationID, step, subject, nonce string, params map[string]string) (*ProviderAnswer, error) {












	body, err := json.Marshal(map[string]any{"step": step, "subject": subject, "nonce": nonce, "params": params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://provider/v1/operations/"+operationID, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, fmt.Errorf("%w — %s at %s: %v", ErrProviderNotReached, p.who, p.socket, err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		refused := &ProviderRefused{Who: p.who, Status: resp.StatusCode}
		_ = json.Unmarshal(raw, refused)
		return nil, refused
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d: %s", p.who, resp.StatusCode, bytes.TrimSpace(raw))
	}
	var a ProviderAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("%s gave an unreadable answer: %w", p.who, err)
	}


	if a.OperationID != operationID || a.Step != step {
		return nil, fmt.Errorf("%s answered about %s/%s, not %s/%s", p.who, a.OperationID, a.Step, operationID, step)
	}
	switch a.Status {
	case "succeeded", "failed", "unknown":
	default:
		return nil, fmt.Errorf("%s answered status %q, which is none of the three", p.who, a.Status)
	}
	return &a, nil
}




type QueryResult struct {

	Answer *ProviderAnswer






	NotFound bool









	ReceivedNoResult   bool
	ProviderInstanceID string
}







var ErrNotAnAnswer = errors.New("core: the reply is not an answer about this operation")










func (p *ProviderClient) Query(ctx context.Context, operationID string) (*QueryResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://provider/v1/operations/"+operationID, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, fmt.Errorf("%w — %s at %s: %v", ErrProviderNotReached, p.who, p.socket, err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}


	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered HTTP %d %s", ErrNotAnAnswer, p.who, resp.StatusCode, bytes.TrimSpace(raw))
	}
	var a ProviderAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("%w: %s sent an unreadable body: %v", ErrNotAnAnswer, p.who, err)
	}


	if a.OperationID != operationID {
		return nil, fmt.Errorf("%w: %s answered about %q, not %q", ErrNotAnAnswer, p.who, a.OperationID, operationID)
	}
	if a.ProviderInstanceID == "" {
		return nil, fmt.Errorf("%w: %s named no instance in its reply", ErrNotAnAnswer, p.who)
	}
	switch a.Status {
	case "not_found":
		return &QueryResult{NotFound: true, ProviderInstanceID: a.ProviderInstanceID}, nil
	case "received_no_result":
		return &QueryResult{ReceivedNoResult: true, ProviderInstanceID: a.ProviderInstanceID}, nil
	case "succeeded", "failed", "unknown":
		return &QueryResult{Answer: &a, ProviderInstanceID: a.ProviderInstanceID}, nil
	}
	return nil, fmt.Errorf("%w: %s answered status %q", ErrNotAnAnswer, p.who, a.Status)
}




















func (p *ProviderClient) KnownSteps(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://provider/v1/steps", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, fmt.Errorf("%w — %s at %s: %v", ErrProviderNotReached, p.who, p.socket, err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered HTTP %d %s", ErrNotAnAnswer, p.who, resp.StatusCode, bytes.TrimSpace(raw))
	}



	var body struct {
		Steps *[]string `json:"steps"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("%w: %s sent an unreadable body: %v", ErrNotAnAnswer, p.who, err)
	}
	if body.Steps == nil {
		return nil, fmt.Errorf("%w: %s sent no `steps` (an executor that handles none sends [])", ErrNotAnAnswer, p.who)
	}
	return *body.Steps, nil
}






const environmentTimeout = 5 * time.Second

















func (p *ProviderClient) EnvironmentFacts(ctx context.Context, destination string) ([]GovernanceFact, error) {
	ctx, cancel := context.WithTimeout(ctx, environmentTimeout)
	defer cancel()
	ask := map[string]any{}
	if destination != "" {
		ask["destination"] = destination
	}
	body, err := json.Marshal(ask)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://provider/v1/environment/facts", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, fmt.Errorf("%w — %s at %s: %v", ErrProviderNotReached, p.who, p.socket, err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered HTTP %d %s", ErrNotAnAnswer, p.who, resp.StatusCode, bytes.TrimSpace(raw))
	}




	var answer struct {
		Facts *[]GovernanceFact `json:"facts"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("%w: %s sent an unreadable body: %v", ErrNotAnAnswer, p.who, err)
	}
	if answer.Facts == nil {
		return nil, fmt.Errorf("%w: %s sent no `facts` (one that states none sends [])", ErrNotAnAnswer, p.who)
	}
	return *answer.Facts, nil
}
