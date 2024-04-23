// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusremotewrite

import (
	"testing"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestPrometheusConverter_AddGaugeNumberDataPoints(t *testing.T) {
	ts := uint64(time.Now().UnixNano())
	tests := []struct {
		name   string
		metric func() pmetric.Metric
		want   func() map[uint64]*prompb.TimeSeries
	}{
		{
			name: "gauge",
			metric: func() pmetric.Metric {
				return getIntGaugeMetric(
					"test",
					pcommon.NewMap(),
					1, ts,
				)
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test"},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{
							{
								Value:     1,
								Timestamp: ConvertTimestamp(pcommon.Timestamp(ts)),
							}},
					},
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metric := tt.metric()
			converter := NewPrometheusConverter()

			addGaugeNumberDataPoints(
				converter,
				metric.Gauge().DataPoints(),
				pcommon.NewResource(),
				Settings{},
				metric.Name(),
			)

			assert.Equal(t, tt.want(), converter.unique)
			assert.Empty(t, converter.conflicts)
		})
	}
}

func TestPrometheusConverter_AddSumNumberDataPoints(t *testing.T) {
	ts := pcommon.Timestamp(time.Now().UnixNano())
	tests := []struct {
		name   string
		metric func() pmetric.Metric
		want   func() map[uint64]*prompb.TimeSeries
	}{
		{
			name: "sum",
			metric: func() pmetric.Metric {
				return getIntSumMetric(
					"test",
					pcommon.NewMap(),
					pmetric.AggregationTemporalityCumulative,
					1, uint64(ts.AsTime().UnixNano()),
				)
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test"},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{
							{
								Value:     1,
								Timestamp: ConvertTimestamp(ts),
							}},
					},
				}
			},
		},
		{
			name: "sum with exemplars",
			metric: func() pmetric.Metric {
				m := getIntSumMetric(
					"test",
					pcommon.NewMap(),
					pmetric.AggregationTemporalityCumulative,
					1, uint64(ts.AsTime().UnixNano()),
				)
				m.Sum().DataPoints().At(0).Exemplars().AppendEmpty().SetDoubleValue(2)
				return m
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test"},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{{
							Value:     1,
							Timestamp: ConvertTimestamp(ts),
						}},
						Exemplars: []prompb.Exemplar{
							{Value: 2},
						},
					},
				}
			},
		},
		{
			name: "monotonic cumulative sum with start timestamp",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				metric.SetName("test_sum")
				metric.SetEmptySum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
				metric.SetEmptySum().SetIsMonotonic(true)

				dp := metric.Sum().DataPoints().AppendEmpty()
				dp.SetDoubleValue(1)
				dp.SetTimestamp(ts)
				dp.SetStartTimestamp(ts)

				return metric
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_sum"},
				}
				createdLabels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_sum" + createdSuffix},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{
							{Value: 1, Timestamp: ConvertTimestamp(ts)},
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
			name: "monotonic cumulative sum with no start time",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				metric.SetName("test_sum")
				metric.SetEmptySum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
				metric.SetEmptySum().SetIsMonotonic(true)

				dp := metric.Sum().DataPoints().AppendEmpty()
				dp.SetTimestamp(ts)

				return metric
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_sum"},
				}
				return map[uint64]*prompb.TimeSeries{
					TimeSeriesSignature(labels): {
						Labels: labels,
						Samples: []prompb.Sample{
							{Value: 0, Timestamp: ConvertTimestamp(ts)},
						},
					},
				}
			},
		},
		{
			name: "non-monotonic cumulative sum with start time",
			metric: func() pmetric.Metric {
				metric := pmetric.NewMetric()
				metric.SetName("test_sum")
				metric.SetEmptySum().SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
				metric.SetEmptySum().SetIsMonotonic(false)

				dp := metric.Sum().DataPoints().AppendEmpty()
				dp.SetTimestamp(ts)

				return metric
			},
			want: func() map[uint64]*prompb.TimeSeries {
				labels := labelsAdapter{
					{Name: model.MetricNameLabel, Value: "test_sum"},
				}
				return map[uint64]*prompb.TimeSeries{
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

			addSumNumberDataPoints(
				converter,
				metric.Sum().DataPoints(),
				pcommon.NewResource(),
				metric,
				Settings{ExportCreatedMetric: true},
				metric.Name(),
			)

			assert.Equal(t, tt.want(), converter.unique)
			assert.Empty(t, converter.conflicts)
		})
	}
}
