// SPDX-License-Identifier: Apache-2.0







package identity

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)


const (
	keyFile     = "node.key"
	certFile    = "node.crt"
	caFile      = "ca.crt"
	metaFile    = "identity.json"
	revokedFile = "revoked"
)


type Identity struct {
	NodeID           string    `json:"node_id"`
	DisplayName      string    `json:"display_name"`
	Roles            []string  `json:"roles"`
	MasterState      string    `json:"master_state,omitempty"`
	ClusterID        string    `json:"cluster_id"`
	ControlEndpoints []string  `json:"control_endpoints"`





	EntryPoint       string    `json:"entry_point,omitempty"`




	Network string `json:"network,omitempty"`
	CertNotAfter     time.Time `json:"cert_not_after"`
	HeartbeatSec     int       `json:"heartbeat_interval_sec"`
















	LeaderEpoch      int64     `json:"leader_epoch"`
	EnrolledAt       time.Time `json:"enrolled_at"`
}


type Store struct{ dir string }


func NewStore(dataDir string) *Store { return &Store{dir: filepath.Join(dataDir, "pki")} }

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }


func (s *Store) Enrolled() bool {
	_, err := os.Stat(s.path(certFile))
	return err == nil
}







func (s *Store) Revoked() bool {
	_, err := os.Stat(s.path(revokedFile))
	return err == nil
}


func (s *Store) MarkRevoked(reason string) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path(revokedFile),
		[]byte(time.Now().UTC().Format(time.RFC3339)+" "+reason+"\n"), 0o600)
}


func (s *Store) Load() (*Identity, error) {
	raw, err := os.ReadFile(s.path(metaFile))
	if err != nil {
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, fmt.Errorf("identity: parse %s: %w", metaFile, err)
	}
	return &id, nil
}





func (s *Store) SaveMeta(id *Identity) error {
	meta, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(metaFile), meta, 0o600)
}

func (s *Store) CertPath() string { return s.path(certFile) }
func (s *Store) KeyPath() string  { return s.path(keyFile) }
func (s *Store) CAPath() string   { return s.path(caFile) }





func (s *Store) generateKey() (*ecdsa.PrivateKey, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	path := s.path(keyFile)

	if raw, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(raw)
		if block == nil {
			return nil, fmt.Errorf("identity: %s exists but is not PEM", path)
		}
		return x509.ParseECPrivateKey(block.Bytes)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate key: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("identity: write key: %w", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}







func csr(key *ecdsa.PrivateKey, displayName string) ([]byte, error) {
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: displayName},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("identity: create CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}


type EnrollRequest struct {
	MasterURL    string
	Token        string
	DisplayName  string
	Roles        []string
	MasterState  string
	AgentVersion string
	Labels       map[string]string



	CAPin []byte
}

type enrollResponse struct {
	NodeID               string    `json:"node_id"`
	CertificatePEM       string    `json:"certificate_pem"`
	CABundlePEM          string    `json:"ca_bundle_pem"`
	ControlEndpoints     []string  `json:"control_endpoints"`
	CertNotAfter         time.Time `json:"cert_not_after"`
	HeartbeatIntervalSec int       `json:"heartbeat_interval_sec"`
	ClusterID            string    `json:"cluster_id"`
}

type apiError struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation"`
	RequestID   string `json:"request_id"`
}

func (e apiError) Error() string {
	msg := e.Code + ": " + e.Message
	if e.Remediation != "" {
		msg += "\n  " + e.Remediation
	}
	return msg
}


func (s *Store) Enroll(client *http.Client, req EnrollRequest) (*Identity, error) {
	if s.Revoked() {
		return nil, errors.New(
			"identity: this node was revoked; re-enrolling requires an operator to remove " +
				filepath.Join(s.dir, revokedFile))
	}

	key, err := s.generateKey()
	if err != nil {
		return nil, err
	}
	csrPEM, err := csr(key, req.DisplayName)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(map[string]any{
		"token":         req.Token,
		"csr_pem":       string(csrPEM),
		"display_name":  req.DisplayName,
		"roles":         req.Roles,
		"master_state":  req.MasterState,
		"agent_version": req.AgentVersion,
		"platform":      runtime.GOOS,
		"arch":          runtime.GOARCH,
		"labels":        req.Labels,


		"client_ts": time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}





	dial, _, rerr := ResolveRef(req.MasterURL, "")
	if rerr != nil {
		return nil, rerr
	}
	resp, err := client.Post(dial+"/api/v1alpha1/enroll",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("identity: reach control plane at %s (from %s): %w", dial, req.MasterURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusCreated {
		var apiErr apiError
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Code != "" {



			return nil, apiErr
		}
		return nil, fmt.Errorf("identity: enrollment failed: %s: %s", resp.Status, string(raw))
	}

	var er enrollResponse
	if err := json.Unmarshal(raw, &er); err != nil {
		return nil, fmt.Errorf("identity: parse enrollment response: %w", err)
	}

	if err := os.WriteFile(s.path(certFile), []byte(er.CertificatePEM), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.path(caFile), []byte(er.CABundlePEM), 0o644); err != nil {
		return nil, err
	}

	id := &Identity{
		NodeID: er.NodeID, DisplayName: req.DisplayName,
		Roles: req.Roles, MasterState: req.MasterState,
		ClusterID: er.ClusterID, ControlEndpoints: er.ControlEndpoints,



		Network:      req.MasterURL,
		EntryPoint:   dial,
		CertNotAfter: er.CertNotAfter, HeartbeatSec: er.HeartbeatIntervalSec,
		EnrolledAt: time.Now().UTC(),
	}
	meta, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(s.path(metaFile), meta, 0o600); err != nil {
		return nil, err
	}
	return id, nil
}






func (s *Store) Renew(client *http.Client, masterURL string) (time.Time, error) {
	key, err := s.generateKey()
	if err != nil {
		return time.Time{}, err
	}
	id, err := s.Load()
	if err != nil {
		return time.Time{}, err
	}
	csrPEM, err := csr(key, id.DisplayName)
	if err != nil {
		return time.Time{}, err
	}

	body, _ := json.Marshal(map[string]string{"csr_pem": string(csrPEM)})
	resp, err := client.Post(masterURL+"/api/v1alpha1/agent/certificate/renew",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return time.Time{}, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusForbidden {


		_ = s.MarkRevoked("renewal refused by control plane")
		return time.Time{}, errors.New("identity: this node has been revoked")
	}
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("identity: renewal failed: %s: %s", resp.Status, string(raw))
	}

	var out struct {
		CertificatePEM string    `json:"certificate_pem"`
		NotAfter       time.Time `json:"not_after"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return time.Time{}, err
	}
	if err := os.WriteFile(s.path(certFile), []byte(out.CertificatePEM), 0o600); err != nil {
		return time.Time{}, err
	}

	id.CertNotAfter = out.NotAfter
	if meta, err := json.MarshalIndent(id, "", "  "); err == nil {
		_ = os.WriteFile(s.path(metaFile), meta, 0o600)
	}
	return out.NotAfter, nil
}
