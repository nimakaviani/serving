/*
Copyright 2020 The Knative Authors

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

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
	pkglogging "knative.dev/pkg/logging"
	"knative.dev/pkg/metrics"

	"sync"

	kubeclient "knative.dev/pkg/client/injection/kube/client"
	"knative.dev/serving/cmd/scraper/stats"
	"knative.dev/serving/pkg/apis/networking"
	"knative.dev/serving/pkg/apis/serving"
	"knative.dev/serving/pkg/autoscaler"
)

var (
	logger    *zap.SugaredLogger
	masterURL = flag.String("master", "", "The address of the Kubernetes API server. "+
		"Overrides any value in kubeconfig. Only required if out-of-cluster.")
	kubeconfig = flag.String("kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
)

const (
	reportingPeriod = 1 * time.Second
)

func buildServer(port string, logger *zap.SugaredLogger, reporter *stats.PrometheusStatsReporter) *http.Server {
	scraperMux := http.NewServeMux()
	scraperMux.Handle("/", reporter.Handler())
	return &http.Server{
		Addr:    ":" + port,
		Handler: scraperMux,
	}
}

type config struct {
	ScraperPort       string `split_words:"true" required:"true"`
	NodeName          string `split_words:"true" required:"true"`
	PodName           string `split_words:"true" required:"true"`
	PodIP             string `split_words:"true" required:"true"`
	SystemNamespace   string `split_words:"true" required:"true"`
	ConfigLoggingName string `split_words:"true" required:"true"`
}

func revisionPodKey(revisionUID, podName string) string {
	return fmt.Sprintf("%s--%s", revisionUID, podName)
}

type metricsScraper struct {
	node          string
	reporter      *stats.PrometheusStatsReporter
	statRecorders sync.Map
}

const (
	component = "scraper"
)

func main() {
	flag.Parse()

	// Set up a context that we can cancel to tell informers and other subprocesses to stop.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg, err := sharedmain.GetConfig(*masterURL, *kubeconfig)
	if err != nil {
		log.Fatal("Error building kubeconfig:", err)
	}

	log.Printf("Registering %d clients", len(injection.Default.GetClients()))
	log.Printf("Registering %d informer factories", len(injection.Default.GetInformerFactories()))
	log.Printf("Registering %d informers", len(injection.Default.GetInformers()))

	ctx, informers := injection.Default.SetupInformers(ctx, cfg)

	// Parse the environment.
	var env config
	if err := envconfig.Process("", &env); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Set up our logger.
	loggingConfig, err := sharedmain.GetLoggingConfig(ctx)
	if err != nil {
		log.Fatal("Error loading/parsing logging configuration: ", err)
	}

	// Setup the logger.
	logger, _ := pkglogging.NewLoggerFromConfig(loggingConfig, component)
	ctx = pkglogging.WithLogger(ctx, logger)
	defer flush(logger)

	// Run informers instead of starting them from the factory to prevent the sync hanging because of empty handler.
	if err := controller.StartInformers(ctx.Done(), informers...); err != nil {
		logger.Fatalw("Failed to start informers", zap.Error(err))
	}

	reporter, err := stats.NewPrometheusStatsReporter(env.NodeName, reportingPeriod)
	if err != nil {
		logger.Fatalw("Failed creating the reporter: %w", err)
	}

	s := &metricsScraper{
		node:          env.NodeName,
		statRecorders: sync.Map{},
		reporter:      reporter,
	}
	go s.run(ctx, logger)

	server := buildServer(env.ScraperPort, logger, s.reporter)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Errorf("starting server failed %v", err)
	}
}

func (s *metricsScraper) run(ctx context.Context, logger *zap.SugaredLogger) {
	ticker := time.NewTicker(reportingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			err := s.pollMetricsData(ctx, logger, s.node)
			if err != nil {
				logger.Errorf("Failed scraping %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func flush(logger *zap.SugaredLogger) {
	logger.Sync()
	os.Stdout.Sync()
	os.Stderr.Sync()
	metrics.FlushExporter()
}

func (s *metricsScraper) pollMetricsData(ctx context.Context, logger *zap.SugaredLogger, nodeName string) error {
	kubeClient := kubeclient.Get(ctx)
	podList, err := kubeClient.CoreV1().Pods("").List(metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
		LabelSelector: "serving.knative.dev/service",
	})
	if err != nil {
		return err
	}

	sClient, err := autoscaler.NewHTTPScrapeClient(cacheDisabledClient)
	if err != nil {
		return err
	}

	eg := errgroup.Group{}
	for _, p := range podList.Items {
		p := p
		eg.Go(func() error {
			podIP := p.Status.PodIP
			stat, err := sClient.Scrape(urlFromTarget(podIP))
			if err != nil {
				return err
			}

			podNamespace := p.ObjectMeta.Namespace
			podName := p.ObjectMeta.Name
			podConfig, podRevision := p.ObjectMeta.Labels[serving.ConfigurationLabelKey], p.ObjectMeta.Labels[serving.RevisionLabelKey]
			podRevisionUID := p.ObjectMeta.Labels[serving.RevisionUID]
			podRevisionKey := revisionPodKey(podRevisionUID, podName)

			logger.Infof("scraping data for pod: %s: %#v", podRevisionKey, stat)

			var recorder *stats.PrometheusStatsRecorder
			if val, ok := s.statRecorders.Load(podRevisionKey); !ok {
				recorder, err = stats.NewPrometheusStatsRecorder(podNamespace, podConfig, podRevision, podName, s.reporter)
				if err != nil {
					return err
				}
				s.statRecorders.Store(podRevisionKey, recorder)
			} else {
				recorder = val.(*stats.PrometheusStatsRecorder)
			}

			recorder.Report(
				stat.AverageConcurrentRequests,
				stat.AverageProxiedConcurrentRequests,
				stat.RequestCount,
				stat.ProxiedRequestCount,
			)
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	go s.unregisterMissing(podList.Items, logger)
	return nil
}

// cacheDisabledClient is a http client with cache disabled. It is shared by
// every goruntime for a revision scraper.
var cacheDisabledClient = &http.Client{
	Transport: &http.Transport{
		// Do not use the cached connection
		DisableKeepAlives: true,
	},
	Timeout: 3 * time.Second,
}

func (s *metricsScraper) unregisterMissing(podItems []corev1.Pod, logger *zap.SugaredLogger) {
	logger.Infof("pods on node %s: %d", s.node, len(podItems))
	podKeys := make(map[string]struct{}, len(podItems))
	for _, p := range podItems {
		podName := p.ObjectMeta.Name
		podRevisionUID := p.ObjectMeta.Labels[serving.RevisionUID]
		podKeys[revisionPodKey(podRevisionUID, podName)] = struct{}{}
	}

	toBeRemoved := make([]string, 0, len(podItems))
	s.statRecorders.Range(func(key, value interface{}) bool {
		podKey := key.(string)
		if _, ok := podKeys[podKey]; ok {
			logger.Infof("found-key: %s", podKey)
			delete(podKeys, podKey)
			return true
		}

		toBeRemoved = append(toBeRemoved, podKey)
		return true
	})

	for _, key := range toBeRemoved {
		logger.Infof("testing key: %s", key)
		value, ok := s.statRecorders.Load(key)
		if !ok {
			continue
		}

		logger.Infof("node[%s]: deleting missing pod %s", s.node, key)
		recorder := value.(*stats.PrometheusStatsRecorder)
		success := s.reporter.Unregister(recorder)
		logger.Info("success: %t", success)
		s.statRecorders.Delete(key)
	}
}

func urlFromTarget(podIP string) string {
	return fmt.Sprintf(
		"http://%s:%d/metrics",
		podIP, networking.AutoscalingQueueMetricsPort)
}

