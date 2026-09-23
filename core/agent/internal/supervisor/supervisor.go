// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"os"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"syscall"
	"time"

	"github.com/batonos/baton/core/agent/internal/runtimespec"
)







type RuntimeState string

const (

	StatePending RuntimeState = "pending"
	StateStarting RuntimeState = "starting"
	StateRunning  RuntimeState = "running"

	StateRestarting RuntimeState = "restarting"

	StateExited RuntimeState = "exited"

	StateFailed RuntimeState = "failed"

	StateStopped RuntimeState = "stopped"





	StatePaused RuntimeState = "paused"
)



const (
	restartBackoffMin = 1 * time.Second
	restartBackoffMax = 60 * time.Second
	restartJitter     = 0.2
)


type Status struct {
	Name         string       `json:"name"`
	State         RuntimeState  `json:"state"`
	RuntimeStatus RuntimeStatus `json:"runtime_status"`
	Describe     string       `json:"describe"`
	Session      string       `json:"session"`
	RestartCount int          `json:"restart_count"`
	StartedAt    time.Time    `json:"started_at,omitempty"`
	LastExitCode *int         `json:"last_exit_code,omitempty"`
	LastExitAt   time.Time    `json:"last_exit_at,omitempty"`
	OOMKilled    bool         `json:"oom_killed,omitempty"`

	LastError string `json:"last_error,omitempty"`


	Probe ProbeResult `json:"probe,omitempty"`



	Memory string `json:"memory,omitempty"`
}




type Supervisor struct {
	spec   *runtimespec.Spec
	runner Runner
	log    *slog.Logger






	fetchFacility func(ctx context.Context, ref string) (archive []byte, digest, id string, err error)

	servicesMu sync.Mutex
	services   map[string]*service

	mu        sync.RWMutex
	state     RuntimeState
	runtimeStatus RuntimeStatus

















	statusGen uint64
	restarts  int
	startedAt time.Time
	lastExit  *Exit
	lastErr   string




	probe      ProbeResult
	probeFails int




	restartRequested bool












	reconcileMu sync.Mutex




	mail *mailWaker

	hasPendingMail func() bool

	stop     chan struct{}
	stopOnce sync.Once
	finished chan struct{}
}


func New(spec *runtimespec.Spec, opts Options) (*Supervisor, error) {
	if opts.Logger == nil {
		return nil, errors.New("supervisor: a logger is required")
	}
	sup := &Supervisor{
		spec:     spec,
		runner:   newRunner(spec, opts),
		log:      opts.Logger.With("runtime", spec.Metadata.Name),
		state:    StatePending,


		runtimeStatus: silentStatusFor(spec),
		stop:           make(chan struct{}),
		finished:       make(chan struct{}),
		hasPendingMail: opts.HasPendingMail,
		fetchFacility:  opts.FetchFacility,
	}
	if len(spec.Adapter.Inbox.OnMessage) > 0 {
		sup.mail = newMailWaker()
	}
	return sup, nil
}


func (s *Supervisor) Spec() *runtimespec.Spec { return s.spec }














func (s *Supervisor) EnsureWorkspace() error {
	_, err := ensureWorkspace(s.log, s.spec.Adapter.Workspace.MountPath)
	return err
}


func (s *Supervisor) Status() Status {



	s.refreshRuntimeStatus()

	s.mu.RLock()
	defer s.mu.RUnlock()

	st := Status{
		Name:          s.spec.Metadata.Name,
		State:         s.state,
		RuntimeStatus: s.runtimeStatus,
		Describe:     s.runner.Describe(),
		Session:      SessionWord(s.spec.Adapter.Terminal.TTY),
		RestartCount: s.restarts,
		StartedAt:    s.startedAt,
		LastError:    s.lastErr,
		Probe:        s.probe,
		Memory:       appliedMemory(),
	}
	if s.lastExit != nil {
		code := s.lastExit.Code
		st.LastExitCode = &code
		st.LastExitAt = s.lastExit.At
		st.OOMKilled = s.lastExit.OOMKilled
	}
	return st
}






func silentStatusFor(spec *runtimespec.Spec) RuntimeStatus {
	if spec != nil && spec.Adapter.Status.Declared() {
		return StatusUnknown
	}
	return StatusNotDeclared
}






func (s *Supervisor) refreshRuntimeStatus() {
	if !s.spec.Adapter.Status.Declared() {
		return
	}
	s.mu.RLock()
	gen := s.statusGen
	s.mu.RUnlock()

	_, file := StatusPaths(envOr("BATON_DATA_DIR", DefaultDataDir))
	st, raw, ok := readStatusFile(file)
	if !ok {
		if raw != "" {



			s.log.Warn("runtime reported a status outside the vocabulary; keeping the previous value",
				"reported", raw, "path", file)
		}
		return
	}
	s.mu.Lock()



	if s.statusGen == gen {
		s.runtimeStatus = st
	}
	s.mu.Unlock()
}







func (s *Supervisor) clearRuntimeStatus() {
	if s.spec.Adapter.Status.Declared() {
		_, file := StatusPaths(envOr("BATON_DATA_DIR", DefaultDataDir))
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			s.log.Warn("could not drop the runtime's last status", "path", file, "error", err)
		}
	}
	s.mu.Lock()
	s.runtimeStatus = silentStatusFor(s.spec)


	s.statusGen++
	s.mu.Unlock()
}







func (s *Supervisor) Pause() error {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	if state != StateRunning {
		return fmt.Errorf("supervisor: the runtime is %s, not running", state)
	}
	if err := s.runner.Signal(syscall.SIGSTOP); err != nil {
		return err
	}
	s.setState(StatePaused)


	s.clearRuntimeStatus()
	return nil
}


