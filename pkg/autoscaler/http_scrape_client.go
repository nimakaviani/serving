/*
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package autoscaler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type httpScrapeClient struct {
	httpClient *http.Client
}

func NewHTTPScrapeClient(httpClient *http.Client) (*httpScrapeClient, error) {
	if httpClient == nil {
		return nil, errors.New("HTTP client must not be nil")
	}

	return &httpScrapeClient{
		httpClient: httpClient,
	}, nil
}

func (c *httpScrapeClient) Scrape(url string) (Stat, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return emptyStat, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return emptyStat, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return emptyStat, fmt.Errorf("GET request for URL %q returned HTTP status %v", url, resp.StatusCode)
	}

	return extractData(resp.Body)
}

func (c *httpScrapeClient) BulkScrape(url string) ([]Stat, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return emptyBulkStat, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return emptyBulkStat, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return emptyBulkStat, fmt.Errorf("GET request for URL %q returned HTTP status %v", url, resp.StatusCode)
	}

	return extractBulkData(url, resp.Body)
}

func extractBulkData(url string, body io.Reader) ([]Stat, error) {
	var parser expfmt.TextParser
	bulkMetricFamilies, err := parser.TextToMetricFamilies(body)
	if err != nil {
		return emptyBulkStat, fmt.Errorf("reading text format failed: %w", err)
	}

	stats := map[string]Stat{}
	for _, m := range []string{
		"queue_average_concurrent_requests",
		"queue_average_proxied_concurrent_requests",
		"queue_requests_per_second",
		"queue_proxied_operations_per_second",
	} {
		pms := prometheusBulkMetric(bulkMetricFamilies, m)
		if pms == nil {
			return emptyBulkStat, nil
		}

		for _, pm := range pms {
			podName := prometheusLabel(pm.Label, "destination_pod")
			if podName == "" {
				continue
			}

			revName := prometheusLabel(pm.Label, "destination_revision")
			if revName == "" {
				continue
			}

			var (
				stat Stat
				ok   bool
			)
			if stat, ok = stats[podName]; !ok {
				stat = emptyStat
				stat.Time = time.Now()
				stat.PodName = podName
				stat.RevisionName = revName
			}

			metricMap := map[string]*float64{
				"queue_average_concurrent_requests":         &stat.AverageConcurrentRequests,
				"queue_average_proxied_concurrent_requests": &stat.AverageProxiedConcurrentRequests,
				"queue_requests_per_second":                 &stat.RequestCount,
				"queue_proxied_operations_per_second":       &stat.ProxiedRequestCount,
			}
			if pv, ok := metricMap[m]; ok {
				*pv = *pm.Gauge.Value
				stats[podName] = stat
			}
		}
	}

	fmt.Printf(">> %s -- stats: %#v\n", url, stats)

	statArray := make([]Stat, 0, len(stats))
	for _, s := range stats {
		statArray = append(statArray, s)
	}
	return statArray, nil
}

func extractData(body io.Reader) (Stat, error) {
	var parser expfmt.TextParser
	metricFamilies, err := parser.TextToMetricFamilies(body)
	if err != nil {
		return emptyStat, fmt.Errorf("reading text format failed: %w", err)
	}

	stat := emptyStat
	for m, pv := range map[string]*float64{
		"queue_average_concurrent_requests":         &stat.AverageConcurrentRequests,
		"queue_average_proxied_concurrent_requests": &stat.AverageProxiedConcurrentRequests,
		"queue_requests_per_second":                 &stat.RequestCount,
		"queue_proxied_operations_per_second":       &stat.ProxiedRequestCount,
	} {
		pm := prometheusMetric(metricFamilies, m)
		if pm == nil {
			return emptyStat, fmt.Errorf("could not find value for %s in response", m)
		}
		*pv = *pm.Gauge.Value

		if stat.PodName == "" {
			stat.PodName = prometheusLabel(pm.Label, "destination_pod")
			if stat.PodName == "" {
				return emptyStat, errors.New("could not find pod name in metric labels")
			}
		}
	}
	// Transitional metrics, which older pods won't report.
	for m, pv := range map[string]*float64{
		"process_uptime": &stat.ProcessUptime, // Can be removed after 0.15 cuts.
	} {
		pm := prometheusMetric(metricFamilies, m)
		// Ignore if not found.
		if pm == nil {
			continue
		}
		*pv = *pm.Gauge.Value
	}
	return stat, nil
}

// prometheusMetric returns the point of the first Metric of the MetricFamily
// with the given key from the given map. If there is no such MetricFamily or it
// has no Metrics, then returns nil.
func prometheusMetric(metricFamilies map[string]*dto.MetricFamily, key string) *dto.Metric {
	if metric, ok := metricFamilies[key]; ok && len(metric.Metric) > 0 {
		return metric.Metric[0]
	}

	return nil
}

func prometheusBulkMetric(metricFamilies map[string]*dto.MetricFamily, key string) []*dto.Metric {
	if metric, ok := metricFamilies[key]; ok && len(metric.Metric) > 0 {
		// for i, m := range metric.Metric {
		// 	for _, l := range m.Label {
		// println(">>>> ", i, ". label ", l.GetName(), ": ", l.GetValue())
		// }
		// fmt.Printf(">> %d.metric[%s].value %v\n", i, key, *m.Gauge.Value)
		// }
		return metric.Metric
	}

	return nil
}

// prometheusLabels returns the value of the label with the given key from the
// given slice of labels. Returns an empty string if the label cannot be found.
func prometheusLabel(labels []*dto.LabelPair, key string) string {
	for _, label := range labels {
		if *label.Name == key {
			return *label.Value
		}
	}

	return ""
}
