// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"testing"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/exemplar"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/scrape"
	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func newSuffixLookupTransaction(t *testing.T, meta testMetadataStore) *transaction {
	tr := newTxn(t, true)
	tr.ctx = scrape.ContextWithMetricMetadataStore(tr.ctx, meta)
	tr.mc = meta
	return tr
}

func appendSuffixLookupSample(t *testing.T, tr *transaction, name, series string, value float64) {
	_, err := tr.Append(0, labels.FromStrings("__name__", name, "job", "job-a", "instance", "localhost:1234", "series", series), 0, ts, value, nil, nil, storage.AOptions{})
	require.NoError(t, err)
}

func TestTransactionSuffixLookupAfterCanonicalReplacement(t *testing.T) {
	tr := newSuffixLookupTransaction(t, testMetadataStore{"foo": {MetricFamily: "foo", Type: model.MetricTypeGauge}})
	appendSuffixLookupSample(t, tr, "foo_sum", "first", 1)
	appendSuffixLookupSample(t, tr, "foo_count", "middle", 2)
	appendSuffixLookupSample(t, tr, "foo_sum", "last", 3)
	metrics, err := tr.getMetrics()
	require.NoError(t, err)
	ms := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	require.Equal(t, 1, ms.Len())
	metric := ms.At(0)
	require.Equal(t, "foo", metric.Name())
	require.Equal(t, pmetric.MetricTypeGauge, metric.Type())
	points := metric.Gauge().DataPoints()
	require.Equal(t, 1, points.Len())
	point := points.At(0)
	series, ok := point.Attributes().Get("series")
	require.True(t, ok)
	require.Equal(t, "last", series.Str())
	require.Equal(t, float64(3), point.DoubleValue())
}

func TestGetOrCreateMetricFamily_SuffixLookupAfterCanonicalReplacement(t *testing.T) {
	tr := newSuffixLookupTransaction(t, testMetadataStore{
		"foo":       {MetricFamily: "foo", Type: model.MetricTypeCounter},
		"foo_total": {MetricFamily: "foo_total", Type: model.MetricTypeGauge},
	})
	rk := resourceKey{job: "job-a", instance: "localhost:1234"}
	original := tr.getOrCreateMetricFamily(rk, emptyScopeID, "foo_created")
	require.Equal(t, pmetric.MetricTypeSum, original.mtype)

	// A nested suffix resolves through gauge metadata and replaces foo_total.
	replacement := tr.getOrCreateMetricFamily(rk, emptyScopeID, "foo_total_sum")
	require.Equal(t, pmetric.MetricTypeGauge, replacement.mtype)
	require.Equal(t, "foo_total", replacement.name)

	resolved := tr.getOrCreateMetricFamily(rk, emptyScopeID, "foo_created")
	require.NotSame(t, original, resolved)
	require.Equal(t, pmetric.MetricTypeSum, resolved.mtype)
	require.Same(t, resolved, tr.families[rk][emptyScopeID][metricFamilyKey{name: resolved.name}])
	require.Same(t, resolved, tr.getOrCreateMetricFamily(rk, emptyScopeID, "foo_created"))
	require.Same(t, resolved, tr.getOrCreateMetricFamily(rk, emptyScopeID, "foo_total"))
}

func TestTransactionCounterCreatedLookupAfterCanonicalReplacement(t *testing.T) {
	tr := newSuffixLookupTransaction(t, testMetadataStore{
		"foo":       {MetricFamily: "foo", Type: model.MetricTypeCounter},
		"foo_total": {MetricFamily: "foo_total", Type: model.MetricTypeGauge},
	})
	appendSuffixLookupSample(t, tr, "foo_created", "first", 100)
	appendSuffixLookupSample(t, tr, "foo_total", "first", 1)
	appendSuffixLookupSample(t, tr, "foo_total_sum", "middle", 2)
	appendSuffixLookupSample(t, tr, "foo_created", "last", 300)
	appendSuffixLookupSample(t, tr, "foo_total", "last", 3)

	metrics, err := tr.getMetrics()
	require.NoError(t, err)
	ms := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	require.Equal(t, 1, ms.Len())
	metric := ms.At(0)
	require.Equal(t, "foo_total", metric.Name())
	require.Equal(t, pmetric.MetricTypeSum, metric.Type())
	points := metric.Sum().DataPoints()
	require.Equal(t, 1, points.Len())
	point := points.At(0)
	series, ok := point.Attributes().Get("series")
	require.True(t, ok)
	require.Equal(t, "last", series.Str())
	require.Equal(t, float64(3), point.DoubleValue())
	require.Equal(t, uint64(300_000_000_000), uint64(point.StartTimestamp()))
}

func TestTransactionCounterCreatedLookupAndExemplar(t *testing.T) {
	tr := newSuffixLookupTransaction(t, testMetadataStore{"requests": {MetricFamily: "requests", Type: model.MetricTypeCounter}})
	appendSuffixLookupSample(t, tr, "requests_total", "a", 10)
	appendSuffixLookupSample(t, tr, "requests_created", "a", 100)
	appendSuffixLookupSample(t, tr, "requests_total", "b", 20)
	appendSuffixLookupSample(t, tr, "requests_created", "b", 200)
	err := tr.appendExemplar(labels.FromStrings("__name__", "requests_total", "job", "job-a", "instance", "localhost:1234", "series", "a"), exemplar.Exemplar{Value: 9, Ts: ts})
	require.NoError(t, err)
	metrics, err := tr.getMetrics()
	require.NoError(t, err)
	ms := metrics.ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	require.Equal(t, 1, ms.Len())
	require.Equal(t, pmetric.MetricTypeSum, ms.At(0).Type())
	points := ms.At(0).Sum().DataPoints()
	require.Equal(t, 2, points.Len())
	require.Equal(t, uint64(100_000_000_000), uint64(points.At(0).StartTimestamp()))
	require.Equal(t, uint64(200_000_000_000), uint64(points.At(1).StartTimestamp()))
	require.Equal(t, 1, points.At(0).Exemplars().Len())
	require.Equal(t, 0, points.At(1).Exemplars().Len())
}

func TestGetOrCreateMetricFamily_CacheContext(t *testing.T) {
	tr := newSuffixLookupTransaction(t, testMetadataStore{
		"latency": {MetricFamily: "latency", Type: model.MetricTypeHistogram},
	})
	type contextKey struct {
		resource resourceKey
		scope    scopeID
		native   bool
	}
	a := contextKey{resource: resourceKey{job: "job-a", instance: "localhost:1234"}}
	b := contextKey{resource: resourceKey{job: "job-b", instance: "localhost:5678"}}
	scoped := contextKey{resource: a.resource, scope: scopeID{name: "other-scope"}}
	native := contextKey{resource: a.resource, native: true}
	families := make(map[contextKey]*metricFamily)
	for _, key := range []contextKey{a, b, scoped, a, native, a, native, b, scoped} {
		tr.addingNativeHistogram = key.native
		family := tr.getOrCreateMetricFamily(key.resource, key.scope, "latency_bucket")
		if existing, ok := families[key]; ok {
			require.Same(t, existing, family)
		} else {
			for _, other := range families {
				require.NotSame(t, other, family)
			}
			families[key] = family
		}
		require.Same(t, family, tr.getOrCreateMetricFamily(key.resource, key.scope, "latency_bucket"))
	}
}
