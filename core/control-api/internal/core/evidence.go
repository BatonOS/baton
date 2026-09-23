// SPDX-License-Identifier: Apache-2.0

package core

import (
	"context"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)






















type EvidenceWriter func(ctx context.Context, t *spi.Transaction)

type evidenceKey struct{}





func WithEvidenceWriter(ctx context.Context, w EvidenceWriter) context.Context {
	return context.WithValue(ctx, evidenceKey{}, w)
}







func writeEvidenceFor(ctx context.Context, st spi.TransactionStore, id string) {
	w, _ := ctx.Value(evidenceKey{}).(EvidenceWriter)
	if w == nil {
		return
	}
	t, err := st.Get(ctx, id)
	if err != nil || t == nil {
		return
	}
	w(ctx, t)
}
