// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package prometheusreceiver

import (
	"testing"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/relabel"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pmetric"
)

func TestSuffixLookupRelabeledScrape(t *testing.T) {
	payload := "# TYPE foo gauge\nfoo 0\nfoo_sum{series=\"first\"} 1\nfoo_count{series=\"middle\"} 2\nbar_sum{series=\"last\"} 3\n"
	targets := []*testData{{
		name:  "suffix-alias",
		pages: []mockPrometheusResponse{{code: 200, data: payload}},
		validateFunc: func(t *testing.T, td *testData, rms []pmetric.ResourceMetrics) {
			verifyNumValidScrapeResults(t, td, rms)
			found := false
			for _, metric := range getMetrics(rms[0]) {
				if metric.Name() != "foo" || metric.Type() != pmetric.MetricTypeGauge {
					continue
				}
				points := metric.Gauge().DataPoints()
				for i := 0; i < points.Len(); i++ {
					point := points.At(i)
					series, ok := point.Attributes().Get("series")
					if !ok {
						continue
					}
					if series.Str() == "last" && point.DoubleValue() == 3 {
						found = true
					}
				}
			}
			require.True(t, found, "a valid scraped and relabeled final series must be emitted")
		},
	}}
	testComponent(t, targets, nil, func(cfg *PromConfig) {
		for _, sc := range cfg.ScrapeConfigs {
			config := relabel.DefaultRelabelConfig
			config.SourceLabels = model.LabelNames{"__name__"}
			config.Regex = relabel.MustNewRegexp("bar_sum")
			config.TargetLabel = "__name__"
			config.Replacement = "foo_sum"
			sc.MetricRelabelConfigs = []*relabel.Config{&config}
		}
	})
}
