// SPDX-License-Identifier: Apache-2.0












package edition

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/ha"
	"github.com/batonos/baton/core/pkg/spi/policy"
	"github.com/batonos/baton/core/pkg/spi/store"
)












type Components struct {
	Store    store.Store
	Policy   policy.Decider
	Audit    audit.Sink
	Elector  ha.LeaderElector
	Replicas ha.ReplicationProvider
	Failover ha.FailoverCoordinator
}


type Config struct {
	DataDir  string
	TenantID string


	ReadOnly bool

	Settings map[string]any
}


type Provider interface {


	Name() string




	Features() []string
	Build(ctx context.Context, cfg Config) (Components, error)
}

var (
	mu        sync.RWMutex
	providers = map[string]Provider{}
)



func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	name := p.Name()
	if _, dup := providers[name]; dup {
		panic(fmt.Sprintf("edition: %q registered twice", name))
	}
	providers[name] = p
}







func Get(name string) (Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := providers[name]
	if !ok {
		return nil, fmt.Errorf("edition %q is not part of this binary (available: %v)", name, names())
	}
	return p, nil
}


func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	return names()
}

func names() []string {
	out := make([]string, 0, len(providers))
	for n := range providers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
