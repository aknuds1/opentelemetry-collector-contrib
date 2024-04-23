// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusremotewrite

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/timestamp"
	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	conventions "go.opentelemetry.io/collector/semconv/v1.6.1"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/coreinternal/testdata"
)

func Test_isValidAggregationTemporality(t *testing.T) {
	l := pcommon.NewMap()

	tests := []struct {
		name   string
		metric pmetric.Metric
		want   bool
	}{
		{
			name: "summary",
			metric: func() pmetric.Metric {
				quantiles := pmetric.NewSummaryDataPointValueAtQuantileSlice()
				quantiles.AppendEmpty().SetValue(1)
				return getSummaryMetric("", l, 0, 0, 0, quantiles)
			}(),
			want: true,
		},
		{
			name:   "gauge",
			metric: getIntGaugeMetric("", l, 0, 0),
			want:   true,
		},
		{
			name:   "cumulative sum",
			metric: getIntSumMetric("", l, pmetric.AggregationTemporalityCumulative, 0, 0),
			want:   true,
		},
		{
			name: "cumulative histogram",
			metric: getHistogramMetric(
				"", l, pmetric.AggregationTemporalityCumulative, 0, 0, 0, []float64{}, []uint64{}),
			want: true,
		},
		{
			name: "cumulative exponential histogram",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				h := metric.SetEmptyExponentialHistogram()
				h.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
				return metric
			}(),
			want: true,
		},
		{
			name:   "missing type",
			metric: pmetric.NewMetric(),
			want:   false,
		},
		{
			name:   "unspecified sum temporality",
			metric: getIntSumMetric("", l, pmetric.AggregationTemporalityUnspecified, 0, 0),
			want:   false,
		},
		{
			name:   "delta sum",
			metric: getIntSumMetric("", l, pmetric.AggregationTemporalityDelta, 0, 0),
			want:   false,
		},
		{
			name: "delta histogram",
			metric: getHistogramMetric(
				"", l, pmetric.AggregationTemporalityDelta, 0, 0, 0, []float64{}, []uint64{}),
			want: false,
		},
		{
			name: "delta exponential histogram",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				h := metric.SetEmptyExponentialHistogram()
				h.SetAggregationTemporality(pmetric.AggregationTemporalityDelta)
				return metric
			}(),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidAggregationTemporality(tt.metric)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestPrometheusConverter_AddSample verifies that PrometheusConverter.AddSample adds the sample to the correct time series.
func TestPrometheusConverter_AddSample(t *testing.T) {
	type testCase struct {
		metric pmetric.Metric
		sample prompb.Sample
		labels labelsAdapter
	}

	t.Run("empty_case", func(t *testing.T) {
		converter := NewPrometheusConverter()
		converter.AddSample(0, 0, nil)
		assert.Empty(t, converter.unique)
		assert.Empty(t, converter.conflicts)
	})

	tests := []struct {
		name     string
		testCase []testCase
		want     map[uint64]*prompb.TimeSeries
	}{
		{
			name: "two_points_same_ts_same_metric",
			testCase: []testCase{
				{
					metric: validMetrics1[validDoubleGauge],
					sample: getSample(floatVal1, msTime1),
					labels: promLbs1,
				},
				{
					metric: validMetrics1[validDoubleGauge],
					sample: getSample(floatVal2, msTime2),
					labels: promLbs1,
				},
			},
			want: twoPointsSameTs(),
		},
		{
			name: "two_points_different_ts_same_metric",
			testCase: []testCase{
				{
					sample: getSample(float64(intVal1), msTime1),
					labels: promLbs1,
				},
				{
					sample: getSample(float64(intVal1), msTime2),
					labels: promLbs2,
				},
			},
			want: twoPointsDifferentTs(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := NewPrometheusConverter()
			tc1 := tt.testCase[0]
			tc2 := tt.testCase[1]
			converter.AddSample(tc1.sample.Timestamp, tc1.sample.Value, tc1.labels)
			converter.AddSample(tc2.sample.Timestamp, tc2.sample.Value, tc2.labels)
			assert.Exactly(t, tt.want, converter.unique)
			assert.Empty(t, converter.conflicts)
		})
	}
}

// Test_TimeSeriesSignature checks that TimeSeriesSignature returns consistent and unique signatures for a distinct label set.
func Test_TimeSeriesSignature(t *testing.T) {
	var oneKBLabels []prompb.Label
	for i := 0; i < 100; i++ {
		const name = "12345"
		const value = "12345"
		oneKBLabels = append(oneKBLabels, prompb.Label{Name: name, Value: value})
	}

	tests := []struct {
		name   string
		lbs    labelsAdapter
		metric pmetric.Metric
	}{
		{
			"int64_signature",
			promLbs1,
			validMetrics1[validIntGauge],
		},
		{
			"histogram_signature",
			promLbs2,
			validMetrics1[validHistogram],
		},
		{
			"unordered_signature",
			getPromLabels(label22, value22, label21, value21),
			validMetrics1[validHistogram],
		},
		// descriptor type cannot be nil, as checked by validateAggregationTemporality
		{
			"nil_case",
			nil,
			validMetrics1[validHistogram],
		},
		{
			// Case that triggers optimized logic when exceeding 1 kb
			"greater_than_1kb_signature",
			oneKBLabels,
			validMetrics1[validIntGauge],
		},
	}

	calcSig := func(labels labelsAdapter) uint64 {
		labels.Sort()

		h := xxhash.New()
		for _, l := range labels {
			_, err := h.WriteString(l.Name)
			require.NoError(t, err)
			_, err = h.Write(seps)
			require.NoError(t, err)
			_, err = h.WriteString(l.Value)
			require.NoError(t, err)
			_, err = h.Write(seps)
			require.NoError(t, err)
		}

		return h.Sum64()
	}

	// run tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := calcSig(tt.lbs)
			sig := TimeSeriesSignature(tt.lbs)
			assert.EqualValues(t, exp, sig)
		})
	}
}

