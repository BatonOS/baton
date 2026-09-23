// SPDX-License-Identifier: Apache-2.0

package httpapi


























import (
	"errors"
	"strings"
)


type agentAddress struct {
	Agent string


	Network string
}

var errNotAnAddress = errors.New("not a <network>.<agent>@<domain> address")







func looksLikeAgentAddress(s string) bool {









	return strings.Contains(strings.TrimPrefix(s, "@"), "@")
}






func parseAgentAddress(s string) (agentAddress, error) {
	s = strings.TrimSpace(s)
	at := strings.Count(s, "@")
	if at == 0 {
		return agentAddress{}, errNotAnAddress
	}
	if at > 1 {
		return agentAddress{}, errors.New("an address has one @: <network>.<agent>@<domain>")
	}
	local, domain, _ := strings.Cut(s, "@")
	if domain == "" {
		return agentAddress{}, errors.New("an address needs a domain after the @")
	}
	if strings.ContainsAny(domain, " \t/:") {
		return agentAddress{}, errors.New("the domain contains a character a domain cannot have")
	}
	label, agent, found := strings.Cut(local, ".")
	if !found {
		return agentAddress{}, errors.New(
			"the part before the @ is <network>.<agent> — one dot, naming the network and then the agent")
	}
	if label == "" || agent == "" {
		return agentAddress{}, errors.New("both the network and the agent must be named before the @")
	}
	if strings.Contains(agent, ".") {



		return agentAddress{}, errors.New(
			"an agent name may not contain a dot: the first dot separates the network from the agent")
	}
	return agentAddress{Agent: agent, Network: label + "." + domain}, nil
}





















func formatAgentAddress(agent, network string) string {
	agent, network = strings.TrimSpace(agent), strings.TrimSpace(network)
	if agent == "" || network == "" {
		return ""
	}
	label, domain, found := strings.Cut(network, ".")
	if !found || label == "" || domain == "" {




		return ""
	}
	if strings.Contains(agent, ".") || strings.Contains(agent, "@") {
		return ""
	}
	return label + "." + agent + "@" + domain
}
