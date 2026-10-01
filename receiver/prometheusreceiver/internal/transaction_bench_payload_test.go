// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/textparse"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

var scrapePayloadExemplarModes = []struct {
	name          string
	withExemplars bool
}{
	{"NoExemplars", false},
	{"SyntheticExemplars", true},
}

func parseScrapePayload(t *testing.T, data []byte, contentType string, withExemplars bool) map[string]pmetric.Metric {
	t.Helper()
	meta := make(testMetadataStore)
	tr := newSuffixLookupTransaction(t, meta)
	fallbackLabels := labels.EmptyLabels()
	if withExemplars {
		fallbackLabels = labels.FromStrings("trace_id", "0102030405060708090a0b0c0d0e0f10", "span_id", "0102030405060708")
	}
	parseAndAppend(t, tr, meta, data, contentType, labels.NewSymbolTable(), fallbackLabels)
	metrics, err := tr.getMetrics()
	require.NoError(t, err)
	families := make(map[string]pmetric.Metric)
	for _, rm := range metrics.ResourceMetrics().All() {
		for _, sm := range rm.ScopeMetrics().All() {
			for _, metric := range sm.Metrics().All() {
				require.NotContains(t, families, metric.Name())
				families[metric.Name()] = metric
			}
		}
	}
	return families
}

func requireScrapePayloadExemplars(t *testing.T, exemplars pmetric.ExemplarSlice, withExemplars bool) {
	t.Helper()
	if withExemplars {
		require.Positive(t, exemplars.Len())
	} else {
		require.Zero(t, exemplars.Len())
	}
}

func TestScrapePayloadClassicHistogram(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		payload     func(bool) []byte
	}{
		{"Protobuf", protoType, func(withExemplars bool) []byte {
			return generateProtobufClassicHistogramPayload(100, withExemplars)
		}},
		{"Text", promTextType, func(bool) []byte {
			return generatePromTextClassicHistogramPayload(100)
		}},
	} {
		for _, mode := range scrapePayloadExemplarModes {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				families := parseScrapePayload(t, tc.payload(mode.withExemplars), tc.contentType, mode.withExemplars)
				require.Len(t, families, 10)
				bounds := []float64{0, 5, 10, 25, 50, 75, 100, 250, 500, 750, 1000, 2500, 5000, 7500, 10000}
				counts := []uint64{10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10, 10}
				for family := range 10 {
					name := fmt.Sprintf("bench_classic_hist_%d", family)
					metric, ok := families[name]
					require.True(t, ok, "missing family %s", name)
					require.Equal(t, pmetric.MetricTypeHistogram, metric.Type())
					points := make(map[string]pmetric.HistogramDataPoint)
					for _, point := range metric.Histogram().DataPoints().All() {
						pod, ok := point.Attributes().Get("pod")
						require.True(t, ok)
						require.NotContains(t, points, pod.Str())
						points[pod.Str()] = point
					}
					require.Len(t, points, 10)
					for series := range 10 {
						point, ok := points[fmt.Sprintf("pod_%d", series)]
						require.True(t, ok)
						require.Equal(t, bounds, point.ExplicitBounds().AsRaw())
						require.Equal(t, counts, point.BucketCounts().AsRaw())
						require.Equal(t, uint64(160), point.Count())
						require.True(t, point.HasSum())
						require.Equal(t, 45.67, point.Sum())
						requireScrapePayloadExemplars(t, point.Exemplars(), mode.withExemplars)
					}
				}
			})
		}
	}
}

