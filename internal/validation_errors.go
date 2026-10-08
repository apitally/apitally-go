package internal

import (
	"context"
	"reflect"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

const (
	validationErrorEventName = "apitally.request.validation_error"
	maxValidationField       = 2_048
	maxValidationMessage     = 2_048
	maxValidationType        = 128
)

// validationDetail is one normalized validation error of a request. Its
// fields form the validation error identity together with method and path.
type validationDetail struct {
	field    string
	message  string
	typeName string
}

type validationErrorKey struct {
	method string
	path   string
	validationDetail
}

// CaptureValidationError records the validation details of err for the
// request in ctx. It does nothing outside a monitored request.
func CaptureValidationError(ctx context.Context, err error) {
	defer recoverAndLogPanic("CaptureValidationError")
	if s := requestStateFromContext(ctx); s != nil {
		details := validationDetails(err)
		s.mu.Lock()
		s.validationDetails = append(s.validationDetails, details...)
		s.mu.Unlock()
	}
}

// fieldError is the method set of go-playground/validator's FieldError, so
// validation errors are recognized without importing the validator.
type fieldError interface {
	Namespace() string
	Field() string
	Tag() string
	Error() string
}

// validationDetails returns the details of go-playground/validator errors
// found by unwrapping err as errors.As does: a slice whose elements are field
// errors. Other errors have none.
func validationDetails(err error) []validationDetail {
	switch wrapper := err.(type) {
	case nil:
		return nil
	case interface{ Unwrap() error }:
		if details := fieldErrorDetails(err); details != nil {
			return details
		}
		return validationDetails(wrapper.Unwrap())
	case interface{ Unwrap() []error }:
		for _, inner := range wrapper.Unwrap() {
			if details := validationDetails(inner); details != nil {
				return details
			}
		}
		return nil
	}
	return fieldErrorDetails(err)
}

func fieldErrorDetails(err error) []validationDetail {
	value := reflect.ValueOf(err)
	if value.Kind() != reflect.Slice || value.Len() == 0 {
		return nil
	}
	details := make([]validationDetail, 0, value.Len())
	for i := range value.Len() {
		fe, ok := value.Index(i).Interface().(fieldError)
		if !ok {
			return nil
		}
		details = append(details, validationDetail{
			field:    truncateString(namespaceWithoutStruct(fe.Namespace()), maxValidationField),
			message:  truncateString(fe.Error(), maxValidationMessage),
			typeName: truncateString(fe.Tag(), maxValidationType),
		})
	}
	return details
}

// namespaceWithoutStruct removes the leading struct name from a field path
// such as "User.Address.City".
func namespaceWithoutStruct(namespace string) string {
	if _, field, ok := strings.Cut(namespace, "."); ok {
		return field
	}
	return namespace
}

func validationErrorEventBody(key validationErrorKey, counts map[string]uint64) attribute.Value {
	return attribute.MapValue(
		attribute.String("method", key.method),
		attribute.String("path", key.path),
		// The validator does not report which part of the request a field came from.
		attribute.String("source", ""),
		attribute.String("field", key.field),
		attribute.String("message", key.message),
		attribute.String("type", key.typeName),
		attribute.Slice("counts", errorCounts(counts)...),
	)
}
