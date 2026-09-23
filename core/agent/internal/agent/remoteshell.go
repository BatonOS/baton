// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"os"
	"path/filepath"
	"strings"
)













const remoteShellEnv = "BATON_ALLOW_REMOTE_SHELL"

func remoteShellFile(dataDir string) string {
	return filepath.Join(dataDir, "remote-shell")
}



func RemoteShellAllowed(dataDir string) bool {
	b, err := os.ReadFile(remoteShellFile(dataDir))
	if err != nil {
		return envTruthy(os.Getenv(remoteShellEnv))
	}
	return strings.TrimSpace(string(b)) == "on"
}


func SetRemoteShell(dataDir string, on bool) error {
	v := "off\n"
	if on {
		v = "on\n"
	}
	return os.WriteFile(remoteShellFile(dataDir), []byte(v), 0o644)
}

func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
