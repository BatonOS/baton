// SPDX-License-Identifier: Apache-2.0






package audit

import (
	"context"
	"time"
)




type Record struct {
	Event     string
	Actor     string
	ActorType string
	ActingFor string
	Action    string
	Target    string
	Result    string
	SourceIP  string
	NodeID    string
	RequestID string
	TraceID   string
	At        time.Time
	Detail    map[string]any
}


type Receipt struct {
	EventID string
	Seq     int64
	Hash    string
}







type Sink interface {
	Append(ctx context.Context, r Record) (Receipt, error)






	Verify(ctx context.Context, fromSeq int64) (brokenAt int64, err error)
}



var ErrVerificationUnsupported = errVerification{}

type errVerification struct{}

func (errVerification) Error() string { return "audit: chain verification unsupported by this sink" }
