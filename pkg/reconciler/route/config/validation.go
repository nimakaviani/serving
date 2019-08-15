/*
Copyright 2019 The Knative Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package config

import (
	"time"

	"knative.dev/pkg/configmap"
	"knative.dev/pkg/webhook"
	"knative.dev/serving/pkg/gc"
	"knative.dev/serving/pkg/network"
)

func NewConfigValidation(logger configmap.Logger) *webhook.ConfigValidation {
	//TODO find value for minRevisionTimeout
	minRevisionTimeout := 10 * time.Second

	return webhook.NewConfigValidation(
		"route",
		logger,
		configmap.Constructors{
			DomainConfigName:   NewDomainFromConfigMap,
			gc.ConfigName:      gc.NewConfigFromConfigMapFunc(logger, minRevisionTimeout),
			network.ConfigName: network.NewConfigFromConfigMap,
		},
	)
}
