// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"fmt"
	"strings"
)




































func parseDeliveryScope(s string) ([]string, error) {
	if s == "" {
		return nil, fmt.Errorf("delivery scope is empty")
	}
	if strings.ContainsAny(s, " \t\n") {
		return nil, fmt.Errorf("delivery scope contains whitespace; it is comma-separated with no spaces")
	}
	parts := strings.Split(s, ",")
	for i, p := range parts {
		if p == "" {
			return nil, fmt.Errorf("delivery scope entry %d is empty", i)
		}




		if strings.ContainsAny(p, "@/:") {
			return nil, fmt.Errorf("delivery scope entry %q may not contain @, / or :; it names a recipient in this network", p)
		}
		if i > 0 && parts[i-1] >= p {
			return nil, fmt.Errorf("delivery scope entries must be ascending and unique; got %q then %q", parts[i-1], p)
		}
	}
	return parts, nil
}





func deliveryScopeCovers(grant, requested []string) bool {
	for _, want := range requested {
		if !deliveryScopeAllows(grant, want) {
			return false
		}
	}
	return true
}







func deliveryScopeAllows(scope []string, recipient string) bool {










	for _, e := range scope {
		if e == "*" || e == recipient {
			return true
		}
	}
	return false
}
