// SPDX-License-Identifier: Apache-2.0







package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)


const (
	RiskLow     = "low"
	RiskConfirm = "confirm"
	RiskBlocked = "blocked"
)


const (



	MaxOutputBytes = 512 << 10


	MaxConcurrent = 8

	DefaultTimeout = 30 * time.Second
)


var (
	ErrNotFound     = errors.New("capability not found")
	ErrPaused       = errors.New("node is paused")
	ErrTooLarge     = errors.New("output exceeds the size limit")
	ErrTooBusy      = errors.New("too many capabilities running")
	ErrInputInvalid = errors.New("input is not valid for this capability")
)


type Handler func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)


type Capability struct {
	Name         string          `yaml:"name" json:"name"`
	Version      string          `yaml:"version" json:"version"`
	Description  string          `yaml:"description,omitempty" json:"description,omitempty"`
	Risk         string          `yaml:"risk" json:"risk"`
	Inputs       json.RawMessage `yaml:"-" json:"inputs,omitempty"`
	Outputs      json.RawMessage `yaml:"-" json:"outputs,omitempty"`
	AllowFrom    []string        `yaml:"allow_from,omitempty" json:"allow_from,omitempty"`
	AllowEgress  bool            `yaml:"allow_data_egress,omitempty" json:"allow_data_egress,omitempty"`
	TimeoutSec   int             `yaml:"timeout_sec,omitempty" json:"timeout_sec,omitempty"`

	handler Handler
}


func (c Capability) Timeout() time.Duration {
	if c.TimeoutSec <= 0 {
		return DefaultTimeout
	}
	return time.Duration(c.TimeoutSec) * time.Second
}


type Manifest struct {
	APIVersion   string       `yaml:"apiVersion"`
	Capabilities []Capability `yaml:"capabilities"`
}



func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Manifest{APIVersion: "baton.mailloop.dev/v1alpha1"}, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("capability: parse %s: %w", path, err)
	}
	if m.APIVersion != "baton.mailloop.dev/v1alpha1" {
		return nil, fmt.Errorf("capability: %s has apiVersion %q, this build understands baton.mailloop.dev/v1alpha1",
			path, m.APIVersion)
	}
	for i, c := range m.Capabilities {
		if c.Name == "" || c.Version == "" {
			return nil, fmt.Errorf("capability: entry %d in %s needs both name and version", i, path)
		}
		if c.Risk == "" {



			m.Capabilities[i].Risk = RiskConfirm
		}
	}
	return &m, nil
}


type Registry struct {
	mu     sync.RWMutex
	byName map[string]Capability
	paused bool
	sem    chan struct{}

	inflight sync.Map
}


func NewRegistry() *Registry {
	return &Registry{
		byName: map[string]Capability{},
		sem:    make(chan struct{}, MaxConcurrent),
	}
}


func (r *Registry) Register(c Capability, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c.handler = h
	r.byName[c.Name] = c
}


func (r *Registry) Has(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.byName[name]
	return ok
}


func (r *Registry) List() []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Capability, 0, len(r.byName))
	for _, c := range r.byName {
		out = append(out, c)
	}
	return out
}



func (r *Registry) Pause() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = true
}


func (r *Registry) Resume() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paused = false
}


func (r *Registry) Paused() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.paused
}


func (r *Registry) Inflight() int {
	n := 0
	r.inflight.Range(func(any, any) bool { n++; return true })
	return n
}


func (r *Registry) Invoke(ctx context.Context, callID, name string, input json.RawMessage) (json.RawMessage, error) {
	r.mu.RLock()
	c, found := r.byName[name]
	paused := r.paused
	r.mu.RUnlock()

	if !found {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if paused {
		return nil, ErrPaused
	}

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	default:
		return nil, ErrTooBusy
	}

	r.inflight.Store(callID, time.Now())
	defer r.inflight.Delete(callID)




	ctx, cancel := context.WithTimeout(ctx, c.Timeout())
	defer cancel()

	type result struct {
		out json.RawMessage
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {

			if p := recover(); p != nil {
				done <- result{err: fmt.Errorf("capability panicked: %v", p)}
			}
		}()
		out, err := c.handler(ctx, input)
		done <- result{out: out, err: err}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			return nil, res.err
		}
		if len(res.out) > MaxOutputBytes {
			return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(res.out))
		}
		return res.out, nil
	case <-ctx.Done():



		return nil, ctx.Err()
	}
}
