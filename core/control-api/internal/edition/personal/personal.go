// SPDX-License-Identifier: Apache-2.0







package personal

import (
	"context"
	"fmt"

	"github.com/batonos/baton/core/control-api/internal/policy/simple"
	"github.com/batonos/baton/core/control-api/internal/store/sqlite"
	"github.com/batonos/baton/core/pkg/spi/edition"
	spistore "github.com/batonos/baton/core/pkg/spi/store"
)


const Name = "personal"


type Provider struct{}


func (Provider) Name() string { return Name }







func (Provider) Features() []string {
	return []string{
		"node.enroll",
		"node.revoke",
		"capability.registry",
		"capability.invoke",
		"console.audit",
		"audit.chain",
		"ha.mirror",
	}
}


func (Provider) Build(ctx context.Context, cfg edition.Config) (edition.Components, error) {
	dbPath, _ := cfg.Settings["db_path"].(string)
	if dbPath == "" {
		return edition.Components{}, fmt.Errorf("personal: db_path setting is required")
	}

	db, err := sqlite.Open(ctx, sqlite.Options{Path: dbPath, ReadOnly: cfg.ReadOnly})
	if err != nil {
		return edition.Components{}, err
	}



	if err := db.Migrate(ctx, sqlite.Migrations); err != nil {
		db.Close()
		return edition.Components{}, err
	}

	return edition.Components{
		Store:  db,
		Policy: simple.New(db),




	}, nil
}



func Store(c edition.Components) (*sqlite.DB, bool) {
	db, ok := c.Store.(*sqlite.DB)
	return db, ok
}

func init() { edition.Register(Provider{}) }

var _ spistore.Store = (*sqlite.DB)(nil)
