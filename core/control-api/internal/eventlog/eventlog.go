// SPDX-License-Identifier: Apache-2.0









package eventlog

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/batonos/baton/core/pkg/spi/audit"
	spi "github.com/batonos/baton/core/pkg/spi/store"
)


type Log struct {
	store spi.EventStore



	epoch atomic.Int64
}


func New(store spi.EventStore, epoch int64) *Log {
	l := &Log{store: store}
	if epoch == 0 {
		epoch = 1
	}
	l.epoch.Store(epoch)
	return l
}





func (l *Log) In(store spi.EventStore) *Log {
	n := &Log{store: store}
	n.epoch.Store(l.epoch.Load())
	return n
}


func (l *Log) SetEpoch(e int64) { l.epoch.Store(e) }







func (l *Log) Append(ctx context.Context, r audit.Record) (audit.Receipt, error) {
	detail := []byte("{}")
	if len(r.Detail) > 0 {
		encoded, err := json.Marshal(r.Detail)
		if err != nil {
			return audit.Receipt{}, fmt.Errorf("eventlog: marshal detail: %w", err)
		}
		detail = encoded
	}

	e := &spi.Event{
		EventID:     "evt_" + uuid.NewString(),
		TS:          r.At,
		Category:    spi.CategoryAudit,
		Event:       r.Event,
		Actor:       r.Actor,
		ActorType:   r.ActorType,
		ActingFor:   r.ActingFor,
		Action:      r.Action,
		Target:      r.Target,
		Result:      r.Result,
		SourceIP:    r.SourceIP,
		NodeID:      r.NodeID,
		RequestID:   r.RequestID,
		TraceID:     r.TraceID,
		LeaderEpoch: l.epoch.Load(),
		Detail:      detail,
	}

	stored, err := l.store.Append(ctx, e)
	if err != nil {
		return audit.Receipt{}, err
	}
	return audit.Receipt{EventID: stored.EventID, Seq: stored.Seq, Hash: stored.Hash}, nil
}




func (l *Log) System(ctx context.Context, event string, fields map[string]any) {
	detail := []byte("{}")
	if len(fields) > 0 {
		if encoded, err := json.Marshal(fields); err == nil {
			detail = encoded
		}
	}
	nodeID, _ := fields["node_id"].(string)




	_, _ = l.store.Append(ctx, &spi.Event{
		EventID:     "evt_" + uuid.NewString(),
		Category:    spi.CategorySystem,
		Event:       event,
		NodeID:      nodeID,
		LeaderEpoch: l.epoch.Load(),
		Detail:      detail,
	})
}



func (l *Log) Verify(ctx context.Context, fromSeq int64) (int64, error) {
	return l.store.VerifyChain(ctx, fromSeq)
}

var _ audit.Sink = (*Log)(nil)
