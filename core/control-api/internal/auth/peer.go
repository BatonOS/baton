// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/tls"
	"errors"
	"net/http"

	"github.com/batonos/baton/core/control-api/internal/ca"
	"github.com/batonos/baton/core/pkg/spi/console"
)


type Principal struct {

	Identity ca.Identity

	Role console.Role

	Subject string
}


func (p Principal) IsNode() bool { return p.Identity.IsNode() }


var ErrNoPeerCertificate = errors.New("auth: no client certificate presented")






func PrincipalFromRequest(r *http.Request) (Principal, error) {
	if r.TLS == nil {
		return Principal{}, ErrNoPeerCertificate
	}
	return principalFromState(r.TLS)
}

func principalFromState(state *tls.ConnectionState) (Principal, error) {
	if len(state.PeerCertificates) == 0 {
		return Principal{}, ErrNoPeerCertificate
	}
	leaf := state.PeerCertificates[0]
	id, err := ca.ParseIdentity(leaf)
	if err != nil {
		return Principal{}, err
	}
	return Principal{
		Identity: id,
		Role:     roleForIdentity(id),
		Subject:  leaf.Subject.CommonName,
	}, nil
}















func roleForIdentity(id ca.Identity) console.Role {
	switch id.Kind {
	case ca.KindUser:
		return console.RoleSuperAdmin
	case ca.KindNode:
		return console.RoleAgentService
	default:


		return ""
	}
}


func RequireRole(p Principal, need console.Role) bool {
	return p.Role.Satisfies(need)
}
