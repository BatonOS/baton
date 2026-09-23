// SPDX-License-Identifier: Apache-2.0


package server

import (
	"bytes"
	"context"
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
	"log"
	"log/slog"
	"sync"
	"sync/atomic"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/batonos/baton/core/control-api/internal/blob"
	"github.com/batonos/baton/core/control-api/internal/ca"
	"github.com/batonos/baton/core/control-api/internal/channel"
	"github.com/batonos/baton/core/control-api/internal/config"
	"github.com/batonos/baton/core/control-api/internal/core"
	"github.com/batonos/baton/core/control-api/internal/dispatch"
	"github.com/batonos/baton/core/control-api/internal/enroll"
	"github.com/batonos/baton/core/control-api/internal/eventlog"
	"github.com/batonos/baton/core/control-api/internal/httpapi"
	"github.com/batonos/baton/core/control-api/internal/skills"
	"github.com/batonos/baton/core/control-api/internal/mirror"
	"github.com/batonos/baton/core/control-api/internal/store/sqlite"
	"github.com/batonos/baton/core/pkg/spi/edition"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


type Server struct {
	cfg    config.Config
	logger *slog.Logger

	store  spi.Store
	sqlite *sqlite.DB
	ca     *ca.CA
	hub    *channel.Hub
	log    *eventlog.Log
	api    *httpapi.API
	mirror *mirror.Mirror


	mirrorCancel context.CancelFunc



	mirrorNodeID    string
	mirrorMasterURL string


	epoch atomic.Int64




	role string










	clientCANone bool



	factsOnce sync.Once
	factsFn   core.FactResolver

	httpSrv *http.Server
	version string
}


func New(ctx context.Context, cfg config.Config, logger *slog.Logger, version string) (*Server, error) {
	if err := os.MkdirAll(cfg.Spec.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("server: create data dir: %w", err)
	}

	provider, err := edition.Get(cfg.Spec.Edition)
	if err != nil {


		return nil, err
	}

	components, err := provider.Build(ctx, edition.Config{
		DataDir:  cfg.Spec.DataDir,
		TenantID: spi.DefaultTenant,
		ReadOnly: cfg.ReadOnly(),
		Settings: map[string]any{"db_path": cfg.DBPath()},
	})
	if err != nil {
		return nil, err
	}
	if components.Store == nil || components.Policy == nil {








		if components.Store != nil {
			components.Store.Close()
		}
		return nil, fmt.Errorf("server: edition %q did not supply a store and policy decider",
			provider.Name())
	}




	var authority *ca.CA




	if !cfg.ReadOnly() && !ca.IsWiped(cfg.CADir()) {
		authority, err = ca.Open(cfg.CADir())
		if err != nil {
			return nil, err
		}
	}

	s := &Server{
		cfg:     cfg,
		logger:  logger,
		version: version,
		store:  components.Store,
		ca:     authority,
		hub:    channel.NewHub(),
	}
	s.sqlite, _ = components.Store.(*sqlite.DB)

	clusterID, epoch, err := s.identity(ctx)
	if err != nil {
		return nil, err
	}
	s.epoch.Store(epoch)
	s.log = eventlog.New(components.Store.Events(), epoch)




	selfRoles := spi.Roles{spi.RoleMaster}
	if cfg.Spec.WithAgentRole {
		selfRoles = append(selfRoles, spi.RoleAgent)
	}
	if err := s.registerSelf(ctx, selfRoles); err != nil {
		return nil, fmt.Errorf("server: register self: %w", err)
	}



	if authority == nil {

	} else if serials, err := components.Store.Certs().RevokedSerials(ctx); err == nil {
		authority.LoadRevoked(serials)
		if len(serials) > 0 {
			logger.Info("revocation list restored", "count", len(serials))
		}
	} else if !cfg.ReadOnly() {
		return nil, fmt.Errorf("server: load revoked serials: %w", err)
	}

	s.hub.OnSeqViolation = func(nodeID string, got, want int64) {
		s.log.System(context.Background(), "channel.seq_violation", map[string]any{
			"node_id": nodeID, "got": got, "want": want,
		})
	}

	blobs, blobErr := blob.New(cfg.BlobDir())
	if blobErr != nil {

		logger.Warn("attachment storage unavailable; attachments will be refused",
			"dir", cfg.BlobDir(), "error", blobErr)
		blobs = nil
	}
	s.api = &httpapi.API{
		Cfg: cfg, Store: components.Store, CA: authority, Hub: s.hub,
		Dispatcher: dispatch.New(components.Store, s.hub, components.Policy, s.log),
		Log: s.log, Logger: logger, Version: version,
		Sessions: httpapi.NewSessionStore(),



		SkillCache: skills.NewCache(cfg.Spec.DataDir),





		Blobs: blobs,
		Edition: provider.Name(), Features: provider.Features(),
		ClusterID: clusterID, Epoch: epoch,
	}

	s.api.Promote = s.promote
	s.api.Demote = s.demote




	s.api.TransferOffered = s.transferOffered
	s.api.RecordTransferOffer = s.recordTransferOffer
	s.api.InstallCA = s.installCA
	s.api.EpochFn = s.epoch.Load
	s.api.RestartIdentityFn = s.restartIdentity








	promoted := false
	demoted := false
	if s.sqlite != nil {
		if mode, merr := s.sqlite.ClusterValue(ctx, "mode"); merr == nil {
			switch mode {
			case "primary":
				promoted = true
				s.sqlite.SetWritable(true)
			case "mirror":
				demoted = true
				s.sqlite.SetWritable(false)
			}
		}
	}
	switch {
	case promoted:
		s.role = "primary (promoted; persisted mode overrides env)"
	case demoted:
		s.role = "read-only mirror (demoted; persisted mode overrides env)"
	case cfg.ReadOnly():
		s.role = "read-only mirror (configured)"
	default:
		s.role = "primary"
	}






	if (cfg.ReadOnly() || demoted) && !promoted {
		if err := s.setupMirror(); err != nil {




			if cfg.ReadOnly() {
				return nil, err
			}
			logger.Warn("demoted control plane could not start mirroring the new master; staying read-only", "error", err)
		} else {
			s.api.Mirror = s.mirror
		}
	}

	return s, nil
}






func (s *Server) promote(ctx context.Context) (int64, error) {
	if s.sqlite == nil {
		return 0, errors.New("server: this control plane has no promotable store")
	}
	ep, err := s.sqlite.Promote(ctx)
	if err != nil {
		return 0, err
	}
	if s.mirrorCancel != nil {
		s.mirrorCancel()
		s.mirrorCancel = nil
	}
	s.epoch.Store(ep)





	s.markOwnMasterState(ctx, spi.MasterActive)





	s.demoteOtherMasters(ctx)
	if s.log != nil {
		s.log.System(ctx, "control_plane.promoted", map[string]any{"epoch": ep})
	}
	return ep, nil
}



func (s *Server) demoteOtherMasters(ctx context.Context) {
	nodes, _, err := s.store.Nodes().List(ctx, spi.NodeFilter{Role: spi.RoleMaster})
	if err != nil {
		return
	}
	for _, n := range nodes {
		if n.DisplayName == s.cfg.Metadata.Name || n.MasterState != spi.MasterActive {
			continue
		}
		if err := s.store.Nodes().SetMasterState(ctx, n.NodeID, spi.MasterStandby); err != nil && s.logger != nil {
			s.logger.Warn("could not mark a superseded master as standby", "node", n.DisplayName, "error", err)
		}
	}
}






const clusterKeyTransferOffered = "transfer_offer_signed_at"









func (s *Server) transferOffered(ctx context.Context) (bool, error) {
	if s.sqlite == nil {
		return false, errors.New("server: this control plane has no store to ask")
	}
	v, err := s.sqlite.ClusterValue(ctx, clusterKeyTransferOffered)
	if errors.Is(err, spi.ErrNotFound) {

		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v != "", nil
}


func (s *Server) recordTransferOffer(ctx context.Context) error {
	if s.sqlite == nil {
		return errors.New("server: this control plane has no store to record in")
	}
	return s.sqlite.SetClusterValue(ctx, clusterKeyTransferOffered,
		time.Now().UTC().Format(time.RFC3339))
}







func (s *Server) demote(ctx context.Context, newMaster string) error {
	if s.sqlite == nil {
		return errors.New("server: this control plane has no demotable store")
	}






	s.markOwnMasterState(ctx, spi.MasterStandby)






	if newMaster != "" && s.ca != nil {
		if err := s.mintOwnNodeIdentity(ctx); err != nil {
			s.logger.Warn("could not mint a follower identity on demote; a restart will not mirror the new master", "error", err)
		} else if err := s.writeFollowURL(newMaster); err != nil {
			s.logger.Warn("could not record the new master URL on demote", "error", err)
		}
	}
	if err := s.sqlite.Demote(ctx); err != nil {
		return err
	}







	if err := ca.WipeSigningKeys(s.cfg.CADir()); err != nil && s.logger != nil {
		s.logger.Warn("could not wipe CA signing key on demote", "error", err)
	}
	s.ca = nil
	s.api.CA = nil
	return nil
}






func (s *Server) markOwnMasterState(ctx context.Context, state spi.MasterState) {
	if s.store == nil {
		return
	}
	n, err := s.store.Nodes().GetByName(ctx, spi.DefaultTenant, s.cfg.Metadata.Name)
	if err != nil || n == nil {
		return
	}
	if err := s.store.Nodes().SetMasterState(ctx, n.NodeID, state); err != nil && s.logger != nil {
		s.logger.Warn("could not sync own master_state", "state", string(state), "error", err)
	}
}





func (s *Server) mintOwnNodeIdentity(ctx context.Context) error {
	self, err := s.store.Nodes().GetByName(ctx, spi.DefaultTenant, s.cfg.Metadata.Name)
	if err != nil || self == nil {
		return fmt.Errorf("server: no own registry row to mint an identity for: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: self.NodeID}}, key)
	if err != nil {
		return err
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	certPEM, _, err := s.ca.IssueFromCSR(csrPEM, ca.Identity{TenantID: spi.DefaultTenant, Kind: ca.KindNode, Name: self.NodeID}, ca.DefaultLeafLifetime)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	pkiDir := filepath.Join(s.cfg.Spec.DataDir, "pki")
	if err := os.MkdirAll(pkiDir, 0o700); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"node.crt", certPEM, 0o600},
		{"node.key", keyPEM, 0o600},
		{"ca.crt", s.ca.Bundle(), 0o644},
	} {
		if err := os.WriteFile(filepath.Join(pkiDir, f.name), f.data, f.mode); err != nil {
			return err
		}
	}
	return nil
}





func (s *Server) followURLPath() string {
	return filepath.Join(s.cfg.Spec.DataDir, "follow-master")
}

func (s *Server) writeFollowURL(url string) error {
	return os.WriteFile(s.followURLPath(), []byte(url), 0o600)
}

func (s *Server) readFollowURL() string {
	b, err := os.ReadFile(s.followURLPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}


func (s *Server) identity(ctx context.Context) (clusterID string, epoch int64, err error) {
	if s.sqlite == nil {
		return "unknown", 1, nil
	}
	clusterID, err = s.sqlite.ClusterValue(ctx, "cluster_id")
	if errors.Is(err, spi.ErrNotFound) {
		if s.cfg.ReadOnly() {


			return httpapi.ClusterIDPending, 1, nil
		}
		clusterID = "cluster_" + uuid.NewString()
		if err := s.sqlite.SetClusterValue(ctx, "cluster_id", clusterID); err != nil {
			return "", 0, err
		}
	} else if err != nil {
		return "", 0, err
	}





	ep, err := s.sqlite.LeaderEpoch(ctx)
	if err != nil {
		return "", 0, err
	}
	return clusterID, ep, nil
}
















func (s *Server) registerSelf(ctx context.Context, roles spi.Roles) error {
	if s.sqlite == nil || s.cfg.ReadOnly() {
		return nil
	}












	if net, err := s.store.Networks().EnsureIdentity(ctx, spi.DefaultTenant, spi.DefaultNetworkName); err != nil {
		return fmt.Errorf("network identity: %w", err)
	} else {
		s.logger.Info("network identity", "network_id", net.NetworkID, "fingerprint", net.Fingerprint)
	}
	name := s.cfg.Metadata.Name
	if existing, err := s.store.Nodes().GetByName(ctx, spi.DefaultTenant, name); err == nil && existing != nil {
		return nil
	} else if err != nil && !errors.Is(err, spi.ErrNotFound) {
		return err
	}
	now := time.Now().UTC()
	nodeID := uuid.NewString()









	owner, oerr := s.store.Identities().Ensure(ctx, spi.DefaultTenant, name)
	if oerr != nil {
		return oerr
	}
	if err := s.store.Nodes().Create(ctx, &spi.Node{
		NodeID:       nodeID,
		OwnerIdentityID: owner.IdentityID,
		TenantID:     spi.DefaultTenant,
		DisplayName:  name,
		Roles:        roles,
		MasterState:  spi.MasterActive,
		Deployment:   spi.DeploymentLocal,
		Status:       spi.NodeStatusActive,
		Trust:        spi.TrustSelfBuilt,
		AgentVersion: s.version,
		EnrolledAt:   now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		return err
	}








	if _, err := s.store.Identities().Bind(ctx, spi.DefaultTenant, name, nodeID); err != nil {
		s.logger.Warn("could not register an agent for this control plane",
			"node", name, "error", err)
	}






	if s.ca == nil {
		return nil
	}
	certPEM, _, err := s.ca.IssueSelfSigned(ca.Identity{
		TenantID: spi.DefaultTenant,
		Kind:     ca.KindNode,
		Name:     nodeID,
	}, ca.DefaultLeafLifetime)
	if err != nil {
		return err
	}
	cert, err := ca.ParseCertificatePEM(certPEM)
	if err != nil {
		return err
	}
	return s.store.Certs().Issue(ctx, &spi.Certificate{
		Serial:            ca.SerialString(cert),
		NodeID:            nodeID,
		FingerprintSHA256: ca.Fingerprint(cert),
		SubjectCN:         cert.Subject.CommonName,
		SANURI:            cert.URIs[0].String(),
		NotBefore:         cert.NotBefore,
		NotAfter:          cert.NotAfter,
		IssuedAt:          now,
	})
}

func (s *Server) setupMirror() error {
	if s.sqlite == nil {
		return errors.New("server: mirror mode requires the SQLite backend in this release")
	}



	masterURL := s.readFollowURL()
	if masterURL == "" {
		masterURL = s.cfg.Spec.MasterURL
	}
	if masterURL == "" {
		return errors.New("server: mirror mode needs a master URL (BATON_MASTER_URL or a recorded follow target)")
	}






	var token string
	if !enroll.Enrolled(s.cfg.Spec.DataDir) {
		var terr error
		if token, terr = enroll.TokenFromEnv(); terr != nil {
			return terr
		}
	}
	id, err := enroll.EnrollIfNeeded(enroll.Request{
		DataDir:     s.cfg.Spec.DataDir,
		MasterURL:   masterURL,
		Token:       token,
		DisplayName: s.cfg.Metadata.Name,


		Roles:       []string{"master"},
		MasterState: "standby",
		Version:     s.version,




		ServingHosts: append([]string{hostFromURL(s.cfg.Spec.AdvertiseURL)}, s.cfg.Spec.TLSSANs...),
	})
	if err != nil {
		return err
	}
	if id.NodeID != "" {
		s.logger.Info("mirror identity",
			"node_id", id.NodeID, "cluster_id", id.ClusterID,
			"cert_not_after", id.CertNotAfter.Format(time.RFC3339))
	}

	client, err := s.nodeClient()
	if err != nil {
		return err
	}
	s.mirror = mirror.New(mirror.Options{
		MasterURL:   masterURL,
		Client:      client,
		Interval:    s.cfg.SnapshotInterval(),
		SnapshotDir: s.cfg.SnapshotDir(),
		Logger:      s.logger.With("component", "mirror"),
		Restore:     s.sqlite.RestoreFrom,
		Record:      s.store.Snapshots().Record,
	})
	if id != nil {
		s.mirrorNodeID = id.NodeID
	}
	s.mirrorMasterURL = masterURL
	return nil
}

















func (s *Server) renewMirrorCert(ctx context.Context) {
	if s.mirror == nil || s.mirrorNodeID == "" {
		return
	}
	log := s.logger.With("component", "mirror-cert")



	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		notBefore, notAfter, err := enroll.CertWindow(s.cfg.Spec.DataDir)
		if err != nil {
			log.Warn("cannot read the mirror certificate to check its expiry", "error", err)
		} else if life := notAfter.Sub(notBefore); life > 0 && time.Until(notAfter) < life/3 {
			if time.Now().After(notAfter) {




				log.Error("the mirror certificate expired before it was renewed; snapshots cannot resume without re-enrolment",
					"not_after", notAfter.Format(time.RFC3339),
					"remedy", "re-enrol this standby with a fresh token")
			} else if newNotAfter, rerr := enroll.Renew(
				s.cfg.Spec.DataDir, s.mirrorRenewClient(), s.mirrorMasterURL, s.mirrorNodeID); rerr != nil {
				log.Warn("mirror certificate renewal failed; will retry",
					"error", rerr, "not_after", notAfter.Format(time.RFC3339))
			} else if c, cerr := s.nodeClient(); cerr != nil {


				log.Warn("renewed the mirror certificate but could not rebuild its client", "error", cerr)
			} else {
				s.mirror.SetClient(c)
				log.Info("mirror certificate renewed",
					"not_after", newNotAfter.Format(time.RFC3339))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}




func (s *Server) mirrorRenewClient() *http.Client {
	c, err := s.nodeClient()
	if err != nil {



		return &http.Client{Timeout: 30 * time.Second}
	}
	return c
}










func (s *Server) installCA(ctx context.Context, publicKey, payload, signature string) error {
	if s.ca != nil {
		return nil
	}
	if s.cfg.Spec.MasterURL == "" {
		return errors.New("no master URL to fetch the CA signing key from")
	}
	client, err := s.nodeClient()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{
		"public_key": publicKey, "payload": payload, "signature": signature,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.Spec.MasterURL+"/api/v1alpha1/ha/ca-bundle", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("master answered %s: %s", resp.Status, string(b))
	}
	var out struct {
		RootCrt         string `json:"root_crt"`
		RootKey         string `json:"root_key"`
		IntermediateCrt string `json:"intermediate_crt"`
		IntermediateKey string `json:"intermediate_key"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return err
	}
	mat := ca.SigningMaterial{
		RootCert:         []byte(out.RootCrt),
		RootKey:          []byte(out.RootKey),
		IntermediateCert: []byte(out.IntermediateCrt),
		IntermediateKey:  []byte(out.IntermediateKey),
	}
	if err := ca.WriteSigningMaterial(s.cfg.CADir(), mat); err != nil {
		return err
	}
	authority, err := ca.Open(s.cfg.CADir())
	if err != nil {
		return err
	}
	s.ca = authority
	s.api.CA = authority
	if serials, err := s.store.Certs().RevokedSerials(ctx); err == nil {
		authority.LoadRevoked(serials)
	}
	if s.logger != nil {
		s.logger.Info("installed the network CA signing key from the master (transfer)")
	}
	return nil
}

func (s *Server) nodeClient() (*http.Client, error) {
	certPath := filepath.Join(s.cfg.Spec.DataDir, "pki", "node.crt")
	keyPath := filepath.Join(s.cfg.Spec.DataDir, "pki", "node.key")
	caPath := filepath.Join(s.cfg.Spec.DataDir, "pki", "ca.crt")

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("server: mirror needs an enrolled identity at %s: %w", certPath, err)
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("server: read cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("server: cluster CA bundle is not valid PEM")
	}

	return &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				RootCAs:      pool,
				MinVersion:   tls.VersionTLS12,
			},
		},
	}, nil
}








func (s *Server) tlsConfig() (*tls.Config, error) {
	certPath := filepath.Join(s.cfg.TLSDir(), "server.crt")
	keyPath := filepath.Join(s.cfg.TLSDir(), "server.key")

	hosts := dedupe(append([]string{
		"localhost", "127.0.0.1", s.cfg.Metadata.Name,
		hostFromURL(s.cfg.Spec.AdvertiseURL),
	}, s.cfg.Spec.TLSSANs...))






























	if covered, missing := certCovers(certPath, hosts); !covered && s.ca != nil {










		for _, f := range []string{certPath, keyPath} {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		if len(missing) > 0 {
			s.logger.Info("serving certificate does not cover the configured names; reissuing",
				"missing", missing, "hosts", hosts)
		}
	}

	if _, err := os.Stat(certPath); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(s.cfg.TLSDir(), 0o700); err != nil {
			return nil, err
		}
		if s.ca == nil {
			return nil, fmt.Errorf(
				"server: this mirror has no serving certificate at %s; it is issued by the "+
					"primary during enrolment. Remove %s and let it enrol again",
				certPath, s.cfg.Spec.DataDir)
		}
		certPEM, keyPEM, err := s.ca.IssueServer(hosts)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(certPath, certPEM, 0o444); err != nil {
			return nil, err
		}
		if err := os.WriteFile(keyPath, keyPEM, 0o400); err != nil {
			return nil, err
		}
		_ = os.Chmod(keyPath, 0o400)
		s.logger.Info("issued server certificate", "hosts", hosts)
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("server: load serving certificate: %w", err)
	}

	pool := x509.NewCertPool()
	if s.ca != nil {
		pool = s.ca.Pool()
	} else if bundle, err := os.ReadFile(filepath.Join(s.cfg.Spec.DataDir, "pki", "ca.crt")); err == nil {
		pool.AppendCertsFromPEM(bundle)
	}






	if len(pool.Subjects()) == 0 {
		s.clientCANone = true
		s.logger.Warn("this control plane holds NO client-CA — every certificate will be refused",
			"role", s.role,
			"why", "no signing authority here and no ca.crt in the data directory",
			"consequence", "operators and every node will be told their certificate is signed by an unknown authority, which is true and points at the wrong thing",
			"remedy", "give this node the cluster CA bundle, or point it at the master it should mirror (BATON_MASTER_URL)")
	}

	verifyPeer := func(raw [][]byte, chains [][]*x509.Certificate) error { return nil }
	if s.ca != nil {
		verifyPeer = s.ca.VerifyPeer
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		ClientCAs:    pool,


		ClientAuth: tls.VerifyClientCertIfGiven,




		VerifyPeerCertificate: verifyPeer,
	}, nil
}



























func (s *Server) AdminCertificate(ctx context.Context) (certPEM, keyPEM []byte, err error) {
	if s.ca == nil {
		return nil, nil, errors.New("server: a mirror cannot issue credentials; use the primary")
	}
	certPEM, keyPEM, err = s.ca.IssueAdmin()
	if err != nil {
		return nil, nil, err
	}
	cert, err := ca.ParseCertificatePEM(certPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("server: read the name back off the operator certificate: %w", err)
	}
	if cert.Subject.CommonName == "" {
		return nil, nil, errors.New("server: the operator certificate carries no CommonName, so it names no address")
	}
	if _, err := s.store.Identities().Ensure(ctx, spi.DefaultTenant, cert.Subject.CommonName); err != nil {
		return nil, nil, fmt.Errorf("server: make %q addressable: %w", cert.Subject.CommonName, err)
	}
	return certPEM, keyPEM, nil
}


func (s *Server) Run(ctx context.Context) error {
	tlsCfg, err := s.tlsConfig()
	if err != nil {
		return err
	}

	s.httpSrv = &http.Server{
		Addr:      s.cfg.Spec.ListenAddress,
		Handler:   s.api.Routes(),
		TLSConfig: tlsCfg,



		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          log.New(&handshakeLog{s: s}, "", 0),
	}

	listener, err := net.Listen("tcp", s.cfg.Spec.ListenAddress)
	if err != nil {
		return fmt.Errorf("server: listen on %s: %w", s.cfg.Spec.ListenAddress, err)
	}

	if s.mirror != nil {
		mctx, cancel := context.WithCancel(ctx)
		s.mirrorCancel = cancel
		go s.mirror.Run(mctx)


		go s.renewMirrorCert(mctx)
	}





	if s.cfg.Spec.ProviderSocket != "" {
		pc := core.NewProviderClient(s.cfg.Spec.ProviderSocket)
		if err := core.CheckWorkspaceBoundary(ctx, pc, s.cfg.Spec.WorkspaceBoundary, s.logger); err != nil {
			return err
		}
	}
	s.api.Facts = s.facts
	go s.retentionLoop(ctx)
	go s.offlineSweep(ctx)
	go s.transactionLoop(ctx)



	if httpapi.RegistryConfigured() {
		go s.registryHeartbeat(ctx)
	}

	s.logger.Info("control plane listening",
		"address", s.cfg.Spec.ListenAddress, "mode", string(s.cfg.Spec.Mode),
		"edition", s.api.Edition, "cluster_id", s.api.ClusterID)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpSrv.ServeTLS(listener, "", "")
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		return s.shutdown()
	}
}

func (s *Server) shutdown() error {
	s.logger.Info("shutting down")



	s.hub.CloseAll("control plane shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := s.httpSrv.Shutdown(ctx)
	if closeErr := s.store.Close(); closeErr != nil {
		s.logger.Warn("closing store", "error", closeErr)
	}
	return err
}





func (s *Server) retentionLoop(ctx context.Context) {


	if s.cfg.ReadOnly() {
		return
	}













	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	sweep := func() {
		if n, err := s.store.Messages().Expire(ctx, time.Now().Unix()); err != nil {
			s.logger.Warn("message retention sweep", "error", err)
		} else if n > 0 {

			s.logger.Info("cleared expired message bodies", "count", n)
		}
		if s.cfg.Spec.RetentionDays <= 0 {
			return
		}
		cutoff := time.Now().AddDate(0, 0, -s.cfg.Spec.RetentionDays).Unix()
		n, err := s.store.Events().PruneBefore(ctx, cutoff)
		if err != nil {
			s.logger.Warn("event retention sweep", "error", err)
			return
		}
		if n > 0 {
			s.logger.Info("pruned events", "count", n, "older_than_days", s.cfg.Spec.RetentionDays)
		}
	}



	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}






func (s *Server) offlineSweep(ctx context.Context) {
	if s.cfg.ReadOnly() {
		return
	}
	ticker := time.NewTicker(channel.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:





			if err := s.store.Nodes().Touch(ctx, s.cfg.Metadata.Name, time.Now()); err != nil &&
				!errors.Is(err, spi.ErrNotFound) {
				s.logger.Warn("touch self", "error", err)
			}

			deadline := time.Now().Add(
				-channel.HeartbeatInterval * channel.MissedHeartbeatsBeforeOffline).Unix()



			mirrorWindow := time.Duration(s.cfg.Spec.SnapshotIntervalSec) * time.Second
			if mirrorWindow <= 0 {
				mirrorWindow = 60 * time.Second
			}
			mirrorDeadline := time.Now().Add(-mirrorWindow * channel.MissedHeartbeatsBeforeOffline).Unix()
			changed, err := s.store.Nodes().MarkStaleOffline(ctx, deadline, mirrorDeadline)
			if err != nil {
				s.logger.Warn("offline sweep", "error", err)
				continue
			}
			for _, nodeID := range changed {
				s.log.System(ctx, "node.offline", map[string]any{"node_id": nodeID})
			}
		}
	}
}












func certCovers(path string, hosts []string) (bool, []string) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return false, nil
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return false, nil
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, nil
	}
	var missing []string
	for _, h := range hosts {
		if h == "" {
			continue
		}
		found := false
		if ip := net.ParseIP(h); ip != nil {
			for _, got := range leaf.IPAddresses {
				if got.Equal(ip) {
					found = true
					break
				}
			}
		} else {
			for _, got := range leaf.DNSNames {
				if got == h {
					found = true
					break
				}
			}
		}
		if !found {
			missing = append(missing, h)
		}
	}
	return len(missing) == 0, missing
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func hostFromURL(raw string) string {
	if raw == "" {
		return ""
	}


	trimmed := raw
	for _, prefix := range []string{"https://", "http://"} {
		if len(trimmed) > len(prefix) && trimmed[:len(prefix)] == prefix {
			trimmed = trimmed[len(prefix):]
			break
		}
	}
	if idx := indexAny(trimmed, ":/"); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	return trimmed
}

func indexAny(s, chars string) int {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return i
			}
		}
	}
	return -1
}

var _ = pem.Block{}


func (s *Server) CABundle() []byte {
	if s.ca == nil {
		return nil
	}
	return s.ca.Bundle()
}


func (s *Server) Close() error { return s.store.Close() }











func (s *Server) registryHeartbeat(ctx context.Context) {
	beat := func() {
		if err := s.api.Heartbeat(ctx); err != nil {
			s.logger.Debug("registry heartbeat", "error", err)
		}



























		fetched, landed, err := s.api.CollectSpool(ctx)
		if err != nil {
			s.logger.Debug("collect from the offline mailbox", "error", err)
			return
		}
		if fetched > 0 {



			s.logger.Info("collected from the offline mailbox",
				"fetched", fetched, "landed", landed)
		}
	}
	beat()
	ticker := time.NewTicker(httpapi.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			beat()
		}
	}
}




















type handshakeLog struct{ s *Server }

func (h *handshakeLog) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	if h.s.clientCANone && strings.Contains(line, "unknown authority") {
		h.s.logger.Warn("refused a client certificate, and the certificate is probably fine",
			"detail", line,
			"role", h.s.role,
			"cause", "this control plane holds no client-CA, so it can verify nobody",
			"not_the_cause", "the client certificate — do not go and check it",
			"remedy", "restore the cluster CA bundle here, or point this node at the master it should mirror (BATON_MASTER_URL)")
		return len(p), nil
	}
	h.s.logger.Warn(line)
	return len(p), nil
}
















func (s *Server) restartIdentity() string {


	if s.ca != nil {
		return "same"
	}


	if !ca.IsWiped(s.cfg.CADir()) {


		if _, err := os.Stat(filepath.Join(s.cfg.Spec.DataDir, "pki", "ca.crt")); err == nil {
			return "same"
		}
		return "would-refuse-every-client"
	}


	if _, err := os.Stat(filepath.Join(s.cfg.Spec.DataDir, "pki", "ca.crt")); err == nil {
		return "same"
	}
	return "would-refuse-every-client"
}












func (s *Server) ExportTransaction(ctx context.Context, txID, dir string) ([]string, error) {
	t, err := s.store.Transactions().Get(ctx, txID)
	if err != nil {
		return nil, err
	}
	arts, err := core.ExportTransaction(t)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	written := make([]string, 0, len(arts))
	for _, a := range arts {
		if err := os.WriteFile(filepath.Join(dir, a.Name), a.Bytes, 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", a.Name, err)
		}
		written = append(written, a.Item)
	}
	return written, nil
}
