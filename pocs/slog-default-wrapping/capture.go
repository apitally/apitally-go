package wrapping

import (
	"context"
	"log"
	"log/slog"
	"reflect"
	"slices"
	"sync"
)

type requestKey struct{}

type observation struct {
	record slog.Record
	linked bool
}

type capture struct {
	mu      sync.Mutex
	records []observation
	entered func(slog.Record)
}

type captureHandler struct {
	next slog.Handler
	sink *capture
	with []withOperation
}

type withOperation struct {
	attrs []slog.Attr
	group string
}

func wrap(next slog.Handler, sink *capture) slog.Handler {
	if _, ok := next.(*captureHandler); ok {
		return next
	}
	return &captureHandler{next: next, sink: sink}
}

// restore is the proposal; preserve also restores an app-owned log bridge.
// skip leaves the pristine global alone and supports explicit wrapping only.
// text replaces pristine output with an independent TextHandler.
func activate(strategy string, sink *capture) {
	next := slog.Default().Handler()
	if _, ok := next.(*captureHandler); ok {
		return
	}
	pristine := isPristine(next)
	if strategy == "skip" && pristine {
		return
	}
	writer, flags := log.Writer(), log.Flags()
	if strategy == "text" && pristine {
		next = slog.NewTextHandler(writer, nil)
	}
	slog.SetDefault(slog.New(wrap(next, sink)))
	if (strategy == "restore" && pristine) || strategy == "preserve" {
		log.SetOutput(writer)
		log.SetFlags(flags)
	}
}

func isPristine(h slog.Handler) bool {
	t := reflect.TypeOf(h)
	return t != nil && t.Kind() == reflect.Pointer &&
		t.Elem().PkgPath() == "log/slog" && t.Elem().Name() == "defaultHandler"
}

func (h *captureHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *captureHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.sink.entered != nil {
		h.sink.entered(r)
	}
	// A new record owns the capture attributes; the forwarded record is untouched.
	copy := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	var attrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	attrs = resolveAttrs(attrs)
	for i := len(h.with) - 1; i >= 0; i-- {
		op := h.with[i]
		if op.group != "" {
			if len(attrs) > 0 {
				attrs = []slog.Attr{{Key: op.group, Value: slog.GroupValue(attrs...)}}
			}
		} else {
			attrs = append(slices.Clone(op.attrs), attrs...)
		}
	}
	copy.AddAttrs(attrs...)
	h.sink.mu.Lock()
	h.sink.records = append(h.sink.records, observation{copy, ctx.Value(requestKey{}) != nil})
	h.sink.mu.Unlock()
	return h.next.Handle(ctx, r)
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.with = append(slices.Clone(h.with), withOperation{attrs: resolveAttrs(attrs)})
	copy.next = h.next.WithAttrs(slices.Clone(attrs))
	return &copy
}

func (h *captureHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	copy := *h
	copy.with = append(slices.Clone(h.with), withOperation{group: name})
	copy.next = h.next.WithGroup(name)
	return &copy
}

func resolveAttrs(attrs []slog.Attr) []slog.Attr {
	var resolved []slog.Attr
	for _, a := range attrs {
		a.Value = a.Value.Resolve()
		if a.Value.Kind() == slog.KindGroup {
			children := resolveAttrs(a.Value.Group())
			if len(children) == 0 {
				continue
			}
			if a.Key == "" {
				resolved = append(resolved, children...)
				continue
			}
			a.Value = slog.GroupValue(children...)
		}
		if !a.Equal(slog.Attr{}) {
			resolved = append(resolved, a)
		}
	}
	return resolved
}
