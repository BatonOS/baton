// SPDX-License-Identifier: Apache-2.0












package grantproof

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
)




const purpose = "baton-grant-v1"




const canonicalLines = 12


type Fields struct {
	TenantID    string
	GrantID     string
	Grantor     string
	Grantee     string
	Action      string
	Object      string
	Scope       string
	Effect      string
	Constraints string
	ValidFrom   string
	ValidUntil  string
}




func Parse(canonical []byte) (*Fields, error) {
	lines := strings.Split(string(canonical), "\n")
	if len(lines) != canonicalLines {
		return nil, fmt.Errorf("grantproof: canonical has %d lines, this build reads %d", len(lines), canonicalLines)
	}
	if lines[0] != purpose {
		return nil, errors.New("grantproof: not a " + purpose + " document")
	}
	return &Fields{
		TenantID: lines[1], GrantID: lines[2], Grantor: lines[3], Grantee: lines[4],
		Action: lines[5], Object: lines[6], Scope: lines[7], Effect: lines[8],
		Constraints: lines[9], ValidFrom: lines[10], ValidUntil: lines[11],
	}, nil
}




func Verify(pub ed25519.PublicKey, canonical, proof []byte) bool {
	if len(proof) == 0 {
		return false
	}
	return ed25519.Verify(pub, canonical, proof)
}
