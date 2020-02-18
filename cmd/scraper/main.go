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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
	pkglogging "knative.dev/pkg/logging"
	"knative.dev/pkg/metrics"

	"sync"

	kubeclient "knative.dev/pkg/client/injection/kube/client"
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

func handler(w http.ResponseWriter, r *http.Request) {
	// revisionUID := r.URL.Query()["revisionUID"]

}

func buildServer(port string, logger *zap.SugaredLogger, scraper *metricsScraper) *http.Server {
	scraperMux := http.NewServeMux()
	scraperMux.HandleFunc("/", handler)
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

type revisionPod struct {
	revisionUID string
	podIP       string
	podName     string
}

type metricsScraper struct {
	node  string
	stats *sync.Map
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

	s := &metricsScraper{node: env.NodeName}
	go s.run(ctx, logger)

	server := buildServer(env.ScraperPort, logger, s)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Errorf("starting server failed %v", err)
	}
}

func (s *metricsScraper) run(ctx context.Context, logger *zap.SugaredLogger) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			stat, err := pollMetricsData(ctx, logger, s.node)
			if err != nil {
				logger.Errorf("Failed scraping %v", err)
			}
			s.stats = stat
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

func pollMetricsData(ctx context.Context, logger *zap.SugaredLogger, nodeName string) (*sync.Map, error) {
	kubeClient := kubeclient.Get(ctx)
	podList, err := kubeClient.CoreV1().Pods("").List(metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return nil, err
	}

	sClient, err := autoscaler.NewHTTPScrapeClient(cacheDisabledClient)
	if err != nil {
		return nil, err
	}

	podStat := sync.Map{}
	eg := errgroup.Group{}
	for _, p := range podList.Items {
		eg.Go(func() error {
			p := p
			podName, podIP := p.ObjectMeta.Name, p.Status.PodIP
			logger.Infof("scraping data for pod: %s (%s)", podName, podIP)
			stat, err := sClient.Scrape(urlFromTarget(podIP))
			if err != nil {
				return err
			}

			key := revisionPod{
				podName:     podName,
				podIP:       podIP,
				revisionUID: p.ObjectMeta.Labels[serving.RevisionUID],
			}
			podStat.Store(key, stat)
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return &podStat, nil
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

func urlFromTarget(podIP string) string {
	return fmt.Sprintf(
		"http://%s:%d/metrics",
		podIP, networking.AutoscalingQueueMetricsPort)
}
