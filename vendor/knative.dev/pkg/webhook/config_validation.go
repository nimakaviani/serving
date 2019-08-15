/*
Copyright 2018 The Knative Authors

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

package webhook

import (
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/configmap"
)

type ConfigValidation struct {
	name   string
	logger configmap.Logger

	constructors map[string]reflect.Value
}

func NewConfigValidation(
	name string,
	logger configmap.Logger,
	constructors configmap.Constructors,
) *ConfigValidation {

	store := &ConfigValidation{
		name:         name,
		logger:       logger,
		constructors: make(map[string]reflect.Value),
	}

	for configName, constructor := range constructors {
		store.registerConfig(configName, constructor)
	}

	return store
}

func (s *ConfigValidation) registerConfig(name string, constructor interface{}) {
	cType := reflect.TypeOf(constructor)

	if cType.Kind() != reflect.Func {
		panic("config constructor must be a function")
	}

	if cType.NumIn() != 1 || cType.In(0) != reflect.TypeOf(&corev1.ConfigMap{}) {
		panic("config constructor must be of the type func(*k8s.io/api/core/v1/ConfigMap) (..., error)")
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()

	if cType.NumOut() != 2 || !cType.Out(1).Implements(errorType) {
		panic("config constructor must be of the type func(*k8s.io/api/core/v1/ConfigMap) (..., error)")
	}

	s.constructors[name] = reflect.ValueOf(constructor)
}