// Test_createLabelSet checks resultant label names are sanitized and label in extra overrides label in labels if
// collision happens. It does not check whether labels are not sorted
func Test_createLabelSet(t *testing.T) {
	tests := []struct {
		name           string
		resource       pcommon.Resource
		orig           pcommon.Map
		externalLabels map[string]string
		extras         []string
		want           []prompb.Label
	}{
		{
			"labels_clean",
			pcommon.NewResource(),
			lbs1,
			map[string]string{},
			[]string{label31, value31, label32, value32},
			getPromLabels(label11, value11, label12, value12, label31, value31, label32, value32),
		},
		{
			"labels_with_resource",
			func() pcommon.Resource {
				res := pcommon.NewResource()
				res.Attributes().PutStr("service.name", "prometheus")
				res.Attributes().PutStr("service.instance.id", "127.0.0.1:8080")
				return res
			}(),
			lbs1,
			map[string]string{},
			[]string{label31, value31, label32, value32},
			getPromLabels(label11, value11, label12, value12, label31, value31, label32, value32, "job", "prometheus", "instance", "127.0.0.1:8080"),
		},
		{
			"labels_with_nonstring_resource",
			func() pcommon.Resource {
				res := pcommon.NewResource()
				res.Attributes().PutInt("service.name", 12345)
				res.Attributes().PutBool("service.instance.id", true)
				return res
			}(),
			lbs1,
			map[string]string{},
			[]string{label31, value31, label32, value32},
			getPromLabels(label11, value11, label12, value12, label31, value31, label32, value32, "job", "12345", "instance", "true"),
		},
		{
			"labels_duplicate_in_extras",
			pcommon.NewResource(),
			lbs1,
			map[string]string{},
			[]string{label11, value31},
			getPromLabels(label11, value31, label12, value12),
		},
		{
			"labels_dirty",
			pcommon.NewResource(),
			lbs1Dirty,
			map[string]string{},
			[]string{label31 + dirty1, value31, label32, value32},
			getPromLabels(label11+"_", value11, "key_"+label12, value12, label31+"_", value31, label32, value32),
		},
		{
			"no_original_case",
			pcommon.NewResource(),
			pcommon.NewMap(),
			nil,
			[]string{label31, value31, label32, value32},
			getPromLabels(label31, value31, label32, value32),
		},
		{
			"empty_extra_case",
			pcommon.NewResource(),
			lbs1,
			map[string]string{},
			[]string{"", ""},
			getPromLabels(label11, value11, label12, value12, "", ""),
		},
		{
			"single_left_over_case",
			pcommon.NewResource(),
			lbs1,
			map[string]string{},
			[]string{label31, value31, label32},
			getPromLabels(label11, value11, label12, value12, label31, value31),
		},
		{
			"valid_external_labels",
			pcommon.NewResource(),
			lbs1,
			exlbs1,
			[]string{label31, value31, label32, value32},
			getPromLabels(label11, value11, label12, value12, label41, value41, label31, value31, label32, value32),
		},
		{
			"overwritten_external_labels",
			pcommon.NewResource(),
			lbs1,
			exlbs2,
			[]string{label31, value31, label32, value32},
			getPromLabels(label11, value11, label12, value12, label31, value31, label32, value32),
		},
		{
			"colliding attributes",
			pcommon.NewResource(),
			lbsColliding,
			nil,
			[]string{label31, value31, label32, value32},
			getPromLabels(collidingSanitized, value11+";"+value12, label31, value31, label32, value32),
		},
		{
			"sanitize_labels_starts_with_underscore",
			pcommon.NewResource(),
			lbs3,
			exlbs1,
			[]string{label31, value31, label32, value32},
			getPromLabels(label11, value11, label12, value12, "key"+label51, value51, label41, value41, label31, value31, label32, value32),
		},
	}
	// run tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := NewPrometheusConverter()
			assert.ElementsMatch(t, tt.want, createLabels(converter, tt.resource, tt.orig, tt.externalLabels, nil, true, tt.extras...))
		})
	}
}

