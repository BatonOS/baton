// SPDX-License-Identifier: Apache-2.0













package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (


	rootLifetime         = 10 * 365 * 24 * time.Hour
	intermediateLifetime = 5 * 365 * 24 * time.Hour




	DefaultLeafLifetime = 24 * time.Hour


	ServerLifetime = 90 * 24 * time.Hour
)



type Kind string

const (
	KindNode Kind = "node"
	KindUser Kind = "user"
)












type Identity struct {
	TenantID string
	Kind     Kind



	Name string
}


func (i Identity) SANURI() *url.URL {
	return &url.URL{
		Scheme: "baton",
		Host:   i.TenantID,
		Path:   fmt.Sprintf("/%s/%s", i.Kind, i.Name),
	}
}


func (i Identity) IsNode() bool { return i.Kind == KindNode }


func ParseIdentity(cert *x509.Certificate) (Identity, error) {
	for _, u := range cert.URIs {
		if u.Scheme != "baton" {
			continue
		}
		parts := splitPath(u.Path)
		if len(parts) != 2 {
			continue
		}
		kind := Kind(parts[0])
		if kind != KindNode && kind != KindUser {
			continue
		}
		return Identity{TenantID: u.Host, Kind: kind, Name: parts[1]}, nil
	}
	return Identity{}, errors.New("ca: certificate carries no baton:// SAN URI")
}

func splitPath(p string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(p); i++ {
		if i == len(p) || p[i] == '/' {
			if seg := p[start:i]; seg != "" {
				out = append(out, seg)
			}
			start = i + 1
		}
	}
	return out
}







type SigningMaterial struct {
	RootCert         []byte
	RootKey          []byte
	IntermediateCert []byte
	IntermediateKey  []byte
}


func ReadSigningMaterial(dir string) (SigningMaterial, error) {
	var m SigningMaterial
	var err error
	if m.RootCert, err = os.ReadFile(filepath.Join(dir, "root.crt")); err != nil {
		return m, fmt.Errorf("ca: read root.crt: %w", err)
	}
	if m.RootKey, err = os.ReadFile(filepath.Join(dir, "root.key")); err != nil {
		return m, fmt.Errorf("ca: read root.key: %w", err)
	}
	if m.IntermediateCert, err = os.ReadFile(filepath.Join(dir, "intermediate.crt")); err != nil {
		return m, fmt.Errorf("ca: read intermediate.crt: %w", err)
	}
	if m.IntermediateKey, err = os.ReadFile(filepath.Join(dir, "intermediate.key")); err != nil {
		return m, fmt.Errorf("ca: read intermediate.key: %w", err)
	}
	return m, nil
}





func WriteSigningMaterial(dir string, m SigningMaterial) error {
	if len(m.RootCert) == 0 || len(m.RootKey) == 0 || len(m.IntermediateCert) == 0 || len(m.IntermediateKey) == 0 {
		return errors.New("ca: incomplete signing material")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("ca: create dir: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "root.key")); err == nil {
		return errors.New("ca: refusing to install over an existing signing key")
	}
	files := []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"root.crt", m.RootCert, 0o444},
		{"root.key", m.RootKey, 0o400},
		{"intermediate.crt", m.IntermediateCert, 0o444},
		{"intermediate.key", m.IntermediateKey, 0o400},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, f.mode); err != nil {
			return fmt.Errorf("ca: write %s: %w", f.name, err)
		}

		if err := os.Chmod(filepath.Join(dir, f.name), f.mode); err != nil {
			return fmt.Errorf("ca: chmod %s: %w", f.name, err)
		}
	}
	return nil
}





func IsWiped(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "root.crt")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "root.key"))
	return errors.Is(err, os.ErrNotExist)
}





func WipeSigningKeys(dir string) error {
	for _, name := range []string{"root.key", "intermediate.key"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("ca: wipe %s: %w", name, err)
		}
	}
	return nil
}


type CA struct {
	dir string

	root         *x509.Certificate
	rootKey      *ecdsa.PrivateKey
	intermediate *x509.Certificate
	intermedKey  *ecdsa.PrivateKey

	mu      sync.RWMutex
	revoked map[string]struct{}
}







func Open(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ca: create dir: %w", err)
	}
	c := &CA{dir: dir, revoked: map[string]struct{}{}}

	rootPath := filepath.Join(dir, "root.crt")
	if _, err := os.Stat(rootPath); errors.Is(err, os.ErrNotExist) {
		if err := c.bootstrap(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, fmt.Errorf("ca: stat root: %w", err)
	}

	return c, c.load()
}

