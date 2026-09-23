// SPDX-License-Identifier: Apache-2.0













package enroll

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
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
	"strings"
	"time"
)


type Identity struct {
	NodeID       string    `json:"node_id"`
	DisplayName  string    `json:"display_name"`
	Roles        []string  `json:"roles"`
	MasterState  string    `json:"master_state,omitempty"`
	ClusterID    string    `json:"cluster_id"`
	CertNotAfter time.Time `json:"cert_not_after"`
	EnrolledAt   time.Time `json:"enrolled_at"`
}


type Request struct {
	DataDir     string
	MasterURL   string
	Token       string
	DisplayName string
	Roles       []string
	MasterState string
	Version     string





	ServingHosts []string
}


func Enrolled(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "pki", "node.crt"))
	return err == nil
}






func EnrollIfNeeded(req Request) (*Identity, error) {
	pkiDir := filepath.Join(req.DataDir, "pki")
	if Enrolled(req.DataDir) {
		return load(pkiDir)
	}
	if req.Token == "" {
		return nil, fmt.Errorf(
			"enroll: %s is not enrolled and no token was provided; "+
				"set BATON_ENROLLMENT_TOKEN or BATON_ENROLLMENT_TOKEN_FILE", req.DisplayName)
	}

	if err := os.MkdirAll(pkiDir, 0o700); err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("enroll: generate key: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}


	keyPath := filepath.Join(pkiDir, "node.key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(
		&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		return nil, err
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: req.DisplayName}}, key)
	if err != nil {
		return nil, fmt.Errorf("enroll: create CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	body, err := json.Marshal(map[string]any{
		"token":         req.Token,
		"csr_pem":       string(csrPEM),
		"display_name":  req.DisplayName,
		"roles":         req.Roles,
		"master_state":  req.MasterState,
		"serving_hosts": req.ServingHosts,
		"agent_version": req.Version,
		"platform":      runtime.GOOS,
		"arch":          runtime.GOARCH,
		"client_ts":     time.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}




	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Post(req.MasterURL+"/api/v1alpha1/enroll", "application/json",
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("enroll: reach %s: %w", req.MasterURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusCreated {
		var apiErr struct {
			Code        string `json:"code"`
			Message     string `json:"message"`
			Remediation string `json:"remediation"`
		}
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Code != "" {


			return nil, fmt.Errorf("enroll: %s: %s\n  %s", apiErr.Code, apiErr.Message, apiErr.Remediation)
		}
		return nil, fmt.Errorf("enroll: %s: %s", resp.Status, string(raw))
	}

	var out struct {
		NodeID               string    `json:"node_id"`
		CertificatePEM       string    `json:"certificate_pem"`
		CABundlePEM          string    `json:"ca_bundle_pem"`
		CertNotAfter         time.Time `json:"cert_not_after"`
		ClusterID            string    `json:"cluster_id"`
		ServerCertificatePEM string    `json:"server_certificate_pem"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("enroll: parse response: %w", err)
	}

	if err := os.WriteFile(filepath.Join(pkiDir, "node.crt"), []byte(out.CertificatePEM), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(pkiDir, "ca.crt"), []byte(out.CABundlePEM), 0o644); err != nil {
		return nil, err
	}





	if out.ServerCertificatePEM != "" {
		tlsDir := filepath.Join(req.DataDir, "tls")
		if err := os.MkdirAll(tlsDir, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(tlsDir, "server.crt"),
			[]byte(out.ServerCertificatePEM), 0o600); err != nil {
			return nil, err
		}


		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, err
		}
		serverKey := filepath.Join(tlsDir, "server.key")
		if err := os.WriteFile(serverKey, keyPEM, 0o600); err != nil {
			return nil, err
		}
		if err := os.Chmod(serverKey, 0o600); err != nil {
			return nil, err
		}
	}

	id := &Identity{
		NodeID: out.NodeID, DisplayName: req.DisplayName,
		Roles: req.Roles, MasterState: req.MasterState,
		ClusterID: out.ClusterID, CertNotAfter: out.CertNotAfter,
		EnrolledAt: time.Now().UTC(),
	}
	meta, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(pkiDir, "identity.json"), meta, 0o600); err != nil {
		return nil, err
	}
	return id, nil
}

func load(pkiDir string) (*Identity, error) {
	raw, err := os.ReadFile(filepath.Join(pkiDir, "identity.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Identity{}, nil
		}
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(raw, &id); err != nil {
		return nil, err
	}
	return &id, nil
}




func TokenFromEnv() (string, error) {
	if v := os.Getenv("BATON_ENROLLMENT_TOKEN"); v != "" {
		return trimSpace(v), nil
	}
	path := os.Getenv("BATON_ENROLLMENT_TOKEN_FILE")
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("enroll: read token from %s: %w", path, err)
	}
	return trimSpace(string(raw)), nil
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}




















func Renew(dataDir string, client *http.Client, masterURL, commonName string) (time.Time, error) {
	pkiDir := filepath.Join(dataDir, "pki")

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return time.Time{}, fmt.Errorf("renew: generate key: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return time.Time{}, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: commonName}}, key)
	if err != nil {
		return time.Time{}, fmt.Errorf("renew: create CSR: %w", err)
	}
	body, err := json.Marshal(map[string]any{
		"csr_pem": string(pem.EncodeToMemory(
			&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
	})
	if err != nil {
		return time.Time{}, err
	}

	resp, err := client.Post(masterURL+"/api/v1alpha1/agent/certificate/renew",
		"application/json", bytes.NewReader(body))
	if err != nil {
		return time.Time{}, fmt.Errorf("renew: reach %s: %w", masterURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return time.Time{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return time.Time{}, fmt.Errorf("renew: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		CertificatePEM string    `json:"certificate_pem"`
		NotAfter       time.Time `json:"not_after"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return time.Time{}, fmt.Errorf("renew: parse response: %w", err)
	}
	if out.CertificatePEM == "" {
		return time.Time{}, errors.New("renew: the control plane returned no certificate")
	}






	if err := os.WriteFile(filepath.Join(pkiDir, "node.key"),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return time.Time{}, err
	}
	if err := os.WriteFile(filepath.Join(pkiDir, "node.crt"), []byte(out.CertificatePEM), 0o600); err != nil {
		return time.Time{}, err
	}
	return out.NotAfter, nil
}







func CertWindow(dataDir string) (notBefore, notAfter time.Time, err error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "pki", "node.crt"))
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return time.Time{}, time.Time{}, errors.New("enroll: node.crt is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return cert.NotBefore, cert.NotAfter, nil
}
