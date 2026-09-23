// SPDX-License-Identifier: Apache-2.0

package identity

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/batonos/baton/core/agent/internal/netresolve"
)











const applicationFile = "application.json"


type Application struct {
	RequestID string `json:"request_id"`







	Network    string `json:"network"`
	EntryPoint string `json:"entry_point"`
	Agent       string    `json:"agent"`
	Fingerprint string    `json:"fingerprint"`


	NetworkFingerprint string    `json:"network_fingerprint"`
	AppliedAt          time.Time `json:"applied_at"`
}




func (s *Store) PublicKeyPEM() (string, error) {
	key, err := s.generateKey()
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}





func KeyFingerprint(spkiPEM string) (string, error) {
	block, _ := pem.Decode([]byte(spkiPEM))
	if block == nil {
		return "", errors.New("identity: public key is not PEM")
	}

















	canon := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: block.Bytes})
	sum := sha256.Sum256([]byte(strings.TrimSpace(string(canon))))
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "="), nil
}



type NetworkDescriptor struct {
	NetworkID string `json:"network_id"`
	Identity  struct {
		PublicKey   string `json:"public_key"`
		Fingerprint string `json:"fingerprint"`
	} `json:"identity"`
	Record struct {
		ExpiresAt string `json:"expires_at"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	} `json:"record"`
}






func VerifyEntryPoint(client *http.Client, entryPoint, pin string) (string, error) {
	resp, err := client.Get(strings.TrimRight(entryPoint, "/") + "/api/v1alpha1/network")
	if err != nil {
		return "", fmt.Errorf("identity: reach %s: %w", entryPoint, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", apiFailure(resp.StatusCode, raw)
	}
	var d NetworkDescriptor
	if err := json.Unmarshal(raw, &d); err != nil {
		return "", fmt.Errorf("identity: descriptor is not JSON: %w", err)
	}
	block, _ := pem.Decode([]byte(d.Identity.PublicKey))
	if block == nil {
		return "", errors.New("identity: descriptor carries no PEM public key")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("identity: descriptor key: %w", err)
	}
	pub, ok := pubAny.(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("identity: descriptor key is %T, not ed25519", pubAny)
	}
	payload, err := base64.StdEncoding.DecodeString(d.Record.Payload)
	if err != nil {
		return "", errors.New("identity: descriptor payload is not base64")
	}
	sig, err := base64.StdEncoding.DecodeString(d.Record.Signature)
	if err != nil {
		return "", errors.New("identity: descriptor signature is not base64")
	}
	if !ed25519.Verify(pub, payload, sig) {
		return "", errors.New("identity: the entry point's descriptor is not signed by the key it carries")
	}
	lines := strings.Split(string(payload), "\n")
	if len(lines) < 3 || lines[0] != "network-descriptor" || lines[1] != d.NetworkID || lines[2] != d.Identity.Fingerprint {
		return "", errors.New("identity: the signed bytes do not describe this descriptor")
	}
	if exp, err := time.Parse(time.RFC3339, d.Record.ExpiresAt); err == nil && time.Now().After(exp) {
		return "", fmt.Errorf("identity: the descriptor expired at %s", d.Record.ExpiresAt)
	}

	if fp, err := KeyFingerprint(d.Identity.PublicKey); err != nil || fp != d.Identity.Fingerprint {
		return "", errors.New("identity: the descriptor's fingerprint is not that of its key")
	}
	if pin != "" && d.Identity.Fingerprint != pin {
		return "", fmt.Errorf("identity: this entry point is network %s, not the one named (%s)", d.Identity.Fingerprint, pin)
	}
	return d.Identity.Fingerprint, nil
}





















func ResolveRef(ref, pin string) (entryPoint, usePin string, err error) {
	if netresolve.ChannelOf(ref) == netresolve.ChannelEntryPoint {
		return strings.TrimRight(ref, "/"), pin, nil
	}
	res, rerr := (&netresolve.Resolver{}).Resolve(context.Background(), ref)
	if rerr != nil {
		return "", "", fmt.Errorf("identity: %w", rerr)
	}
	if len(res.Candidates) == 0 {
		return "", "", fmt.Errorf("identity: %s names a network but no endpoint", ref)
	}








	if pin != "" && res.Pin != "" && pin != res.Pin {
		return "", "", fmt.Errorf("identity: %s says the network's key is %s, and the pin given says %s — "+
			"one of them is wrong and this node will not choose", ref, res.Pin, pin)
	}
	if pin == "" {
		pin = res.Pin
	}
	return res.Candidates[0], pin, nil
}

func (s *Store) Apply(client *http.Client, ref, agent, pin string) (*Application, error) {
	if s.Enrolled() {
		return nil, errors.New("identity: this node is already enrolled; it does not apply")
	}
	if existing, err := s.LoadApplication(); err == nil {
		return nil, fmt.Errorf("identity: an application is already outstanding (%s, since %s); wait for it or clear it",
			existing.RequestID, existing.AppliedAt.Format(time.RFC3339))
	}
	entryPoint, pin, err := ResolveRef(ref, pin)
	if err != nil {
		return nil, err
	}
	netFP, verr := VerifyEntryPoint(client, entryPoint, pin)
	if verr != nil {
		return nil, verr
	}
	pub, err := s.PublicKeyPEM()
	if err != nil {
		return nil, err
	}
	fp, err := KeyFingerprint(pub)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"agent": agent, "node_public_key_pem": pub})
	resp, err := client.Post(strings.TrimRight(entryPoint, "/")+"/api/v1alpha1/join-requests", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("identity: reach %s: %w", entryPoint, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return nil, apiFailure(resp.StatusCode, raw)
	}
	var out struct {
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.RequestID == "" {
		return nil, errors.New("identity: the network answered without a request id")
	}
	app := &Application{RequestID: out.RequestID, Network: ref, EntryPoint: strings.TrimRight(entryPoint, "/"), Agent: agent, Fingerprint: fp, NetworkFingerprint: netFP, AppliedAt: time.Now().UTC()}
	if err := s.saveApplication(app); err != nil {
		return nil, err
	}
	return app, nil
}


type ApplicationState string

const (
	ApplicationPending   ApplicationState = "pending"
	ApplicationAdmitted  ApplicationState = "admitted"
	ApplicationCollected ApplicationState = "collected"
	ApplicationDenied    ApplicationState = "denied"
	ApplicationExpired   ApplicationState = "expired"
)


func (s *Store) Poll(client *http.Client, app *Application) (ApplicationState, error) {
	resp, err := client.Get(app.EntryPoint + "/api/v1alpha1/join-requests/" + app.RequestID)
	if err != nil {
		return "", fmt.Errorf("identity: reach %s: %w", app.EntryPoint, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", apiFailure(resp.StatusCode, raw)
	}
	var out struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return ApplicationState(out.State), nil
}


func (s *Store) Collect(client *http.Client, app *Application) (string, error) {
	resp, err := client.Post(app.EntryPoint+"/api/v1alpha1/join-requests/"+app.RequestID+"/collect", "application/json", nil)
	if err != nil {
		return "", fmt.Errorf("identity: reach %s: %w", app.EntryPoint, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", apiFailure(resp.StatusCode, raw)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.Token == "" {
		return "", errors.New("identity: the network answered without a token")
	}
	return out.Token, nil
}


func (s *Store) LoadApplication() (*Application, error) {
	raw, err := os.ReadFile(s.path(applicationFile))
	if err != nil {
		return nil, err
	}
	var app Application
	if err := json.Unmarshal(raw, &app); err != nil {
		return nil, err
	}
	return &app, nil
}


func (s *Store) ClearApplication() error {
	err := os.Remove(s.path(applicationFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (s *Store) saveApplication(app *Application) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(app, "", "  ")
	return os.WriteFile(s.path(applicationFile), raw, 0o600)
}

func apiFailure(status int, raw []byte) error {
	var e struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		Remediation string `json:"remediation"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Code != "" {
		if e.Remediation != "" {
			return fmt.Errorf("%s: %s\n  %s", e.Code, e.Message, e.Remediation)
		}
		return fmt.Errorf("%s: %s", e.Code, e.Message)
	}
	return fmt.Errorf("identity: HTTP %d", status)
}
