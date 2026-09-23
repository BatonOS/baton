// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"os"
	"strconv"
	"strings"
	"sync"
)





var appliedMemory = sync.OnceValue(AppliedMemory)



















func AppliedMemory() string {


	if raw, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		return quantity(strings.TrimSpace(string(raw)))
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		return quantity(strings.TrimSpace(string(raw)))
	}
	return ""
}






func quantity(raw string) string {
	if raw == "" || raw == "max" {



		return "unlimited"
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return ""
	}


	if n >= 1<<62 {
		return "unlimited"
	}
	const (
		Ki = 1 << 10
		Mi = 1 << 20
		Gi = 1 << 30
	)
	switch {
	case n%Gi == 0:
		return strconv.FormatInt(n/Gi, 10) + "Gi"
	case n%Mi == 0:
		return strconv.FormatInt(n/Mi, 10) + "Mi"
	case n%Ki == 0:
		return strconv.FormatInt(n/Ki, 10) + "Ki"
	default:



		return strconv.FormatInt(n, 10)
	}
}
