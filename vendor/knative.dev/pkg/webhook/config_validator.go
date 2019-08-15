package webhook

import (
	"context"
)

type cfgValidationKey struct{}

// +k8s:deepcopy-gen=false
func FromContext(ctx context.Context) *ConfigValidator {
	return ctx.Value(cfgValidationKey{}).(*ConfigValidator)
}

func ToContext(ctx context.Context, c *ConfigValidator) context.Context {
	return context.WithValue(ctx, cfgValidationKey{}, c)
}

type ConfigValidator struct {
	RegisteredValidations []*ConfigValidation
}
