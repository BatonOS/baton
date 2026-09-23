// SPDX-License-Identifier: Apache-2.0













package ha

import (
	"context"
	"errors"
	"time"
)




var ErrNotSupported = errors.New("ha: not supported in this edition")


type Leadership struct {



	Epoch     int64
	AcquiredAt time.Time


	Expires time.Time
}


type LeaderElector interface {

	Campaign(ctx context.Context) (Leadership, error)

	Epoch() int64

	Resign(ctx context.Context) error
}


type ReplState struct {




	Healthy *bool

	LagSeconds float64
	LastAppliedAt *time.Time
	Detail        string
}


type SnapshotMeta struct {
	SizeBytes int64
	SHA256    string
	TakenAt   time.Time
}


type ReplicationProvider interface {
	Status(ctx context.Context) (ReplState, error)

	Snapshot(ctx context.Context, w interface{ Write([]byte) (int, error) }) (SnapshotMeta, error)
}



type Plan struct {
	FromNodeID string
	ToNodeID   string


	DataLossWindow time.Duration
	Preconditions  []Precondition



	FencingRequired bool
	Steps           []string
}


type Precondition struct {
	Name      string
	Satisfied bool
	Detail    string

	Blocking bool
}


type PlanRequest struct {
	TargetNodeID string


	Force bool
}


type FailoverCoordinator interface {
	Plan(ctx context.Context, r PlanRequest) (Plan, error)



	Execute(ctx context.Context, p Plan) error
}




type FencingProvider interface {
	Name() string
	Fence(ctx context.Context, nodeID string) error
	Verify(ctx context.Context, nodeID string) (fenced bool, err error)
}
