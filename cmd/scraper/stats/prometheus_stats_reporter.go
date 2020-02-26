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

package stats

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	destinationNsLabel     = "destination_namespace"
	destinationConfigLabel = "destination_configuration"
	destinationRevLabel    = "destination_revision"
	destinationPodLabel    = "destination_pod"
)

var (
	metricLabelNames = []string{
		destinationNsLabel,
		destinationConfigLabel,
		destinationRevLabel,
		destinationPodLabel,
	}
)

func newGV(n, h string) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{Name: n, Help: h},
		metricLabelNames,
	)
}

// PrometheusStatsReporter structure represents a prometheus stats reporter.
type PrometheusStatsRecorder struct {
	startTime       time.Time
	reportingPeriod time.Duration
	labels          prometheus.Labels

	requestsPerSecond                prometheus.Gauge
	proxiedRequestsPerSecond         prometheus.Gauge
	averageConcurrentRequests        prometheus.Gauge
	averageProxiedConcurrentRequests prometheus.Gauge
	processUptime                    prometheus.Gauge
}

type PrometheusStatsReporter struct {
	registry        *prometheus.Registry
	handler         http.Handler
	reportingPeriod time.Duration

	requestsPerSecondGV                *prometheus.GaugeVec
	proxiedRequestsPerSecondGV         *prometheus.GaugeVec
	averageConcurrentRequestsGV        *prometheus.GaugeVec
	averageProxiedConcurrentRequestsGV *prometheus.GaugeVec
	processUptimeGV                    *prometheus.GaugeVec
}

// NewPrometheusStatsReporter creates a reporter that collects and reports queue metrics.
func NewPrometheusStatsRecorder(namespace, config, revision, pod string, r *PrometheusStatsReporter) (*PrometheusStatsRecorder, error) {
	if namespace == "" {
		return nil, errors.New("namespace must not be empty")
	}
	if config == "" {
		return nil, errors.New("config must not be empty")
	}
	if revision == "" {
		return nil, errors.New("revision must not be empty")
	}
	if pod == "" {
		return nil, errors.New("pod must not be empty")
	}

	labels := prometheus.Labels{
		destinationNsLabel:     namespace,
		destinationConfigLabel: config,
		destinationRevLabel:    revision,
		destinationPodLabel:    pod,
	}

	return &PrometheusStatsRecorder{
		startTime:       time.Now(),
		reportingPeriod: r.reportingPeriod,
		labels:          labels,

		requestsPerSecond:                r.requestsPerSecondGV.With(labels),
		proxiedRequestsPerSecond:         r.proxiedRequestsPerSecondGV.With(labels),
		averageConcurrentRequests:        r.averageConcurrentRequestsGV.With(labels),
		averageProxiedConcurrentRequests: r.averageProxiedConcurrentRequestsGV.With(labels),
		processUptime:                    r.processUptimeGV.With(labels),
	}, nil
}

func NewPrometheusStatsReporter(nodeName string, reportingPeriod time.Duration) (*PrometheusStatsReporter, error) {
	registry := prometheus.NewRegistry()

	// For backwards compatibility, the name is kept as `operations_per_second`.
	requestsPerSecondGV := newGV(
		"queue_requests_per_second",
		"Number of requests per second")
	proxiedRequestsPerSecondGV := newGV(
		"queue_proxied_operations_per_second",
		"Number of proxied requests per second")
	averageConcurrentRequestsGV := newGV(
		"queue_average_concurrent_requests",
		"Number of requests currently being handled by this pod")
	averageProxiedConcurrentRequestsGV := newGV(
		"queue_average_proxied_concurrent_requests",
		"Number of proxied requests currently being handled by this pod")
	processUptimeGV := newGV(
		"process_uptime",
		"The number of seconds that the process has been up")

	for _, gv := range []*prometheus.GaugeVec{
		requestsPerSecondGV, proxiedRequestsPerSecondGV,
		averageConcurrentRequestsGV, averageProxiedConcurrentRequestsGV,
		processUptimeGV} {
		if err := registry.Register(gv); err != nil {
			return nil, fmt.Errorf("register metric failed: %w", err)
		}
	}

	return &PrometheusStatsReporter{
		registry: registry,
		handler:  promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),

		reportingPeriod:                    reportingPeriod,
		requestsPerSecondGV:                requestsPerSecondGV,
		proxiedRequestsPerSecondGV:         proxiedRequestsPerSecondGV,
		averageConcurrentRequestsGV:        averageConcurrentRequestsGV,
		averageProxiedConcurrentRequestsGV: averageProxiedConcurrentRequestsGV,
		processUptimeGV:                    processUptimeGV,
	}, nil
}

// Report captures request metrics.
func (r *PrometheusStatsRecorder) Report(acr float64, apcr float64, rc float64, prc float64) {
	// Requests per second is a rate over time while concurrency is not.
	rp := r.reportingPeriod.Seconds()
	r.requestsPerSecond.Set(rc / rp)
	r.proxiedRequestsPerSecond.Set(prc / rp)
	r.averageConcurrentRequests.Set(acr)
	r.averageProxiedConcurrentRequests.Set(apcr)
	r.processUptime.Set(time.Since(r.startTime).Seconds())
}

// Handler returns an uninstrumented http.Handler used to serve stats registered by this
// PrometheusStatsReporter.
func (r *PrometheusStatsReporter) Handler() http.Handler {
	return r.handler
}

func (r *PrometheusStatsReporter) Unregister(recorder *PrometheusStatsRecorder) bool {
	return r.requestsPerSecondGV.Delete(recorder.labels) &&
		r.averageConcurrentRequestsGV.Delete(recorder.labels) &&
		r.averageProxiedConcurrentRequestsGV.Delete(recorder.labels) &&
		r.proxiedRequestsPerSecondGV.Delete(recorder.labels) &&
		r.processUptimeGV.Delete(recorder.labels)
}
