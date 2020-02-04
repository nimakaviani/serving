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
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
	pkglogging "knative.dev/pkg/logging"
	"knative.dev/pkg/metrics"
)

var (
	logger *zap.SugaredLogger
)

func handler(w http.ResponseWriter, r *http.Request) {

}

func buildServer(env config, logger *zap.SugaredLogger) *http.Server {
	scraperMux := http.NewServeMux()
	scraperMux.HandleFunc("/", handler)
	return &http.Server{
		Addr:    ":" + strconv.Itoa(env.ScraperPort),
		Handler: scraperMux,
	}
}

type config struct {
	ScraperPort int `split_words:"true" required:"true"`

	// Logging configuration
	ServingLoggingConfig         string `split_words:"true" required:"true"`
	ServingLoggingLevel          string `split_words:"true" required:"true"`
	ServingRequestLogTemplate    string `split_words:"true"` // optional
	ServingEnableProbeRequestLog bool   `split_words:"true"` // optional
}

func main() {
	// Parse the environment.
	var env config
	if err := envconfig.Process("", &env); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Setup the logger.
	logger, _ = pkglogging.NewLogger(env.ServingLoggingConfig, env.ServingLoggingLevel)
	logger = logger.Named("queueproxy")
	defer flush(logger)

	server := buildServer(env, logger)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Errorf("starting server failed %v", err)
	}

}

func flush(logger *zap.SugaredLogger) {
	logger.Sync()
	os.Stdout.Sync()
	os.Stderr.Sync()
	metrics.FlushExporter()
}
