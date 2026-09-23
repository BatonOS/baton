// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"fmt"
	"strings"
)













var accessVerbs = map[string]bool{"read": true, "list": true}

var accessTypes = map[string]bool{
	"skill": true, "knowledge": true, "policy": true, "workflow": true, "template": true, "*": true,
}

type scopeEntry struct{ verb, rtype, pattern string }

func (e scopeEntry) String() string { return e.verb + ":" + e.rtype + "/" + e.pattern }



func parseScope(s string) ([]scopeEntry, error) {
	if s == "" {
		return nil, fmt.Errorf("scope is empty")
	}
	if strings.ContainsAny(s, " \t\n") {
		return nil, fmt.Errorf("scope contains whitespace; it is comma-separated with no spaces")
	}
	parts := strings.Split(s, ",")
	out := make([]scopeEntry, 0, len(parts))
	for _, p := range parts {
		colon := strings.IndexByte(p, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("scope entry %q is not verb:type/pattern", p)
		}
		slash := strings.IndexByte(p[colon+1:], '/')
		if slash < 0 {
			return nil, fmt.Errorf("scope entry %q is not verb:type/pattern", p)
		}
		e := scopeEntry{verb: p[:colon], rtype: p[colon+1 : colon+1+slash], pattern: p[colon+1+slash+1:]}
		if !accessVerbs[e.verb] {
			return nil, fmt.Errorf("scope verb %q is not read|list", e.verb)
		}
		if !accessTypes[e.rtype] {
			return nil, fmt.Errorf("scope type %q is not a resource type or *", e.rtype)
		}
		if e.pattern == "" {
			return nil, fmt.Errorf("scope entry %q has an empty pattern", p)
		}
		out = append(out, e)
	}
	for i := 1; i < len(out); i++ {
		if out[i-1].String() >= out[i].String() {
			return nil, fmt.Errorf("scope entries must be ascending and unique; got %q then %q", out[i-1], out[i])
		}
	}
	return out, nil
}




func covers(sup, sub string) bool { return sup == "*" || sup == sub }

func entryCovers(b, a scopeEntry) bool {
	return a.verb == b.verb && covers(b.rtype, a.rtype) && covers(b.pattern, a.pattern)
}



func scopeSubset(sub, sup []scopeEntry) bool {
	for _, a := range sub {
		ok := false
		for _, b := range sup {
			if entryCovers(b, a) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}




func scopeAllows(scope []scopeEntry, verb, rtype, name string) bool {
	req := scopeEntry{verb: verb, rtype: rtype, pattern: name}
	for _, e := range scope {
		if entryCovers(e, req) {
			return true
		}
	}
	return false
}