func BenchmarkCreateAttributes(b *testing.B) {
	r := pcommon.NewResource()
	ext := map[string]string{}

	m := pcommon.NewMap()
	m.PutStr("test-string-key2", "test-value-2")
	m.PutStr("test-string-key1", "test-value-1")
	m.PutInt("test-int-key", 123)
	m.PutBool("test-bool-key", true)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		converter := NewPrometheusConverter()
		b.StartTimer()
		createLabels(converter, r, m, ext, nil, true)
	}
}

// TestPrometheusConverter_AddExemplars verifies that PrometheusConverter.AddExemplars adds exemplars correctly given bucket bounds data.
func TestPrometheusConverter_AddExemplars(t *testing.T) {
	ts1 := getTimeSeries(
		getPromLabels(label11, value11, label12, value12),
		getSample(float64(intVal1), msTime1),
	)
	ts2 := getTimeSeries(
		getPromLabels(label11, value11, label12, value12),
		getSample(float64(intVal1), msTime1),
	)
	tsMap1 := tsWithoutSampleAndExemplar()
	tests := []struct {
		name         string
		orig         map[uint64]*prompb.TimeSeries
		dataPoint    pmetric.HistogramDataPoint
		bucketBounds []BucketBoundsData[*prompb.TimeSeries]
		want         map[uint64]*prompb.TimeSeries
	}{
		{
			name:      "timeSeries_is_empty",
			orig:      map[uint64]*prompb.TimeSeries{},
			dataPoint: getHistogramDataPointWithExemplars(t, time.UnixMilli(msTime1), float64(intVal1), traceIDValue1, "", "", ""),
			bucketBounds: getBucketBoundsData(
				[]float64{1, 2, 3},
				getTimeSeries(getPromLabels(label11, value11, label12, value12), getSample(float64(intVal1), msTime1)),
			),
			want: map[uint64]*prompb.TimeSeries{},
		},
		{
			name:         "timeSeries_without_sample",
			orig:         tsMap1,
			dataPoint:    getHistogramDataPointWithExemplars(t, time.UnixMilli(msTime1), float64(intVal1), traceIDValue1, "", "", ""),
			bucketBounds: getBucketBoundsData([]float64{1, 2, 3}, tsMap1[lb1Sig]),
			want:         tsWithoutSampleAndExemplar(),
		},
		{
			name: "exemplar_value_less_than_bucket_bound",
			orig: map[uint64]*prompb.TimeSeries{
				lb1Sig: ts1,
			},
			dataPoint:    getHistogramDataPointWithExemplars(t, time.UnixMilli(msTime1), floatVal2, traceIDValue1, "", "", ""),
			bucketBounds: getBucketBoundsData([]float64{1, 2, 3}, ts1),
			want:         tsWithSamplesAndExemplars(),
		},
		{
			name: "infinite_bucket_bound",
			orig: map[uint64]*prompb.TimeSeries{
				lb1Sig: ts2,
			},
			dataPoint:    getHistogramDataPointWithExemplars(t, time.UnixMilli(msTime1), math.MaxFloat64, traceIDValue1, "", "", ""),
			bucketBounds: getBucketBoundsData([]float64{1, math.Inf(1)}, ts2),
			want:         tsWithInfiniteBoundExemplarValue(),
		},
	}
	// run tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			converter := &PrometheusConverter{
				unique: tt.orig,
			}
			converter.AddHistogramExemplars(ConvertExemplars(tt.dataPoint, converter), tt.bucketBounds)
			assert.Exactly(t, tt.want, converter.unique)
		})
	}
}