func (s *Supervisor) Resume() error {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	if state != StatePaused {
		return fmt.Errorf("supervisor: the runtime is %s, not paused", state)
	}
	if err := s.runner.Signal(syscall.SIGCONT); err != nil {
		return err
	}
	s.setState(StateRunning)
	return nil
}







func (s *Supervisor) Restart(ctx context.Context) error {
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	if state == StatePaused {



		if err := s.runner.Signal(syscall.SIGCONT); err != nil {
			return err
		}
		s.setState(StateRunning)
	}

	s.mu.Lock()
	s.restartRequested = true
	s.mu.Unlock()

	if err := s.runner.Stop(ctx, s.spec.Adapter.Lifecycle.StopGrace()); err != nil {
		s.mu.Lock()
		s.restartRequested = false
		s.mu.Unlock()
		return err
	}
	return nil
}







func (s *Supervisor) Run(ctx context.Context) error {
	defer close(s.finished)



	defer s.stopServices()




	if s.wakesOnMail() && s.hasPendingMail != nil {
		go s.runMailWaker(ctx)
	}











	s.clearRuntimeStatus()




	if err := s.installOnce(ctx); err != nil {
		s.setError(err.Error())
		s.setState(StateFailed)
		s.log.Error("package.install failed", "error", err)
		return err
	}




	s.reconcilePlugins(ctx)



	go s.runProbes(ctx)



	go s.serveSkillSync(ctx)




	go s.serveManage(ctx)

	backoff := restartBackoffMin

	for {
		s.setState(StateStarting)
		err := s.runner.Start(ctx)
		if err != nil {
			s.setError(err.Error())
			s.setState(StateFailed)
			s.log.Error("runtime failed to start", "error", err)

			if s.spec.Adapter.Lifecycle.Restart == runtimespec.RestartNever {
				return nil
			}
			if !s.waitBeforeRestart(ctx, &backoff) {
				return nil
			}
			continue
		}

		s.markRunning()

		exit, waitErr := s.runner.Wait(ctx)
		if waitErr != nil && ctx.Err() != nil {

			s.shutdown()
			return nil
		}

		select {
		case <-s.stop:
			s.recordExit(exit)
			s.setState(StateStopped)
			return nil
		default:
		}

		s.recordExit(exit)
		s.log.Warn("runtime exited",
			"code", exit.Code, "signal", exit.Signal, "oom_killed", exit.OOMKilled)

		if !s.shouldRestart(exit) {
			if exit.Code == 0 {
				s.setState(StateExited)
			} else {
				s.setState(StateFailed)
			}
			return nil
		}

		s.setState(StateRestarting)
		if !s.waitBeforeRestart(ctx, &backoff) {
			return nil
		}
		s.mu.Lock()
		s.restarts++
		s.mu.Unlock()
	}
}


func (s *Supervisor) shouldRestart(exit Exit) bool {


	s.mu.Lock()
	requested := s.restartRequested
	s.restartRequested = false
	s.mu.Unlock()
	if requested {
		return true
	}

	switch s.spec.Adapter.Lifecycle.Restart {
	case runtimespec.RestartAlways:
		return true
	case runtimespec.RestartNever:
		return false
	default:
		return exit.Code != 0
	}
}


func (s *Supervisor) waitBeforeRestart(ctx context.Context, backoff *time.Duration) bool {
	wait := jitter(*backoff)
	s.log.Info("restarting the runtime", "in", wait.Round(time.Millisecond).String())

	select {
	case <-ctx.Done():
		s.shutdown()
		return false
	case <-s.stop:
		s.setState(StateStopped)
		return false
	case <-time.After(wait):
	}

	*backoff *= 2
	if *backoff > restartBackoffMax {
		*backoff = restartBackoffMax
	}
	return true
}


func (s *Supervisor) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stop) })

	if err := s.runner.Stop(ctx, s.spec.Adapter.Lifecycle.StopGrace()); err != nil {
		return err
	}
	select {
	case <-s.finished:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.spec.Adapter.Lifecycle.StopGrace() + 10*time.Second):
		return fmt.Errorf("supervisor: %s did not finish", s.spec.Metadata.Name)
	}
	return nil
}



func (s *Supervisor) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(),
		s.spec.Adapter.Lifecycle.StopGrace()+5*time.Second)
	defer cancel()
	if err := s.runner.Stop(ctx, s.spec.Adapter.Lifecycle.StopGrace()); err != nil {
		s.log.Warn("stopping the runtime", "error", err)
	}
	s.setState(StateStopped)
}

func (s *Supervisor) setState(state RuntimeState) {
	s.mu.Lock()
	previous := s.state
	s.state = state
	s.mu.Unlock()
	if previous != state {
		s.log.Info("runtime state changed", "from", string(previous), "to", string(state))
	}
}

func (s *Supervisor) markRunning() {
	s.mu.Lock()
	s.state = StateRunning
	s.startedAt = time.Now().UTC()
	s.lastErr = ""
	s.mu.Unlock()
}

func (s *Supervisor) recordExit(exit Exit) {
	s.mu.Lock()
	s.lastExit = &exit
	s.mu.Unlock()


	s.clearRuntimeStatus()
}

func (s *Supervisor) setError(msg string) {
	s.mu.Lock()
	s.lastErr = msg
	s.mu.Unlock()
}

func jitter(d time.Duration) time.Duration {
	delta := float64(d) * restartJitter
	return time.Duration(float64(d) - delta + rand.Float64()*2*delta)
}









func SessionWord(tty bool) string {
	if tty {
		return "tty"
	}
	return "headless"
}
