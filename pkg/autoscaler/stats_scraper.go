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
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"knative.dev/pkg/logging"
	av1alpha1 "knative.dev/serving/pkg/apis/autoscaling/v1alpha1"
	"knative.dev/serving/pkg/apis/networking"
	"knative.dev/serving/pkg/apis/serving"
)

const (
	httpClientTimeout = 3 * time.Second

	// scraperPodName is the name used in all stats sent from the scraper to
	// the autoscaler. The actual customer pods are hidden behind the scraper. The
	// autoscaler does need to know how many customer pods are reporting metrics.
	// Instead, the autoscaler knows the stats it receives are either from the
	// scraper or the activator.
	scraperPodName = "service-scraper"

	// scraperMaxRetries are retries to be done to the actual Scrape routine. We want
	// to retry if a Scrape returns an error or if the Scrape goes to a pod we already
	// scraped.
	scraperMaxRetries = 10
)

var (
	// ErrFailedGetEndpoints specifies the error returned by scraper when it fails to
	// get endpoints.
	ErrFailedGetEndpoints = errors.New("failed to get endpoints")

	// ErrDidNotReceiveStat specifies the error returned by scraper when it does not receive
	// stat from an unscraped pod
	ErrDidNotReceiveStat = errors.New("did not receive stat from an unscraped pod")
)

// StatsScraper defines the interface for collecting Revision metrics
type StatsScraper interface {
	// Scrape scrapes the Revision queue metric endpoint. The duration is used
	// to cutoff young pods, whose stats might skew lower.
	Scrape(time.Duration) (Stat, error)
	BulkScrape(time.Duration, *av1alpha1.Metric) ([]Stat, error)
	Run(context.Context)
}

// scrapeClient defines the interface for collecting Revision metrics for a given
// URL. Internal used only.
type scrapeClient interface {
	// Scrape scrapes the given URL.
	Scrape(url string) (Stat, error)
	BulkScrape(url string) ([]Stat, error)
}

// cacheDisabledClient is a http client with cache disabled. It is shared by
// every goruntime for a revision scraper.
var cacheDisabledClient = &http.Client{
	Transport: &http.Transport{
		// Do not use the cached connection
		DisableKeepAlives: true,
	},
	Timeout: httpClientTimeout,
}

// ServiceScraper scrapes Revision metrics via a K8S service by sampling. Which
// pod to be picked up to serve the request is decided by K8S. Please see
// https://kubernetes.io/docs/concepts/services-networking/network-policies/
// for details.
type ServiceScraper struct {
	podsLister corev1listers.PodLister
	sClient    scrapeClient
	revMap     sync.Map
}

// NewServiceScraper creates a new StatsScraper for the Revision which
// the given Metric is responsible for.
func NewServiceScraper(podsLister corev1listers.PodLister) (*ServiceScraper, error) {
	sClient, err := NewHTTPScrapeClient(cacheDisabledClient)
	if err != nil {
		return nil, err
	}
	serviceScraper, err := newServiceScraperWithClient(podsLister, sClient)
	if err != nil {
		return nil, err
	}
	return serviceScraper, nil
}

func newServiceScraperWithClient(
	podsLister corev1listers.PodLister,
	sClient scrapeClient) (*ServiceScraper, error) {
	if sClient == nil {
		return nil, errors.New("scrape client must not be nil")
	}

	return &ServiceScraper{
		sClient:    sClient,
		podsLister: podsLister,
		revMap:     sync.Map{},
	}, nil
}

func urlFromTarget(t, ns string) string {
	return fmt.Sprintf(
		"http://%s.%s:%d/metrics",
		t, ns, networking.AutoscalingQueueMetricsPort)
}

// Scrape calls the destination service then sends it
// to the given stats channel.
func (s *ServiceScraper) BulkScrape(window time.Duration, metric *av1alpha1.Metric) ([]Stat, error) {
	revisionName := metric.ObjectMeta.Labels[serving.RevisionLabelKey]
	if v, ok := s.revMap.Load(revisionName); ok {
		// TODO nimak - there is probably some race here when deleting
		// revision stats
		defer s.revMap.Delete(revisionName)
		return v.([]Stat), nil
	}
	return nil, fmt.Errorf("no metrics for revision %s", revisionName)
}

func (s *ServiceScraper) Run(ctx context.Context) {
	logger := logging.FromContext(ctx)
	ticker := time.NewTicker(time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			logger.Infof(">> bulk scrape ...")
			pods, err := s.podsLister.Pods("knative-serving").List(labels.SelectorFromSet(labels.Set{
				"knative.dev/scraper": "devel",
			}))
			if err != nil {
				logger.Errorf("failed listing pods %v", err)
				continue
			}

			logger.Infof(">> len(pods): %d", len(pods))
			grp := errgroup.Group{}
			for _, p := range pods {
				p := p
				grp.Go(func() error {
					// TODO nimak: fix the port here
					url := fmt.Sprintf("http://%s:%s/", p.Status.PodIP, "8101")
					fmt.Printf(">> scrape-url: %s\n", url)
					stats, err := s.tryBulkScrape(url)
					if err != nil {
						fmt.Printf("error scraping %s: %v\n", url, err)
						return err
					}

					for _, stat := range stats {
						var stats []Stat
						if v, ok := s.revMap.Load(stat.RevisionName); ok {
							stats = v.([]Stat)
						}
						stats = append(stats, stat)
						s.revMap.Store(stat.RevisionName, stats)
					}

					return nil
				})
			}

			if err := grp.Wait(); err != nil {
				logger.Errorf("scraping failed: %w", err)
			}
		}
	}
}

// Scrape calls the destination service then sends it
// to the given stats channel.
func (s *ServiceScraper) Scrape(window time.Duration) (Stat, error) {
	return emptyStat, nil
}

func (s *ServiceScraper) tryBulkScrape(url string) ([]Stat, error) {
	return s.sClient.BulkScrape(url)
}
