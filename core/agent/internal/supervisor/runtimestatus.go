// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"os"
	"path/filepath"
	"strings"
)













type RuntimeStatus string

const (












	StatusNotDeclared RuntimeStatus = "not-declared"







	StatusUnknown RuntimeStatus = "unknown"





	StatusIdle RuntimeStatus = "idle"

	StatusWorking RuntimeStatus = "working"





	StatusNeedsInput RuntimeStatus = "needs-input"

	StatusDone RuntimeStatus = "done"







	StatusErrored RuntimeStatus = "errored"
)



func (s RuntimeStatus) selfReported() bool {
	switch s {
	case StatusIdle, StatusWorking, StatusNeedsInput, StatusDone, StatusErrored:
		return true
	}
	return false
}





func (s RuntimeStatus) Known() bool {
	return s.selfReported() || s == StatusUnknown || s == StatusNotDeclared
}









const StatusDirName = "runtime-status"


const statusFileName = "status"



func StatusPaths(dataDir string) (dir, file string) {
	dir = filepath.Join(dataDir, StatusDirName)
	return dir, filepath.Join(dir, statusFileName)
}













func readStatusFile(path string) (st RuntimeStatus, raw string, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	raw = strings.TrimSpace(string(b))
	if raw == "" {
		return "", "", false
	}
	cand := RuntimeStatus(raw)
	if !cand.selfReported() {





		return "", raw, false
	}
	return cand, raw, true
}
