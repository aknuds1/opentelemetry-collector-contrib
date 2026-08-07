// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal"

import (
	"context"

	"github.com/shirou/gopsutil/v4/common"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/scraper"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/metadata"
)

// Config is the configuration of a scraper.
type Config interface {
	SetRootPath(rootPath string)
}

func NewEnvVarFactory(delegate scraper.Factory, envMap common.EnvMap) scraper.Factory {
	return scraper.NewFactory(delegate.Type(), func() component.Config {
		return delegate.CreateDefaultConfig()
	}, scraper.WithMetrics(func(ctx context.Context, settings scraper.Settings, config component.Config) (scraper.Metrics, error) {
		scrp, err := delegate.CreateMetrics(ctx, settings, config)
		if err != nil {
			return nil, err
		}
		return &envVarScraper{delegate: scrp, envMap: envMap}, nil
	}, delegate.MetricsStability()))
}

type envVarScraper struct {
	delegate scraper.Metrics
	envMap   common.EnvMap
}

func (evs *envVarScraper) Start(ctx context.Context, host component.Host) error {
	ctx = context.WithValue(ctx, common.EnvKey, evs.envMap)
	return evs.delegate.Start(ctx, host)
}

func (evs *envVarScraper) ScrapeMetrics(ctx context.Context) (pmetric.Metrics, error) {
	ctx = context.WithValue(ctx, common.EnvKey, evs.envMap)
	return evs.delegate.ScrapeMetrics(ctx)
}

func (evs *envVarScraper) Shutdown(ctx context.Context) error {
	ctx = context.WithValue(ctx, common.EnvKey, evs.envMap)
	return evs.delegate.Shutdown(ctx)
}

// NewResourceAttributeFactory wraps delegate so that every resource it emits also carries the host
// identity attributes enabled in cfg. Host detection is deferred to Start, so building the factory
// performs no I/O; source shares one detection across all the scrapers given the same instance.
//
// Pass withServiceInstanceID=false for scrapers that emit one resource per observed subject rather
// than one for the host. delegate is returned unwrapped when it would gain nothing.
func NewResourceAttributeFactory(
	delegate scraper.Factory,
	source *HostIdentitySource,
	cfg metadata.ResourceAttributesConfig,
	withServiceInstanceID bool,
) scraper.Factory {
	if !anyResourceAttributeEnabled(cfg, withServiceInstanceID) {
		return delegate
	}

	return scraper.NewFactory(delegate.Type(), func() component.Config {
		return delegate.CreateDefaultConfig()
	}, scraper.WithMetrics(func(ctx context.Context, settings scraper.Settings, config component.Config) (scraper.Metrics, error) {
		scrp, err := delegate.CreateMetrics(ctx, settings, config)
		if err != nil {
			return nil, err
		}
		return &resourceAttributeScraper{
			delegate:              scrp,
			source:                source,
			cfg:                   cfg,
			withServiceInstanceID: withServiceInstanceID,
			attrs:                 pcommon.NewMap(),
		}, nil
	}, delegate.MetricsStability()))
}

// resourceAttributeScraper adds a fixed set of resource attributes to everything its delegate emits.
type resourceAttributeScraper struct {
	delegate              scraper.Metrics
	source                *HostIdentitySource
	cfg                   metadata.ResourceAttributesConfig
	withServiceInstanceID bool

	// attrs is resolved in Start and only read afterwards. It starts out empty so ScrapeMetrics
	// stays safe if Start never ran.
	attrs pcommon.Map
}

func (ras *resourceAttributeScraper) Start(ctx context.Context, host component.Host) error {
	ras.attrs = ras.source.Get(ctx).ResourceAttributes(ras.cfg, ras.withServiceInstanceID)
	return ras.delegate.Start(ctx, host)
}

func (ras *resourceAttributeScraper) ScrapeMetrics(ctx context.Context) (pmetric.Metrics, error) {
	metrics, err := ras.delegate.ScrapeMetrics(ctx)

	// Scrapers routinely return usable metrics alongside a partial error, so annotate regardless.
	rms := metrics.ResourceMetrics()
	for i := range rms.Len() {
		dst := rms.At(i).Resource().Attributes()
		for k, v := range ras.attrs.All() {
			v.CopyTo(dst.PutEmpty(k))
		}
	}

	return metrics, err
}

func (ras *resourceAttributeScraper) Shutdown(ctx context.Context) error {
	return ras.delegate.Shutdown(ctx)
}
