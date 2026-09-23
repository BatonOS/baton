// SPDX-License-Identifier: Apache-2.0









package app

import (
	"github.com/batonos/baton/core/pkg/spi/audit"
	"github.com/batonos/baton/core/pkg/spi/console"
	"github.com/batonos/baton/core/pkg/spi/ha"
	"github.com/batonos/baton/core/pkg/spi/policy"
	"github.com/batonos/baton/core/pkg/spi/store"
)




type Options struct {
	Edition string

	Store    store.Store
	Policy   policy.Decider
	Audit    audit.Sink
	Elector  ha.LeaderElector
	Replicas ha.ReplicationProvider
	Failover ha.FailoverCoordinator




	ConsoleCommands []console.Command
}


type Option func(*Options)


func WithEdition(name string) Option { return func(o *Options) { o.Edition = name } }


func WithStore(s store.Store) Option { return func(o *Options) { o.Store = s } }


func WithPolicy(d policy.Decider) Option { return func(o *Options) { o.Policy = d } }


func WithAudit(s audit.Sink) Option { return func(o *Options) { o.Audit = s } }


func WithElector(e ha.LeaderElector) Option { return func(o *Options) { o.Elector = e } }


func WithReplication(r ha.ReplicationProvider) Option { return func(o *Options) { o.Replicas = r } }


func WithFailover(f ha.FailoverCoordinator) Option { return func(o *Options) { o.Failover = f } }


func WithConsoleCommands(cmds ...console.Command) Option {
	return func(o *Options) { o.ConsoleCommands = append(o.ConsoleCommands, cmds...) }
}


func Apply(opts ...Option) Options {
	o := Options{Edition: "personal"}
	for _, fn := range opts {
		fn(&o)
	}
	return o
}
