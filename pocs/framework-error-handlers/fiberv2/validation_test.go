package fiberv2

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
)

func TestReturnedValidationErrors(t *testing.T) {
	validate := validator.New()
	validate.RegisterTagNameFunc(func(field reflect.StructField) string { return field.Tag.Get("json") })
	type input struct {
		Email string `json:"email" validate:"required"`
	}
	validationError := validate.Struct(input{})
	fields, ok := validationError.(validator.ValidationErrors)
	if !ok || len(fields) != 1 {
		t.Fatalf("validator returned %T: %v", validationError, validationError)
	}
	for _, status := range []int{400, 422, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			returned := fmt.Errorf("validate: %w", validationError)
			scenario := scenario{
				handler: func(*fiber.Ctx) error { return returned },
				errorHandler: func(c *fiber.Ctx, err error) error {
					var fields validator.ValidationErrors
					if errors.As(err, &fields) {
						return c.Status(status).SendString(err.Error())
					}
					return fiber.DefaultErrorHandler(c, err)
				},
			}
			baseline := exercise(t, scenario)
			scenario.observed = true
			got := exercise(t, scenario)
			assertResponse(t, baseline, got, status, returned.Error())
			want := []observe.Field{{Namespace: "input.email", Field: "email", Tag: "required", Message: fields[0].Error()}}
			if got.state.Error != returned || got.state.Status != status || !reflect.DeepEqual(got.state.Validation, want) || baseline.calls != 1 || got.calls != 1 {
				t.Fatalf("state=%+v calls=%d/%d", got.state, baseline.calls, got.calls)
			}
		})
	}
}
