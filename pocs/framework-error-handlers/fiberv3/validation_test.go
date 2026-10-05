package fiberv3

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
)

func TestBindValidationRecognitionAndStatus(t *testing.T) {
	// v3.5.0 bind.go:193-218,301-311 returns validation errors directly from JSON.
	// All uses returnErr at bind.go:565; auto-handling makes a new *fiber.Error without Unwrap.
	for _, test := range []struct {
		name         string
		bind         func(*fiber.Bind, any) error
		auto         bool
		wrap         bool
		mappedStatus int
		body         string
		status       int
		kind         string
	}{
		{name: "JSON manual", bind: (*fiber.Bind).JSON, status: 500, kind: "validation"},
		{name: "JSON auto", bind: (*fiber.Bind).JSON, auto: true, status: 500, kind: "validation"},
		{name: "Body auto", bind: (*fiber.Bind).Body, auto: true, status: 500, kind: "validation"},
		{name: "All manual", bind: (*fiber.Bind).All, status: 500, kind: "validation"},
		{name: "All auto", bind: (*fiber.Bind).All, auto: true, status: 400, kind: "fiber"},
		{name: "wrapped validation", bind: (*fiber.Bind).JSON, wrap: true, status: 500, kind: "wrapped"},
		{name: "custom 400", bind: (*fiber.Bind).JSON, mappedStatus: 400, status: 400, kind: "validation"},
		{name: "custom 422 wrapped", bind: (*fiber.Bind).JSON, wrap: true, mappedStatus: 422, status: 422, kind: "wrapped"},
		{name: "parse manual", bind: (*fiber.Bind).JSON, body: "{", status: 500, kind: "parse"},
		{name: "parse auto", bind: (*fiber.Bind).JSON, auto: true, body: "{", status: 400, kind: "fiber"},
		{name: "valid", bind: (*fiber.Bind).JSON, body: `{"email":"valid@example.com","address":{"city":"Sydney"}}`, status: 200, kind: "none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			validate := validator.New()
			validate.RegisterTagNameFunc(func(field reflect.StructField) string {
				return strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
			})
			scenario := scenario{validator: structValidator{validate}, handler: func(c fiber.Ctx) error {
				var input validationRequest
				bind := c.Bind()
				if test.auto {
					bind = bind.WithAutoHandling()
				}
				err := test.bind(bind, &input)
				if err == nil {
					return c.SendString("ok")
				}
				if test.wrap {
					return fmt.Errorf("bind validation: %w", err)
				}
				return err
			}}
			if test.mappedStatus != 0 {
				scenario.errorHandler = func(c fiber.Ctx, err error) error {
					var fields validator.ValidationErrors
					if errors.As(err, &fields) {
						return c.Status(test.mappedStatus).SendString("validation: " + err.Error())
					}
					return fiber.DefaultErrorHandler(c, err)
				}
			}
			body := test.body
			if body == "" {
				body = `{"email":"not-an-email","address":{"city":""}}`
			}
			request := func() *http.Request {
				request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				return request
			}
			scenario.request = request()
			baseline := exercise(t, scenario)
			scenario.observed, scenario.request = true, request()
			got := exercise(t, scenario)
			assertResponse(t, baseline, got, test.status, baseline.body)
			wantCalls := int32(1)
			if test.kind == "none" {
				wantCalls = 0
			}
			if baseline.calls != wantCalls || got.calls != wantCalls || got.state.Status != test.status || got.outerError != nil {
				t.Fatalf("calls=%d/%d observed status=%d outer=%v", baseline.calls, got.calls, got.state.Status, got.outerError)
			}
			err := got.state.Error
			if test.kind == "none" {
				if err != nil || got.body != "ok" || len(got.state.Validation) != 0 {
					t.Fatalf("valid input: state=%+v body=%q", got.state, got.body)
				}
				return
			}
			if err == nil || baseline.outerError == nil || err.Error() != baseline.outerError.Error() || got.state.Stack != "" {
				t.Fatalf("recorded=%v baseline=%v stack=%q", err, baseline.outerError, got.state.Stack)
			}
			switch test.kind {
			case "validation", "wrapped":
				var fields validator.ValidationErrors
				if !errors.As(err, &fields) || len(fields) != 2 {
					t.Fatalf("expected real validator error, got %T: %v", err, err)
				}
				if test.kind == "validation" {
					if _, ok := err.(validator.ValidationErrors); !ok || errors.Unwrap(err) != nil {
						t.Fatalf("JSON/All manual changed validation error: %T", err)
					}
				} else if errors.Unwrap(err) == nil {
					t.Fatal("expected a wrapped validation error")
				}
				want := []observe.Field{
					{Namespace: "validationRequest.email", Field: "email", Tag: "email", Message: fields[0].Error()},
					{Namespace: "validationRequest.address.city", Field: "city", Tag: "required", Message: fields[1].Error()},
				}
				if !reflect.DeepEqual(got.state.Validation, want) || !reflect.DeepEqual(observe.Recognize(err), want) {
					t.Fatalf("recognized=%+v want=%+v", got.state.Validation, want)
				}
				if strings.TrimPrefix(want[1].Namespace, "validationRequest.") != "address.city" || want[0].Message == "" || want[1].Message == "" {
					t.Fatalf("JSON-tag validation paths/messages: %+v", want)
				}
			case "fiber":
				fiberError, ok := err.(*fiber.Error)
				if !ok || fiberError.Code != 400 || errors.Unwrap(err) != nil || len(observe.Recognize(err)) != 0 || len(got.state.Validation) != 0 {
					t.Fatalf("auto error=%T: %v validation=%+v", err, err, got.state.Validation)
				}
			case "parse":
				var bindError *fiber.BindError
				var syntaxError *json.SyntaxError
				if !errors.As(err, &bindError) || bindError.Source != fiber.BindSourceBody || !errors.As(err, &syntaxError) || len(got.state.Validation) != 0 {
					t.Fatalf("parse error=%T: %v validation=%+v", err, err, got.state.Validation)
				}
			}
		})
	}
}

type validationRequest struct {
	Email   string            `json:"email" validate:"required,email"`
	Address validationAddress `json:"address"`
}

type validationAddress struct {
	City string `json:"city" validate:"required"`
}

type structValidator struct {
	validate *validator.Validate
}

func (v structValidator) Validate(value any) error {
	return v.validate.Struct(value)
}