// Test_getPromExemplars checks if exemplars is not nul and return the prometheus exemplars.
func Test_getPromExemplars(t *testing.T) {
	tnow := time.Now()
	tests := []struct {
		name      string
		histogram pmetric.HistogramDataPoint
		expected  promExemplars
	}{
		{
			"with_exemplars",
			getHistogramDataPointWithExemplars(t, tnow, floatVal1, traceIDValue1, spanIDValue1, label11, value11),
			promExemplars{
				{
					Value:     floatVal1,
					Timestamp: timestamp.FromTime(tnow),
					Labels:    []prompb.Label{getLabel(traceIDKey, traceIDValue1), getLabel(spanIDKey, spanIDValue1), getLabel(label11, value11)},
				},
			},
		},
		{
			"with_exemplars_without_trace_or_span",
			getHistogramDataPointWithExemplars(t, tnow, floatVal1, "", "", label11, value11),
			promExemplars{
				{
					Value:     floatVal1,
					Timestamp: timestamp.FromTime(tnow),
					Labels:    []prompb.Label{getLabel(label11, value11)},
				},
			},
		},
		{
			"too_many_runes_drops_labels",
			getHistogramDataPointWithExemplars(t, tnow, floatVal1, "", "", keyWith129Runes, ""),
			promExemplars{
				{
					Value:     floatVal1,
					Timestamp: timestamp.FromTime(tnow),
					Labels:    []prompb.Label{},
				},
			},
		},
		{
			"runes_at_limit_bytes_over_keeps_labels",
			getHistogramDataPointWithExemplars(t, tnow, floatVal1, "", "", keyWith128Runes, ""),
			promExemplars{
				{
					Value:     floatVal1,
					Timestamp: timestamp.FromTime(tnow),
					Labels:    []prompb.Label{getLabel(keyWith128Runes, "")},
				},
			},
		},
		{
			"too_many_runes_with_exemplar_drops_attrs_keeps_exemplar",
			getHistogramDataPointWithExemplars(t, tnow, floatVal1, traceIDValue1, spanIDValue1, keyWith64Runes, ""),
			promExemplars{
				{
					Value:     floatVal1,
					Timestamp: timestamp.FromTime(tnow),
					Labels:    []prompb.Label{getLabel(traceIDKey, traceIDValue1), getLabel(spanIDKey, spanIDValue1)},
				},
			},
		},
		{
			"without_exemplar",
			pmetric.NewHistogramDataPoint(),
			promExemplars{},
		},
	}
	// run tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exemplars := ConvertExemplars(tt.histogram, NewPrometheusConverter())
			assert.Exactly(t, tt.expected, exemplars)
		})
	}
}

