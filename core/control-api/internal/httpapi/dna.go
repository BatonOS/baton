// SPDX-License-Identifier: Apache-2.0

package httpapi

import "strings"



















const (
	purposeDNARegister = "dna-register"
	purposeRunSign     = "run-sign"
	purposeRunTool     = "run-tool"
	purposeOutputSign  = "output-sign"
)




























func runSignCanonical(
	signer, runID, agent, sessionID, model, config, template, registeredAt, nonce string,
) string {
	return strings.Join([]string{
		purposeRunSign, signer, runID, agent,
		sessionID, model, config, template, registeredAt, nonce,
	}, "\n")
}





















func runToolCanonical(signer, runID, seq, skillID, skillDigest, tier, registeredAt, nonce string) string {
	return strings.Join([]string{
		purposeRunTool, signer, runID, seq, skillID, skillDigest, tier, registeredAt, nonce,
	}, "\n")
}








































func outputSignCanonical(signer, recordID, runID, outputDigest, callerExeDigest, ts, nonce string) string {
	return strings.Join([]string{
		purposeOutputSign, signer, recordID, runID, outputDigest, callerExeDigest, ts, nonce,
	}, "\n")
}








func dnaRegisterCanonical(networkID, resourceID, typ, name, version, contentHash, parentRef, ts, nonce string) string {
	return strings.Join([]string{purposeDNARegister, networkID, resourceID, typ, name, version, contentHash, parentRef, ts, nonce}, "\n")
}
