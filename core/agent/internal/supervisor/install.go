// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)













const installMarker = "install.done"










func (s *Supervisor) installOnce(ctx context.Context) error {
	if len(s.spec.Package.Install) == 0 {
		return nil
	}
	hostname, _ := os.Hostname()
	marker := filepath.Join(envOr("BATON_DATA_DIR", DefaultDataDir), installMarker)
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == hostname && hostname != "" {
		return nil
	}
	env, err := buildEnvironment(s.spec)
	if err != nil {
		return err
	}
	for i, argv := range s.spec.Package.Install {
		if len(argv) == 0 {
			continue
		}
		s.log.Info("package.install", "step", i, "argv", strings.Join(argv, " "))
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Env = env
		if s.spec.Package.WorkingDir != "" {
			cmd.Dir = s.spec.Package.WorkingDir
		}
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			tail := string(out)
			if len(tail) > 2000 {
				tail = "…" + tail[len(tail)-2000:]
			}
			return fmt.Errorf("package.install[%d] (%s) failed: %w\n%s", i, strings.Join(argv, " "), runErr, tail)
		}
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(hostname+"\n"), 0o600)
}