func TestAddResourceTargetInfo(t *testing.T) {
	resourceAttrMap := map[string]any{
		conventions.AttributeServiceName:       "service-name",
		conventions.AttributeServiceNamespace:  "service-namespace",
		conventions.AttributeServiceInstanceID: "service-instance-id",
	}
	resourceWithServiceAttrs := pcommon.NewResource()
	assert.NoError(t, resourceWithServiceAttrs.Attributes().FromRaw(resourceAttrMap))
	resourceWithServiceAttrs.Attributes().PutStr("resource_attr", "resource-attr-val-1")
	resourceWithOnlyServiceAttrs := pcommon.NewResource()
	assert.NoError(t, resourceWithOnlyServiceAttrs.Attributes().FromRaw(resourceAttrMap))
	for _, tc := range []struct {
		desc       string
		resource   pcommon.Resource
		settings   Settings
		timestamp  pcommon.Timestamp
		wantLabels labelsAdapter
	}{
		{
			desc:     "empty resource",
			resource: pcommon.NewResource(),
		},
		{
			desc:     "disable target info metric",
			resource: testdata.GenerateMetricsNoLibraries().ResourceMetrics().At(0).Resource(),
			settings: Settings{DisableTargetInfo: true},
		},
		{
			desc:      "with resource",
			resource:  testdata.GenerateMetricsNoLibraries().ResourceMetrics().At(0).Resource(),
			timestamp: testdata.TestMetricStartTimestamp,
			wantLabels: []prompb.Label{
				{
					Name:  model.MetricNameLabel,
					Value: targetMetricName,
				},
				{
					Name:  "resource_attr",
					Value: "resource-attr-val-1",
				},
			},
		},
		{
			desc:      "with resource, with namespace",
			resource:  testdata.GenerateMetricsNoLibraries().ResourceMetrics().At(0).Resource(),
			timestamp: testdata.TestMetricStartTimestamp,
			settings:  Settings{Namespace: "foo"},
			wantLabels: []prompb.Label{
				{
					Name:  model.MetricNameLabel,
					Value: fmt.Sprintf("foo_%s", targetMetricName),
				},
				{
					Name:  "resource_attr",
					Value: "resource-attr-val-1",
				},
			},
		},
		{
			desc:      "with resource, with service attributes",
			resource:  resourceWithServiceAttrs,
			timestamp: testdata.TestMetricStartTimestamp,
			wantLabels: []prompb.Label{
				{
					Name:  model.MetricNameLabel,
					Value: targetMetricName,
				},
				{
					Name:  "instance",
					Value: "service-instance-id",
				},
				{
					Name:  "job",
					Value: "service-namespace/service-name",
				},
				{
					Name:  "resource_attr",
					Value: "resource-attr-val-1",
				},
			},
		},
		{
			desc:      "with resource, with only service attributes",
			resource:  resourceWithOnlyServiceAttrs,
			timestamp: testdata.TestMetricStartTimestamp,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			converter := NewPrometheusConverter()

			addResourceTargetInfo(tc.resource, tc.settings, tc.timestamp, converter)

			if len(tc.wantLabels) == 0 || tc.settings.DisableTargetInfo {
				assert.Empty(t, converter.TimeSeries())
				return
			}

			expected := map[uint64]*prompb.TimeSeries{
				TimeSeriesSignature(tc.wantLabels): {
					Labels: tc.wantLabels,
					Samples: []prompb.Sample{
						{
							Value:     1,
							Timestamp: 1581452772000,
						},
					},
				},
			}
			assert.Exactly(t, expected, converter.unique)
			assert.Empty(t, converter.conflicts)
		})
	}
}

func TestMostRecentTimestampInMetric(t *testing.T) {
	laterTimestamp := pcommon.NewTimestampFromTime(testdata.TestMetricTime.Add(1 * time.Minute))
	metricMultipleTimestamps := testdata.GenerateMetricsOneMetric().ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics().At(0)
	// the first datapoint timestamp is at testdata.TestMetricTime
	metricMultipleTimestamps.Sum().DataPoints().At(1).SetTimestamp(laterTimestamp)
	for _, tc := range []struct {
		desc     string
		input    pmetric.Metric
		expected pcommon.Timestamp
	}{
		{
			desc:     "empty",
			input:    pmetric.NewMetric(),
			expected: pcommon.Timestamp(0),
		},
		{
			desc:     "multiple timestamps",
			input:    metricMultipleTimestamps,
			expected: laterTimestamp,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			got := MostRecentTimestampInMetric(tc.input)
			assert.Exactly(t, tc.expected, got)
		})
	}
}