func (c *CA) bootstrap() error {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("ca: generate root key: %w", err)
	}
	now := time.Now()
	rootTmpl := &x509.Certificate{
		SerialNumber:          mustSerial(),
		Subject:               pkix.Name{CommonName: "BATON Root CA", Organization: []string{"MailLoop BATON"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(rootLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("ca: self-sign root: %w", err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return err
	}

	intKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("ca: generate intermediate key: %w", err)
	}
	intTmpl := &x509.Certificate{
		SerialNumber:          mustSerial(),
		Subject:               pkix.Name{CommonName: "BATON Issuing CA", Organization: []string{"MailLoop BATON"}},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(intermediateLifetime),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	intDER, err := x509.CreateCertificate(rand.Reader, intTmpl, root, &intKey.PublicKey, rootKey)
	if err != nil {
		return fmt.Errorf("ca: sign intermediate: %w", err)
	}

	for _, f := range []struct {
		name string
		blk  *pem.Block
		mode os.FileMode
	}{
		{"root.crt", &pem.Block{Type: "CERTIFICATE", Bytes: rootDER}, 0o444},
		{"root.key", keyBlock(rootKey), 0o400},
		{"intermediate.crt", &pem.Block{Type: "CERTIFICATE", Bytes: intDER}, 0o444},
		{"intermediate.key", keyBlock(intKey), 0o400},
	} {
		path := filepath.Join(c.dir, f.name)
		if err := os.WriteFile(path, pem.EncodeToMemory(f.blk), f.mode); err != nil {
			return fmt.Errorf("ca: write %s: %w", f.name, err)
		}

		if err := os.Chmod(path, f.mode); err != nil {
			return fmt.Errorf("ca: chmod %s: %w", f.name, err)
		}
	}
	return nil
}

func (c *CA) load() error {
	var err error
	if c.root, err = readCert(filepath.Join(c.dir, "root.crt")); err != nil {
		return err
	}
	if c.rootKey, err = readKey(filepath.Join(c.dir, "root.key")); err != nil {
		return err
	}
	if c.intermediate, err = readCert(filepath.Join(c.dir, "intermediate.crt")); err != nil {
		return err
	}
	if c.intermedKey, err = readKey(filepath.Join(c.dir, "intermediate.key")); err != nil {
		return err
	}
	return nil
}






func (c *CA) IssueFromCSR(csrPEM []byte, id Identity, lifetime time.Duration) (certPEM []byte, cert *x509.Certificate, err error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, nil, errors.New("ca: expected a PEM CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: parse CSR: %w", err)
	}


	if err := csr.CheckSignature(); err != nil {
		return nil, nil, fmt.Errorf("ca: CSR signature invalid: %w", err)
	}

	if lifetime <= 0 {
		lifetime = DefaultLeafLifetime
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: mustSerial(),
		Subject:      pkix.Name{CommonName: id.Name},



		NotBefore:   now.Add(-5 * time.Minute),
		NotAfter:    now.Add(lifetime),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:        []*url.URL{id.SANURI()},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.intermediate, csr.PublicKey, c.intermedKey)
	if err != nil {
		return nil, nil, fmt.Errorf("ca: sign: %w", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), parsed, nil
}










func (c *CA) IssueServerForCSR(csrPEM []byte, hosts []string, lifetime time.Duration) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("ca: expected a PEM CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ca: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("ca: CSR signature invalid: %w", err)
	}
	if lifetime <= 0 {
		lifetime = ServerLifetime
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: mustSerial(),
		Subject:      pkix.Name{CommonName: "baton-standby"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(lifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.intermediate, csr.PublicKey, c.intermedKey)
	if err != nil {
		return nil, fmt.Errorf("ca: sign serving certificate: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}


func (c *CA) IssueServer(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: mustSerial(),
		Subject:      pkix.Name{CommonName: "baton-control-api"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(ServerLifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.intermediate, &key.PublicKey, c.intermedKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(keyBlock(key)), nil
}






func (c *CA) RootCertPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.root.Raw}))
}

func (c *CA) Bundle() []byte {
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.intermediate.Raw})
	return append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.root.Raw})...)
}


func (c *CA) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.root)
	pool.AddCert(c.intermediate)
	return pool
}


func (c *CA) LoadRevoked(serials []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range serials {
		c.revoked[s] = struct{}{}
	}
}






func (c *CA) Revoke(serial string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revoked[serial] = struct{}{}
}


func (c *CA) IsRevoked(serial string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, found := c.revoked[serial]
	return found
}




func (c *CA) VerifyPeer(rawCerts [][]byte, _ [][]*x509.Certificate) error {









	if len(rawCerts) == 0 {
		return nil
	}
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("ca: parse peer certificate: %w", err)
	}
	if c.IsRevoked(SerialString(leaf)) {
		return errors.New("ca: certificate has been revoked")
	}
	return nil
}


func SerialString(cert *x509.Certificate) string { return cert.SerialNumber.Text(16) }


func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

func mustSerial() *big.Int {

	max := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		panic(fmt.Sprintf("ca: serial generation failed: %v", err))
	}
	return n
}

func keyBlock(key *ecdsa.PrivateKey) *pem.Block {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		panic(fmt.Sprintf("ca: marshal key: %v", err))
	}
	return &pem.Block{Type: "EC PRIVATE KEY", Bytes: der}
}

func readCert(path string) (*x509.Certificate, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ca: read %s: %w", filepath.Base(path), err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("ca: %s is not PEM", filepath.Base(path))
	}
	return x509.ParseCertificate(block.Bytes)
}

func readKey(path string) (*ecdsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ca: read %s: %w", filepath.Base(path), err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("ca: %s is not PEM", filepath.Base(path))
	}
	return x509.ParseECPrivateKey(block.Bytes)
}







func (c *CA) IssueAdmin() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()


	tmpl := &x509.Certificate{
		SerialNumber: mustSerial(),
		Subject:      pkix.Name{CommonName: "baton-admin"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs: []*url.URL{
			(Identity{TenantID: "default", Kind: KindUser, Name: "admin"}).SANURI(),
		},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.intermediate, &key.PublicKey, c.intermedKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(keyBlock(key)), nil
}









func (c *CA) IssueSelfSigned(id Identity, lifetime time.Duration) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: mustSerial(),
		Subject:      pkix.Name{CommonName: id.Name},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(lifetime),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{id.SANURI()},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.intermediate, &key.PublicKey, c.intermedKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(keyBlock(key)), nil
}


func ParseCertificatePEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("ca: not a PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}
