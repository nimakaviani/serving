package webhook

import(
	"reflect"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"knative.dev/pkg/apis"
)

type ValidatableConfig struct {
	corev1.ConfigMap
}

func (s *ValidatableConfig) Validate(ctx context.Context) (errs *apis.FieldError) {
	v := FromContext(ctx)

	for _, v := range v.RegisteredValidations {
		if constructor, ok := v.constructors[s.Name]; ok {

			inputs := []reflect.Value{
				reflect.ValueOf(&s.ConfigMap),
			}

			outputs := constructor.Call(inputs)
			errVal := outputs[1]

			if !errVal.IsNil(){
				err := errVal.Interface().(error)
				fe := &apis.FieldError {
					Message: fmt.Sprintf("Err %s - %s", s.Name, err.Error()),
				}
				errs = errs.Also(fe)
			}
		}
	}
	return errs
}

func (s *ValidatableConfig) SetDefaults(ctx context.Context) { panic("shouldn't reach here") }

func (s *ValidatableConfig) GetObjectKind() schema.ObjectKind {
	return nil
}

func (s *ValidatableConfig) DeepCopyObject() runtime.Object {
	return &ValidatableConfig {
		ConfigMap: s.ConfigMap,
	}
}