func TestPrometheusConverter_AddSummaryDataPoints(t *testing.T) {
	ts := pcommon.Timestamp(time.Now().UnixNano())
	tests := []struct {
		name   string
		metric func() pmetric.Metric
		want   func() map[uint64]*prompb.TimeSeries
	}{
		{
			name: "summary with start time",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				metric.SetName("test_summary")
				metric.SetEmptySummary()

				dp := metric.Summary().DataPoints().AppendEmpty()
				dp.SetTimestamp(ts)
				dp.SetStartTimestamp(ts)

				return metric
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_summary" + countStr},
				}
				createdLabels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_summary" + createdSuffix},
				}
				sumLabels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_summary" + sumStr},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
					TimeSeriesSignature(sumLabels): {
						Labels: sumLabels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
					TimeSeriesSignature(createdLabels): {
						Labels: createdLabels,
						Samples: []prompb.Sample{
							{Value: float64(ConvertTimestamp(ts)), Timestamp: ConvertTimestamp(ts)},
						},
					},
				}
			},
		},
		{
			name: "summary without start time",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				metric.SetName("test_summary")
				metric.SetEmptySummary()

				dp := metric.Summary().DataPoints().AppendEmpty()
				dp.SetTimestamp(ts)

				return metric
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_summary" + countStr},
				}
				sumLabels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_summary" + sumStr},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
					TimeSeriesSignature(sumLabels): {
						Labels: sumLabels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metric := tt.metric()
			converter := NewPrometheusConverter()

			require.NoError(t, addSummaryDataPoints(
				converter,
				metric.Summary().DataPoints(),
				pcommon.NewResource(),
				Settings{
					ExportCreatedMetric: true,
				},
				metric.Name(),
			))

			assert.Equal(t, tt.want(), converter.unique)
			assert.Empty(t, converter.conflicts)
		})
	}
}

func TestPrometheusConverter_AddHistogramDataPoints(t *testing.T) {
	ts := pcommon.Timestamp(time.Now().UnixNano())
	tests := []struct {
		name   string
		metric func() pmetric.Metric
		want   func() map[uint64]*prompb.TimeSeries
	}{
		{
			name: "histogram with start time",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				metric.SetName("test_hist")
				metric.SetEmptyHistogram().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)

				pt := metric.Histogram().DataPoints().AppendEmpty()
				pt.SetTimestamp(ts)
				pt.SetStartTimestamp(ts)

				return metric
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_hist" + countStr},
				}
				createdLabels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_hist" + createdSuffix},
				}
				infLabels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_hist_bucket"},
					{Name: model.BucketLabel, Value: "+Inf"},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(infLabels): {
						Labels: infLabels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
					TimeSeriesSignature(createdLabels): {
						Labels: createdLabels,
						Samples: []prompb.Sample{
							{Value: float64(ConvertTimestamp(ts)), Timestamp: ConvertTimestamp(ts)},
						},
					},
				}
			},
		},
		{
			name: "histogram without start time",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				metric.SetName("test_hist")
				metric.SetEmptyHistogram().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)

				pt := metric.Histogram().DataPoints().AppendEmpty()
				pt.SetTimestamp(ts)

				return metric
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_hist" + countStr},
				}
				infLabels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_hist_bucket"},
					{Name: model.BucketLabel, Value: "+Inf"},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(infLabels): {
						Labels: infLabels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metric := tt.metric()
			converter := NewPrometheusConverter()

			addHistogramDataPoints(
				converter,
				metric.Histogram().DataPoints(),
				pcommon.NewResource(),
				Settings{
					ExportCreatedMetric: true,
				},
				metric.Name(),
			)

			assert.Equal(t, tt.want(), converter.unique)
			assert.Empty(t, converter.conflicts)
		})
	}
}

