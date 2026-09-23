// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"net"
	"os/exec"
	"time"

	"github.com/batonos/baton/core/agent/internal/runtimespec"
)










type ProbeResult string

const (


	ProbeUnreported ProbeResult = ""



	ProbeNotDeclared ProbeResult = "not-declared"

	ProbePending ProbeResult = "pending"
	ProbePassing ProbeResult = "passing"
	ProbeFailing ProbeResult = "failing"
)





const probeTimeout = 3 * time.Second







func (s *Supervisor) runProbes(ctx context.Context) {
	kind := s.spec.Adapter.Health.Probe
	if kind != runtimespec.ProbeTCP && kind != runtimespec.ProbeExec {
		s.setProbe(ProbeNotDeclared)
		return
	}

	s.setProbe(ProbePending)

	interval := s.spec.Adapter.Health.Interval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	threshold := s.spec.Adapter.Health.FailureThreshold
	if threshold < 1 {
		threshold = 1
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-ticker.C:
		}




		s.mu.RLock()
		running := s.state == StateRunning
		s.mu.RUnlock()
		if !running {
			continue
		}

		ok, err := s.probeOnce(ctx, kind)
		s.mu.Lock()
		if ok {
			s.probeFails = 0
			s.probe = ProbePassing
			s.mu.Unlock()
			continue
		}
		s.probeFails++
		reached := s.probeFails >= threshold
		if reached {
			s.probe = ProbeFailing
		}
		s.mu.Unlock()

		if reached {



			msg := "health probe failed"
			if err != nil {
				msg = "health probe failed: " + err.Error()
			}
			s.setError(msg)
			s.log.Warn("health probe failed",
				"probe", string(kind), "consecutive", s.probeFails, "error", err)
		}
	}
}


func (s *Supervisor) probeOnce(ctx context.Context, kind runtimespec.ProbeKind) (bool, error) {
	return probeOnce(ctx, kind, s.spec.Adapter.Health)
}



func probeOnce(ctx context.Context, kind runtimespec.ProbeKind, h runtimespec.Health) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	switch kind {
	case runtimespec.ProbeTCP:
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", h.TCP)
		if err != nil {
			return false, err
		}
		_ = conn.Close()
		return true, nil

	case runtimespec.ProbeExec:
		argv := h.Exec
		if len(argv) == 0 {
			return false, errExecProbeEmpty
		}



		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		if err := cmd.Run(); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}


func (s *Supervisor) setProbe(r ProbeResult) {
	s.mu.Lock()
	s.probe = r
	s.mu.Unlock()
}

type probeErr string

func (e probeErr) Error() string { return string(e) }

const errExecProbeEmpty probeErr = "health.exec is empty, so there is nothing to run"
