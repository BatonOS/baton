// SPDX-License-Identifier: Apache-2.0

















package grantproof

import (
	"crypto/ed25519"
	"strings"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)



const purpose = "baton-grant-v1"




func Canonical(g spi.Grant) []byte {
	ts := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.RFC3339)
	}
	return []byte(strings.Join([]string{
		purpose,
		g.TenantID,
		g.GrantID,
		g.Grantor,
		g.Grantee,
		g.Action,
		g.Object,
		g.Scope,
		g.Effect,
		g.Constraints,
		ts(g.ValidFrom),
		ts(g.ValidUntil),
	}, "\n"))
}



func Sign(priv ed25519.PrivateKey, g spi.Grant) []byte {
	return ed25519.Sign(priv, Canonical(g))
}



func Verify(pub ed25519.PublicKey, g spi.Grant) bool {
	if len(g.Proof) == 0 {
		return false
	}
	return ed25519.Verify(pub, Canonical(g), g.Proof)
}