func TestPrometheusConverter_GetOrCreateTimeSeries(t *testing.T) {
	converter := NewPrometheusConverter()
	lbls := labelsAdapter{
		{
			Name:  "key1",
			Value: "value1",
		},
		{
			Name:  "key2",
			Value: "value2",
		},
	}
	ts, created := converter.GetOrCreateTimeSeries(lbls)
	require.NotNil(t, ts)
	require.True(t, created)

	// Now, get (not create) the unique time series
	gotTS, created := converter.GetOrCreateTimeSeries(labelsAdapter(ts.Labels))
	require.Same(t, ts, gotTS)
	require.False(t, created)

	var keys []uint64
	for k := range converter.unique {
		keys = append(keys, k)
	}
	require.Len(t, keys, 1)
	h := keys[0]

	// Make sure that state is correctly set
	require.Equal(t, map[uint64]*prompb.TimeSeries{
		h: ts,
	}, converter.unique)
	require.Empty(t, converter.conflicts)

	// Fake a hash collision, by making this not equal to the next series with the same hash
	ts.Labels = append(ts.Labels, prompb.Label{Name: "key3", Value: "value3"})

	// Make the first hash collision
	cTS1, created := converter.GetOrCreateTimeSeries(lbls)
	require.NotNil(t, cTS1)
	require.True(t, created)
	require.Equal(t, map[uint64][]*prompb.TimeSeries{
		h: {cTS1},
	}, converter.conflicts)

	// Fake a hash collision, by making this not equal to the next series with the same hash
	cTS1.Labels = append(cTS1.Labels, prompb.Label{Name: "key3", Value: "value3"})

	// Make the second hash collision
	cTS2, created := converter.GetOrCreateTimeSeries(lbls)
	require.NotNil(t, cTS2)
	require.True(t, created)
	require.Equal(t, map[uint64][]*prompb.TimeSeries{
		h: {cTS1, cTS2},
	}, converter.conflicts)

	// Now, get (not create) the second colliding time series
	gotCTS2, created := converter.GetOrCreateTimeSeries(lbls)
	require.Same(t, cTS2, gotCTS2)
	require.False(t, created)
	require.Equal(t, map[uint64][]*prompb.TimeSeries{
		h: {cTS1, cTS2},
	}, converter.conflicts)

	require.Equal(t, map[uint64]*prompb.TimeSeries{
		h: ts,
	}, converter.unique)
}

/*
func TestCreateLabels(t *testing.T) {
	testCases := []struct {
		name       string
		metricName string
		baseLabels labelsAdapter
		extras     []string
		expected   labelsAdapter
	}{
		{
			name:       "no base labels, no extras",
			metricName: "test",
			baseLabels: nil,
			expected: labelsAdapter{
				{Name: model.MetricNameLabel, Value: "test"},
			},
		},
		{
			name:       "base labels, no extras",
			metricName: "test",
			baseLabels: labelsAdapter{
				{Name: "base1", Value: "value1"},
				{Name: "base2", Value: "value2"},
			},
			expected: labelsAdapter{
				{Name: "base1", Value: "value1"},
				{Name: "base2", Value: "value2"},
				{Name: model.MetricNameLabel, Value: "test"},
			},
		},
		{
			name:       "base labels, 1 extra",
			metricName: "test",
			baseLabels: labelsAdapter{
				{Name: "base1", Value: "value1"},
				{Name: "base2", Value: "value2"},
			},
			extras: []string{"extra1", "extraValue1"},
			expected: labelsAdapter{
				{Name: "base1", Value: "value1"},
				{Name: "base2", Value: "value2"},
				{Name: "extra1", Value: "extraValue1"},
				{Name: model.MetricNameLabel, Value: "test"},
			},
		},
		{
			name:       "base labels, 2 extras",
			metricName: "test",
			baseLabels: labelsAdapter{
				{Name: "base1", Value: "value1"},
				{Name: "base2", Value: "value2"},
			},
			extras: []string{"extra1", "extraValue1", "extra2", "extraValue2"},
			expected: labelsAdapter{
				{Name: "base1", Value: "value1"},
				{Name: "base2", Value: "value2"},
				{Name: "extra1", Value: "extraValue1"},
				{Name: "extra2", Value: "extraValue2"},
				{Name: model.MetricNameLabel, Value: "test"},
			},
		},
		{
			name:       "base labels, unpaired extra",
			metricName: "test",
			baseLabels: labelsAdapter{
				{Name: "base1", Value: "value1"},
				{Name: "base2", Value: "value2"},
			},
			extras: []string{"extra1", "extraValue1", "extra2"},
			expected: labelsAdapter{
				{Name: "base1", Value: "value1"},
				{Name: "base2", Value: "value2"},
				{Name: "extra1", Value: "extraValue1"},
				{Name: model.MetricNameLabel, Value: "test"},
			},
		},
		{
			name:       "no base labels, 1 extra",
			metricName: "test",
			baseLabels: nil,
			extras:     []string{"extra1", "extraValue1"},
			expected: labelsAdapter{
				{Name: "extra1", Value: "extraValue1"},
				{Name: model.MetricNameLabel, Value: "test"},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lbls := createLabels(NewPrometheusConverter(), tc.metricName, tc.baseLabels, tc.extras...)
			assert.Equal(t, lbls, tc.expected)
		})
	}
}
*/
