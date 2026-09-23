// SPDX-License-Identifier: Apache-2.0









package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


type Mirror struct {
	masterURL   string
	client      *http.Client
	interval    time.Duration
	snapshotDir string
	logger      *slog.Logger



	restore func(path string) error

	record func(ctx context.Context, s *spi.Snapshot) error

	mu sync.RWMutex


	lastApplied time.Time
	lastError   string
}


type Options struct {
	MasterURL   string
	Client      *http.Client
	Interval    time.Duration
	SnapshotDir string
	Logger      *slog.Logger
	Restore     func(path string) error
	Record      func(ctx context.Context, s *spi.Snapshot) error
}


func New(o Options) *Mirror {
	if o.Interval <= 0 {
		o.Interval = 60 * time.Second
	}
	return &Mirror{
		masterURL:   o.MasterURL,
		client:      o.Client,
		interval:    o.Interval,
		snapshotDir: o.SnapshotDir,
		logger:      o.Logger,
		restore:     o.Restore,
		record:      o.Record,
	}
}


func (m *Mirror) Run(ctx context.Context) {
	if err := os.MkdirAll(m.snapshotDir, 0o700); err != nil {
		m.logger.Error("snapshot directory", "error", err)
		return
	}



	m.pullOnce(ctx)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pullOnce(ctx)
		}
	}
}

func (m *Mirror) pullOnce(ctx context.Context) {
	started := time.Now()
	if err := m.pull(ctx); err != nil {
		m.mu.Lock()
		m.lastError = err.Error()
		m.mu.Unlock()


		m.logger.Warn("snapshot pull failed",
			"error", err, "master", m.masterURL,
			"lag_seconds", m.LagSeconds())
		return
	}

	m.mu.Lock()
	m.lastApplied = time.Now()
	m.lastError = ""
	m.mu.Unlock()

	m.logger.Info("snapshot applied", "duration_ms", time.Since(started).Milliseconds())
}

func (m *Mirror) pull(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		m.masterURL+"/api/v1alpha1/ha/snapshot", nil)
	if err != nil {
		return err
	}
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("reach primary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("primary returned %s: %s", resp.Status, string(body))
	}

	expected := resp.Header.Get("X-Baton-Snapshot-Sha256")
	if expected == "" {



		return fmt.Errorf("primary did not send a snapshot digest")
	}

	tmpPath := filepath.Join(m.snapshotDir, "incoming-"+uuid.NewString()+".db")
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}


	defer os.Remove(tmpPath)

	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("download snapshot: %w", err)
	}
	if closeErr != nil {
		return closeErr
	}

	got := hex.EncodeToString(h.Sum(nil))
	if got != expected {
		return fmt.Errorf("snapshot digest mismatch: primary said %s, received %s", expected, got)
	}

	if err := m.restore(tmpPath); err != nil {
		return fmt.Errorf("install snapshot: %w", err)
	}

	if m.record != nil {
		applied := time.Now()


		_ = m.record(ctx, &spi.Snapshot{
			ID: "snap_" + uuid.NewString(), TakenAt: applied,
			SizeBytes: size, SHA256: got, AppliedAt: &applied,
		})
	}
	return nil
}







func (m *Mirror) LagSeconds() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.lastApplied.IsZero() {
		return -1
	}
	return time.Since(m.lastApplied).Seconds()
}




func (m *Mirror) httpClient() *http.Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.client
}




func (m *Mirror) SetClient(c *http.Client) {
	if c == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.client = c
}


func (m *Mirror) LastError() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}
