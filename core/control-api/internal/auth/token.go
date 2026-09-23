// SPDX-License-Identifier: Apache-2.0



package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)




const TokenPrefix = "bt_"



var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)






func NewToken() (plaintext, hash string, err error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("auth: generate token: %w", err)
	}
	plaintext = TokenPrefix + strings.ToLower(tokenEncoding.EncodeToString(raw))
	return plaintext, HashToken(plaintext), nil
}


func HashToken(plaintext string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(plaintext)))
	return hex.EncodeToString(sum[:])
}


func EqualTokens(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}












var nodeNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)






func ValidateNodeName(name string) error {
	if name == "" {
		return fmt.Errorf("node name is required")
	}
	if !nodeNamePattern.MatchString(name) {
		return fmt.Errorf(
			"node name %q is not usable: use lowercase letters, digits, and interior "+
				"hyphens, 1-40 characters (no dots or colons, which tmux session names "+
				"forbid, and no leading or trailing hyphen)", name)
	}
	return nil
}













func ValidateOperatorName(name string) error {
	if name == "" {
		return fmt.Errorf("operator name is required")
	}
	if !nodeNamePattern.MatchString(name) {
		return fmt.Errorf(
			"operator name %q is not usable: use lowercase letters, digits, and interior "+
				"hyphens, 1-40 characters. The name becomes a path segment in the "+
				"certificate's SAN URI and the actor in every audit record you write", name)
	}
	return nil
}



func MatchesNamePattern(pattern, name string) (bool, error) {
	if pattern == "" {
		return true, nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {


		return false, fmt.Errorf("auth: token name pattern %q is invalid: %w", pattern, err)
	}
	return re.MatchString(name), nil
}
