// Package observe contains only the framework-independent parts of the POC.
package observe

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"runtime"
	"strings"
)

type State struct {
	Error        error
	Panic        any
	Stack        string
	RawFunctions []string
	Status       int
	Validation   []Field
}

type Field struct {
	Namespace string
	Field     string
	Tag       string
	Message   string
}

type key struct{}

func Put(ctx context.Context, state *State) context.Context {
	return context.WithValue(ctx, key{}, state)
}

func Get(ctx context.Context) *State {
	state, _ := ctx.Value(key{}).(*State)
	return state
}

func (s *State) Record(err error) {
	if err != nil && s.Error == nil && !errors.Is(err, context.Canceled) {
		s.Error = err
	}
	s.Validation = Recognize(s.Error)
}

// Recover must be deferred directly. It preserves the original panic value.
func (s *State) Recover() {
	if value := recover(); value != nil {
		err, isError := value.(error)
		if value != http.ErrAbortHandler && !(isError && errors.Is(err, context.Canceled)) {
			s.Panic = value
			if isError {
				s.Record(err)
			} else {
				s.Record(fmt.Errorf("%v", value))
			}
			pcs := make([]uintptr, 64)
			pcs = pcs[:runtime.Callers(2, pcs)]
			frames := runtime.CallersFrames(pcs)
			var lines []string
			started := false
			for {
				frame, more := frames.Next()
				s.RawFunctions = append(s.RawFunctions, frame.Function)
				// Callers(2) skips runtime.Callers and this deferred method.
				if frame.Function != "runtime.gopanic" {
					started = true
				}
				if started {
					lines = append(lines, fmt.Sprintf("%s\n\t%s:%d", frame.Function, frame.File, frame.Line))
				}
				if !more {
					break
				}
			}
			s.Stack = strings.Join(lines, "\n")
		}
		panic(value)
	}
}

type fieldError interface {
	Namespace() string
	Field() string
	Tag() string
	Error() string
}

// A named slice's elements implement fieldError, not the slice itself.
func Recognize(err error) []Field {
	for err != nil {
		value := reflect.ValueOf(err)
		if value.Kind() == reflect.Slice && value.Len() > 0 {
			fields := make([]Field, 0, value.Len())
			for i := 0; i < value.Len(); i++ {
				field, ok := value.Index(i).Interface().(fieldError)
				if !ok {
					return nil
				}
				fields = append(fields, Field{field.Namespace(), field.Field(), field.Tag(), field.Error()})
			}
			return fields
		}
		err = errors.Unwrap(err)
	}
	return nil
}
