package config

import (
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/webhook"
	"knative.dev/pkg/logging"
	pkgmetrics "knative.dev/pkg/metrics"
	"knative.dev/serving/pkg/autoscaler"
	deployment "knative.dev/serving/pkg/deployment"
	"knative.dev/serving/pkg/metrics"
	"knative.dev/serving/pkg/network"
	pkgtracing "knative.dev/serving/pkg/tracing/config"
)

func NewConfigValidation(logger configmap.Logger) *webhook.ConfigValidation {
	return webhook.NewConfigValidation(
			"revision",
			logger,
			configmap.Constructors{
				deployment.ConfigName:      deployment.NewConfigFromConfigMap,
				network.ConfigName:         network.NewConfigFromConfigMap,
				pkgmetrics.ConfigMapName(): metrics.NewObservabilityConfigFromConfigMap,
				autoscaler.ConfigName:      autoscaler.NewConfigFromConfigMap,
				logging.ConfigMapName():    logging.NewConfigFromConfigMap,
				pkgtracing.ConfigName:      pkgtracing.NewTracingConfigFromConfigMap,
			},
		)
}
