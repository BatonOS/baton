// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"os/exec"
	"sync"
	"time"
)





















const (


	mailWakeFirstBackoff = 5 * time.Second




	mailWakeMaxBackoff = 5 * time.Minute
)


type mailWaker struct {





	pending chan struct{}

	mu      sync.Mutex
	running bool
}

func newMailWaker() *mailWaker { return &mailWaker{pending: make(chan struct{}, 1)} }







func (s *Supervisor) NotifyMail() {
	if s.mail == nil {
		return
	}
	select {
	case s.mail.pending <- struct{}{}:
	default:
	}
}





func (s *Supervisor) wakesOnMail() bool {
	return s.spec != nil && len(s.spec.Adapter.Inbox.OnMessage) > 0
}














func (s *Supervisor) runMailWaker(ctx context.Context) {
	if !s.wakesOnMail() || s.hasPendingMail == nil {
		return
	}
	backoff := mailWakeFirstBackoff
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {




		if s.hasPendingMail() {
			s.runOnMessage(ctx)
			if s.hasPendingMail() {




				timer.Reset(backoff)
				select {
				case <-ctx.Done():
					return
				case <-s.stop:
					return
				case <-s.mail.pending:



					backoff = mailWakeFirstBackoff
					if !timer.Stop() {
						<-timer.C
					}
				case <-timer.C:
					backoff *= 2
					if backoff > mailWakeMaxBackoff {
						backoff = mailWakeMaxBackoff
					}
				}
				continue
			}

			backoff = mailWakeFirstBackoff
		}

		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-s.mail.pending:
		}
	}
}











func (s *Supervisor) runOnMessage(ctx context.Context) {
	argv := s.spec.Adapter.Inbox.OnMessage
	s.mail.mu.Lock()
	s.mail.running = true
	s.mail.mu.Unlock()
	defer func() {
		s.mail.mu.Lock()
		s.mail.running = false
		s.mail.mu.Unlock()
	}()

	env, err := buildEnvironment(s.spec)
	if err != nil {
		s.log.Error("inbox.onMessage: environment", "error", err)
		return
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	if dir := s.spec.Package.WorkingDir; dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()



	s.log.Debug("inbox.onMessage output", "bytes", len(out))
	if err != nil {
		s.log.Warn("inbox.onMessage did not succeed",
			"command", argv[0], "error", err)
		return
	}
	s.log.Info("inbox.onMessage ran", "command", argv[0])
}
