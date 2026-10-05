package observe_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/apitally/apitally-go/pocs/framework-error-handlers/observe"
	"github.com/go-playground/validator/v10"
)

func TestValidationRecognition(t *testing.T) {
	type Address struct {
		City string `json:"city" validate:"required"`
	}
	type Input struct {
		Address Address `json:"address"`
	}
	for _, jsonNames := range []bool{false, true} {
		v := validator.New()
		if jsonNames {
			v.RegisterTagNameFunc(func(field reflect.StructField) string {
				return strings.Split(field.Tag.Get("json"), ",")[0]
			})
		}
		err := v.Struct(Input{})
		for index, candidate := range []error{err, fmt.Errorf("input: %w", err)} {
			fields := observe.Recognize(candidate)
			want := "Input.Address.City"
			if jsonNames {
				want = "Input.address.city"
			}
			if len(fields) != 1 || fields[0].Namespace != want || fields[0].Tag != "required" || fields[0].Message == "" {
				t.Fatalf("JSON names=%v: fields=%+v, want %s", jsonNames, fields, want)
			}
			t.Logf("JSON names=%v, wrapped=%v: Namespace=%s", jsonNames, index == 1, fields[0].Namespace)
		}
		var field interface {
			Namespace() string
			Field() string
			Tag() string
			Error() string
		}
		if errors.As(err, &field) {
			t.Fatal("the slice unexpectedly implements the element's interface")
		}
	}
	if observe.Recognize(errors.New("not validation")) != nil {
		t.Fatal("plain errors must not be recognized")
	}
}

func TestFirstErrorAndCancellation(t *testing.T) {
	state := &observe.State{}
	state.Record(fmt.Errorf("wrapped: %w", context.Canceled))
	if state.Error != nil {
		t.Fatal("cancellation was captured")
	}
	first := errors.New("first")
	state.Record(first)
	state.Record(errors.New("second"))
	if state.Error != first {
		t.Fatal("first error was replaced")
	}
}

func TestPanicStackAndIdentity(t *testing.T) {
	original := errors.New("boom")
	var stacks []string
	for i := 0; i < 2; i++ {
		state := &observe.State{}
		value := catchPanic(state, original)
		if value != original || state.Panic != original || state.Error != original {
			t.Fatal("panic identity changed")
		}
		if len(state.RawFunctions) < 2 || state.RawFunctions[0] != "runtime.gopanic" || !strings.HasSuffix(state.RawFunctions[1], ".panicLeaf") {
			t.Fatalf("unexpected leading frames: %v", state.RawFunctions)
		}
		if !strings.Contains(state.Stack, ".panicLeaf\n\t") || strings.Contains(state.Stack, "runtime.gopanic") || strings.Contains(state.Stack, "observe.(*State).Recover") || strings.Contains(state.Stack, "+0x") || strings.Contains(state.Stack, "goroutine ") {
			t.Fatalf("unexpected formatted stack: %s", state.Stack)
		}
		stacks = append(stacks, state.Stack)
		t.Logf("raw leading functions: %v\nstack:\n%s", state.RawFunctions[:2], state.Stack)
	}
	if stacks[0] != stacks[1] {
		t.Fatalf("same panic line produced different stacks:\n%s\nvs\n%s", stacks[0], stacks[1])
	}
	for _, original := range []error{http.ErrAbortHandler, fmt.Errorf("wrapped: %w", context.Canceled)} {
		state := &observe.State{}
		if value := catchPanic(state, original); value != original || state.Error != nil || state.Panic != nil || state.Stack != "" {
			t.Fatal("abort/cancellation must be re-panicked identically without capture")
		}
	}
}

func catchPanic(state *observe.State, value any) (caught any) {
	defer func() { caught = recover() }()
	func() {
		defer state.Recover()
		panicLeaf(value)
	}()
	return nil
}

func panicLeaf(value any) {
	panic(value)
}
