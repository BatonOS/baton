// SPDX-License-Identifier: Apache-2.0










package capdeny

import (
	"context"
	"time"

	spi "github.com/batonos/baton/core/pkg/spi/store"
)






func earlier(a, b *spi.Grant) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.GrantID < b.GrantID
}




func FirstActiveDeny(ctx context.Context, grants spi.GrantStore, tenantID, capabilityID string, now time.Time) (*spi.Grant, error) {
	all, err := grants.List(ctx, tenantID, "", spi.ActionCapabilityInvoke)
	if err != nil {
		return nil, err
	}
	var first *spi.Grant
	for i := range all {
		g := &all[i]
		if g.Object != capabilityID || g.Effect != spi.GrantDeny || g.StatusAt(now) != spi.GrantActive {
			continue
		}
		if first == nil || earlier(g, first) {
			first = g
		}
	}
	return first, nil
}




func ActiveDenyMap(ctx context.Context, grants spi.GrantStore, tenantID string, now time.Time) (map[string]spi.Grant, error) {
	all, err := grants.List(ctx, tenantID, "", spi.ActionCapabilityInvoke)
	if err != nil {
		return nil, err
	}
	out := map[string]spi.Grant{}
	for _, g := range all {
		if g.Effect != spi.GrantDeny || g.StatusAt(now) != spi.GrantActive {
			continue
		}
		if have, ok := out[g.Object]; !ok || earlier(&g, &have) {
			out[g.Object] = g
		}
	}
	return out, nil
}




func ActiveDenies(ctx context.Context, grants spi.GrantStore, tenantID, capabilityID string, now time.Time) ([]spi.Grant, error) {
	all, err := grants.List(ctx, tenantID, "", spi.ActionCapabilityInvoke)
	if err != nil {
		return nil, err
	}
	out := []spi.Grant{}
	for _, g := range all {
		if g.Object == capabilityID && g.Effect == spi.GrantDeny && g.StatusAt(now) == spi.GrantActive {
			out = append(out, g)
		}
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