func TestScrapePayloadNativeHistogram(t *testing.T) {
	for _, mode := range scrapePayloadExemplarModes {
		t.Run(mode.name, func(t *testing.T) {
			payload := generateProtobufNativeHistogramPayload(100, mode.withExemplars)
			// Validate fixtures separately so benchmark timings exclude validation.
			parser, err := textparse.New(payload, protoType, labels.NewSymbolTable(), textparse.ParserOptions{})
			require.NoError(t, err)
			parsed := 0
			for {
				entry, err := parser.Next()
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(t, err)
				if entry == textparse.EntryHistogram {
					_, _, h, fh := parser.Histogram()
					require.NotNil(t, h)
					require.Nil(t, fh)
					require.NoError(t, h.Validate())
					parsed++
				}
			}
			require.Equal(t, 100, parsed)

			families := parseScrapePayload(t, payload, protoType, mode.withExemplars)
			require.Len(t, families, 10)
			for family := range 10 {
				name := fmt.Sprintf("bench_native_hist_%d", family)
				metric, ok := families[name]
				require.True(t, ok, "missing family %s", name)
				require.Equal(t, pmetric.MetricTypeExponentialHistogram, metric.Type())
				points := make(map[string]pmetric.ExponentialHistogramDataPoint)
				for _, point := range metric.ExponentialHistogram().DataPoints().All() {
					pod, ok := point.Attributes().Get("pod")
					require.True(t, ok)
					require.NotContains(t, points, pod.Str())
					points[pod.Str()] = point
				}
				require.Len(t, points, 10)
				for series := range 10 {
					point, ok := points[fmt.Sprintf("pod_%d", series)]
					require.True(t, ok)
					require.Equal(t, int32(3), point.Scale())
					require.Equal(t, int32(-1), point.Positive().Offset())
					require.Equal(t, []uint64{10, 15, 12, 14}, point.Positive().BucketCounts().AsRaw())
					require.Zero(t, point.Negative().BucketCounts().Len())
					require.Equal(t, float64(0.001), point.ZeroThreshold())
					require.Equal(t, uint64(2), point.ZeroCount())
					require.Equal(t, uint64(53), point.Count())
					require.True(t, point.HasSum())
					require.Equal(t, 1004.78, point.Sum())
					total := point.ZeroCount()
					for _, count := range point.Positive().BucketCounts().All() {
						total += count
					}
					require.Equal(t, point.Count(), total)
					requireScrapePayloadExemplars(t, point.Exemplars(), mode.withExemplars)
				}
			}
		})
	}
}

func TestScrapePayloadExemplars(t *testing.T) {
	for _, tc := range []struct {
		name         string
		points       int
		expectedType pmetric.MetricType
		payload      func(bool) []byte
	}{
		{"Counter", 100, pmetric.MetricTypeSum, func(withExemplars bool) []byte {
			return generateProtobufCounterPayload(100, withExemplars)
		}},
		{"Gauge", 100, pmetric.MetricTypeGauge, func(bool) []byte {
			return generateProtobufGaugePayload(100)
		}},
		{"Summary", 100, pmetric.MetricTypeSummary, func(bool) []byte {
			return generateProtobufSummaryPayload(100)
		}},
		{"Mixed", 660, pmetric.MetricTypeEmpty, generateProtobufMixedPayload},
	} {
		for _, mode := range scrapePayloadExemplarModes {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				families := parseScrapePayload(t, tc.payload(mode.withExemplars), protoType, mode.withExemplars)
				count := 0
				for _, metric := range families {
					if tc.expectedType != pmetric.MetricTypeEmpty {
						require.Equal(t, tc.expectedType, metric.Type())
					}
					switch metric.Type() {
					case pmetric.MetricTypeSum:
						for _, point := range metric.Sum().DataPoints().All() {
							requireScrapePayloadExemplars(t, point.Exemplars(), mode.withExemplars)
							count++
						}
					case pmetric.MetricTypeGauge:
						for _, point := range metric.Gauge().DataPoints().All() {
							requireScrapePayloadExemplars(t, point.Exemplars(), mode.withExemplars)
							count++
						}
					case pmetric.MetricTypeHistogram:
						for _, point := range metric.Histogram().DataPoints().All() {
							requireScrapePayloadExemplars(t, point.Exemplars(), mode.withExemplars)
							count++
						}
					case pmetric.MetricTypeExponentialHistogram:
						for _, point := range metric.ExponentialHistogram().DataPoints().All() {
							requireScrapePayloadExemplars(t, point.Exemplars(), mode.withExemplars)
							count++
						}
					case pmetric.MetricTypeSummary:
						for _, point := range metric.Summary().DataPoints().All() {
							require.Equal(t, 5, point.QuantileValues().Len())
							require.Equal(t, uint64(50), point.Count())
							require.Equal(t, 23.45, point.Sum())
							count++
						}
					default:
						t.Fatalf("unexpected metric type %s", metric.Type())
					}
				}
				require.Equal(t, tc.points, count)
			})
		}
	}
}
