// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/scraper"
	"go.uber.org/zap/zaptest"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/metadata"
)

// mockScraper returns predefined metrics along with an optional error, mimicking the partial
// scrape failures the host metrics scrapers routinely report.
type mockScraper struct {
	metrics pmetric.Metrics
	err     error
	started bool
	stopped bool
}

func (m *mockScraper) Start(context.Context, component.Host) error {
	m.started = true
	return nil
}

func (m *mockScraper) ScrapeMetrics(context.Context) (pmetric.Metrics, error) {
	return m.metrics, m.err
}

func (m *mockScraper) Shutdown(context.Context) error {
	m.stopped = true
	return nil
}

func metricsWithResources(t *testing.T, resourceAttrs ...map[string]any) pmetric.Metrics {
	t.Helper()
	metrics := pmetric.NewMetrics()
	for _, attrs := range resourceAttrs {
		rm := metrics.ResourceMetrics().AppendEmpty()
		require.NoError(t, rm.Resource().Attributes().FromRaw(attrs))
		rm.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetName("system.cpu.time")
	}
	return metrics
}

// fixedHostIdentitySource yields identity without probing the platform, by marking the source's
// sync.Once as already run.
func fixedHostIdentitySource(identity HostIdentity) *HostIdentitySource {
	s := &HostIdentitySource{}
	s.once.Do(func() { s.identity = identity })
	return s
}

func newTestScraper(delegate scraper.Metrics, identity HostIdentity, cfg metadata.ResourceAttributesConfig, withServiceInstanceID bool) *resourceAttributeScraper {
	return &resourceAttributeScraper{
		delegate:              delegate,
		source:                fixedHostIdentitySource(identity),
		cfg:                   cfg,
		withServiceInstanceID: withServiceInstanceID,
		attrs:                 pcommon.NewMap(),
	}
}

func TestResourceAttributeScraperInjectsIntoEveryResource(t *testing.T) {
	mock := &mockScraper{metrics: metricsWithResources(t,
		map[string]any{"process.pid": int64(1234)},
		map[string]any{"process.pid": int64(5678)},
	)}

	s := newTestScraper(mock,
		HostIdentity{ID: "machine-id", Name: "myhost.example.com"},
		metadata.DefaultResourceAttributesConfig(), false)

	require.NoError(t, s.Start(t.Context(), nil))
	assert.True(t, mock.started)

	result, err := s.ScrapeMetrics(t.Context())
	require.NoError(t, err)

	require.Equal(t, 2, result.ResourceMetrics().Len())
	assert.Equal(t, map[string]any{
		"process.pid": int64(1234),
		"host.id":     "machine-id",
		"host.name":   "myhost.example.com",
	}, result.ResourceMetrics().At(0).Resource().Attributes().AsRaw(),
		"pre-existing resource attributes must be preserved")
	assert.Equal(t, map[string]any{
		"process.pid": int64(5678),
		"host.id":     "machine-id",
		"host.name":   "myhost.example.com",
	}, result.ResourceMetrics().At(1).Resource().Attributes().AsRaw())

	require.NoError(t, s.Shutdown(t.Context()))
	assert.True(t, mock.stopped)
}

func TestResourceAttributeScraperInjectsOnPartialError(t *testing.T) {
	mock := &mockScraper{metrics: metricsWithResources(t, map[string]any{}), err: assert.AnError}

	cfg := metadata.DefaultResourceAttributesConfig()
	cfg.HostName.Enabled = false
	s := newTestScraper(mock, HostIdentity{ID: "machine-id"}, cfg, false)
	require.NoError(t, s.Start(t.Context(), nil))

	result, err := s.ScrapeMetrics(t.Context())
	assert.ErrorIs(t, err, assert.AnError, "the delegate's error must be propagated unchanged")

	require.Equal(t, 1, result.ResourceMetrics().Len())
	assert.Equal(t, map[string]any{"host.id": "machine-id"},
		result.ResourceMetrics().At(0).Resource().Attributes().AsRaw(),
		"metrics accompanying a partial error are still worth annotating")
}

func newMockFactory(delegate scraper.Metrics) scraper.Factory {
	return scraper.NewFactory(component.MustNewType("cpu"), func() component.Config { return nil },
		scraper.WithMetrics(func(context.Context, scraper.Settings, component.Config) (scraper.Metrics, error) {
			return delegate, nil
		}, component.StabilityLevelBeta))
}

func TestNewResourceAttributeFactoryWithoutEnabledAttributes(t *testing.T) {
	delegate := newMockFactory(&mockScraper{metrics: pmetric.NewMetrics()})
	source := NewHostIdentitySource(zaptest.NewLogger(t))

	t.Run("nothing enabled", func(t *testing.T) {
		var none metadata.ResourceAttributesConfig
		assert.Equal(t, delegate, NewResourceAttributeFactory(delegate, source, none, true),
			"a scraper that would gain nothing should not pay for a wrapper")
	})

	t.Run("only service.instance.id enabled, suppressed for this scraper", func(t *testing.T) {
		cfg := metadata.ResourceAttributesConfig{
			ServiceInstanceID: metadata.ResourceAttributeConfig{Enabled: true},
		}
		assert.Equal(t, delegate, NewResourceAttributeFactory(delegate, source, cfg, false))
		assert.NotEqual(t, delegate, NewResourceAttributeFactory(delegate, source, cfg, true),
			"the same config does warrant a wrapper for a host-level scraper")
	})
}

func TestNewResourceAttributeFactoryResolvesIdentityInStart(t *testing.T) {
	delegate := newMockFactory(&mockScraper{metrics: metricsWithResources(t, map[string]any{})})

	factory := NewResourceAttributeFactory(delegate, NewHostIdentitySource(zaptest.NewLogger(t)),
		metadata.DefaultResourceAttributesConfig(), true)
	require.NotEqual(t, delegate, factory)
	assert.Equal(t, delegate.Type(), factory.Type())
	assert.Equal(t, delegate.MetricsStability(), factory.MetricsStability())

	s, err := factory.CreateMetrics(t.Context(), scraper.Settings{
		ID:                component.NewID(factory.Type()),
		TelemetrySettings: componenttest.NewNopTelemetrySettings(),
		BuildInfo:         component.NewDefaultBuildInfo(),
	}, nil)
	require.NoError(t, err)

	// Construction must not have detected anything. Scraping before Start therefore adds nothing,
	// and crucially does not panic on an uninitialised map.
	beforeStart, err := s.ScrapeMetrics(t.Context())
	require.NoError(t, err)
	assert.Empty(t, beforeStart.ResourceMetrics().At(0).Resource().Attributes().AsRaw())

	require.NoError(t, s.Start(t.Context(), componenttest.NewNopHost()))

	afterStart, err := s.ScrapeMetrics(t.Context())
	require.NoError(t, err)
	hostName, ok := afterStart.ResourceMetrics().At(0).Resource().Attributes().Get("host.name")
	require.True(t, ok, "Start should have resolved the host identity")
	assert.NotEmpty(t, hostName.Str())
}
