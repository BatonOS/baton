// SPDX-License-Identifier: Apache-2.0




























package supervisor

import (
	"context"
	"errors"
	"syscall"
	"time"

	"github.com/batonos/baton/core/agent/internal/runtimespec"
)


var ErrNotRunning = errors.New("supervisor: the runtime is not running")










type Runner interface {


	Start(ctx context.Context) error


	Wait(ctx context.Context) (Exit, error)


	Stop(ctx context.Context, grace time.Duration) error






	Signal(sig syscall.Signal) error


	Describe() string
}







func newRunner(spec *runtimespec.Spec, opts Options) Runner {
	if spec.Adapter.Terminal.TTY {
		return newTmuxRunner(spec, opts)
	}
	return newProcessRunner(spec, opts)
}






type Exit struct {
	Code      int
	Signal    string
	OOMKilled bool
	At        time.Time
}
