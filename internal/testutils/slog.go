package testutils

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

// SlogRecorder is a slog.Handler that records every record it handles.
type SlogRecorder struct {
	mu      sync.Mutex
	records []slog.Record
}

// RecordSlog installs a SlogRecorder as the slog default handler until the
// test ends.
func RecordSlog(t testing.TB) *SlogRecorder {
	previous := slog.Default()
	recorder := &SlogRecorder{}
	slog.SetDefault(slog.New(recorder))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return recorder
}

// Messages returns the messages of the recorded records at the given level.
func (r *SlogRecorder) Messages(level slog.Level) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var messages []string
	for _, record := range r.records {
		if record.Level == level {
			messages = append(messages, record.Message)
		}
	}
	return messages
}

func (r *SlogRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *SlogRecorder) Handle(_ context.Context, record slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, record.Clone())
	return nil
}

func (r *SlogRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *SlogRecorder) WithGroup(string) slog.Handler      { return r }
