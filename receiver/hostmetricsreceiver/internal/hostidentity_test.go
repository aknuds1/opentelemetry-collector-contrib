// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/metadata"
)

// allResourceAttributes enables every attribute, including the opt-in service.instance.id.
func allResourceAttributes() metadata.ResourceAttributesConfig {
	cfg := metadata.DefaultResourceAttributesConfig()
	cfg.ServiceInstanceID.Enabled = true
	return cfg
}

func TestHostIdentityServiceInstanceID(t *testing.T) {
	t.Run("seeded from host ID, not host name", func(t *testing.T) {
		withName := HostIdentity{ID: "machine-id", Name: "myhost.example.com"}
		withoutName := HostIdentity{ID: "machine-id"}
		assert.Equal(t, withoutName.ServiceInstanceID(), withName.ServiceInstanceID(),
			"host name must not affect the ID when a host ID is available")
	})

	t.Run("falls back to host name", func(t *testing.T) {
		assert.Equal(t,
			uuid.NewSHA1(otelNamespaceUUID, []byte("myhost.example.com")).String(),
			HostIdentity{Name: "myhost.example.com"}.ServiceInstanceID())
	})

	t.Run("distinct hosts get distinct IDs", func(t *testing.T) {
		assert.NotEqual(t,
			HostIdentity{ID: "machine-a"}.ServiceInstanceID(),
			HostIdentity{ID: "machine-b"}.ServiceInstanceID())
		assert.NotEqual(t,
			HostIdentity{Name: "host-a"}.ServiceInstanceID(),
			HostIdentity{Name: "host-b"}.ServiceInstanceID())
	})

	t.Run("empty when nothing is known", func(t *testing.T) {
		assert.Empty(t, HostIdentity{}.ServiceInstanceID(),
			"a placeholder seed would collapse every unidentified host onto one ID")
	})

	// Pinning the exact value is what guarantees the ID stays stable across restarts and across
	// collectors, which is the whole point of deriving it rather than generating it.
	t.Run("is a v5 UUID of the seed in the OTel namespace", func(t *testing.T) {
		got := HostIdentity{ID: "machine-id"}.ServiceInstanceID()
		parsed, err := uuid.Parse(got)
		require.NoError(t, err)
		assert.Equal(t, uuid.Version(5), parsed.Version())
		assert.Equal(t, uuid.NewSHA1(otelNamespaceUUID, []byte("machine-id")).String(), got)
	})
}

func TestHostIdentityResourceAttributes(t *testing.T) {
	identity := HostIdentity{ID: "machine-id", Name: "myhost.example.com"}

	t.Run("all attributes enabled", func(t *testing.T) {
		attrs := identity.ResourceAttributes(allResourceAttributes(), true)
		assert.Equal(t, map[string]any{
			"host.id":             "machine-id",
			"host.name":           "myhost.example.com",
			"service.instance.id": identity.ServiceInstanceID(),
		}, attrs.AsRaw())
	})

	t.Run("service.instance.id is opt-in", func(t *testing.T) {
		attrs := identity.ResourceAttributes(metadata.DefaultResourceAttributesConfig(), true)
		assert.Equal(t, map[string]any{
			"host.id":   "machine-id",
			"host.name": "myhost.example.com",
		}, attrs.AsRaw())
	})

	t.Run("withServiceInstanceID false suppresses it even when enabled", func(t *testing.T) {
		attrs := identity.ResourceAttributes(allResourceAttributes(), false)
		_, ok := attrs.Get("service.instance.id")
		assert.False(t, ok, "per-process resources must not share one host-derived instance ID")
		assert.Equal(t, 2, attrs.Len())
	})

	t.Run("undetected fields are omitted", func(t *testing.T) {
		attrs := HostIdentity{Name: "myhost.example.com"}.ResourceAttributes(allResourceAttributes(), true)
		_, ok := attrs.Get("host.id")
		assert.False(t, ok)
		assert.Equal(t, map[string]any{
			"host.name":           "myhost.example.com",
			"service.instance.id": HostIdentity{Name: "myhost.example.com"}.ServiceInstanceID(),
		}, attrs.AsRaw())
	})

	t.Run("no identity yields no attributes", func(t *testing.T) {
		attrs := HostIdentity{}.ResourceAttributes(allResourceAttributes(), true)
		assert.Zero(t, attrs.Len())
	})

	t.Run("individually disabled attributes are omitted", func(t *testing.T) {
		cfg := allResourceAttributes()
		cfg.HostID.Enabled = false
		attrs := identity.ResourceAttributes(cfg, true)
		_, ok := attrs.Get("host.id")
		assert.False(t, ok)
		assert.Equal(t, 2, attrs.Len())
	})
}

func TestHostIdentitySource(t *testing.T) {
	source := NewHostIdentitySource(zaptest.NewLogger(t))
	identity := source.Get(t.Context())

	// Host ID detection is platform-dependent, so only the host name is guaranteed here.
	assert.NotEmpty(t, identity.Name, "os.Hostname should succeed on a test machine")
	assert.NotEmpty(t, identity.ServiceInstanceID())

	assert.Equal(t, identity, source.Get(t.Context()), "the resolved identity must be reused")
}

// TestHostIdentitySourceDetectsOnce pins the property the shared source exists for: concurrent
// Starts across scrapers collapse to a single probe, rather than one subprocess per scraper on the
// platforms where detection shells out.
//
// It counts probes rather than comparing results, because real detection is deterministic on a
// given host — comparing results would pass just as happily with the sync.Once removed.
func TestHostIdentitySourceDetectsOnce(t *testing.T) {
	var probes atomic.Int64
	detected := HostIdentity{ID: "machine-id", Name: "myhost.example.com"}

	source := NewHostIdentitySource(zaptest.NewLogger(t))
	source.detect = func(context.Context, *zap.Logger) HostIdentity {
		probes.Add(1)
		return detected
	}

	const scrapers = 8
	results := make([]HostIdentity, scrapers)
	var wg sync.WaitGroup
	for i := range scrapers {
		wg.Go(func() { results[i] = source.Get(t.Context()) })
	}
	wg.Wait()

	assert.Equal(t, int64(1), probes.Load(), "%d scrapers must share one probe", scrapers)
	for i := range scrapers {
		assert.Equal(t, detected, results[i], "every scraper must see the resolved identity")
	}
}

func TestDetectHostIDHonoursDeadline(t *testing.T) {
	// The darwin and BSD readers shell out with context.Background(), so this only proves our own
	// bound is wired up: an already-cancelled context must not reach the platform probe.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := detectHostID(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
