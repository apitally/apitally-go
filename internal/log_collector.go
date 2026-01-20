package internal

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
)

const (
	maxLogBufferSize = 1000
	maxLogMsgLength  = 2048
)

type logBufferKey struct{}

type LogRecord struct {
	Timestamp float64 `json:"timestamp"`
	Logger    string  `json:"logger"`
	Level     string  `json:"level"`
	Message   string  `json:"message"`
	File      string  `json:"file,omitempty"`
	Line      int     `json:"line,omitempty"`
}

type LogHandle struct {
	ctx  context.Context
	logs []LogRecord
	mu   sync.Mutex
}

func (h *LogHandle) Context() context.Context {
	return h.ctx
}

func (h *LogHandle) End() []LogRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.logs
}

func (h *LogHandle) append(record LogRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.logs) < maxLogBufferSize {
		h.logs = append(h.logs, record)
	}
}

type LogCollector struct {
	enabled bool
	next    slog.Handler
	mu      sync.RWMutex
}

func NewLogCollector(enabled bool) *LogCollector {
	lc := &LogCollector{
		enabled: enabled,
	}
	if enabled {
		lc.next = slog.Default().Handler()
		slog.SetDefault(slog.New(lc))
	}
	return lc
}

func (lc *LogCollector) StartCapture(ctx context.Context) *LogHandle {
	if !lc.enabled {
		return &LogHandle{ctx: ctx}
	}
	handle := &LogHandle{
		logs: make([]LogRecord, 0, 16),
	}
	handle.ctx = context.WithValue(ctx, logBufferKey{}, handle)
	return handle
}

// Enabled implements slog.Handler.
func (lc *LogCollector) Enabled(ctx context.Context, level slog.Level) bool {
	lc.mu.RLock()
	next := lc.next
	lc.mu.RUnlock()
	if next != nil {
		return next.Enabled(ctx, level)
	}
	return level >= slog.LevelInfo
}

// Handle implements slog.Handler.
func (lc *LogCollector) Handle(ctx context.Context, r slog.Record) error {
	if handle, ok := ctx.Value(logBufferKey{}).(*LogHandle); ok {
		record := LogRecord{
			Timestamp: float64(r.Time.UnixMilli()) / 1000.0,
			Level:     r.Level.String(),
			Message:   truncateLogMessage(r.Message),
		}
		if r.PC != 0 {
			frames := runtime.CallersFrames([]uintptr{r.PC})
			frame, _ := frames.Next()
			record.File = frame.File
			record.Line = frame.Line
			record.Logger = frame.Function
		}
		handle.append(record)
	}

	lc.mu.RLock()
	next := lc.next
	lc.mu.RUnlock()
	if next != nil {
		return next.Handle(ctx, r)
	}
	return nil
}

// WithAttrs implements slog.Handler.
func (lc *LogCollector) WithAttrs(attrs []slog.Attr) slog.Handler {
	lc.mu.RLock()
	next := lc.next
	lc.mu.RUnlock()

	newCollector := &LogCollector{
		enabled: lc.enabled,
	}
	if next != nil {
		newCollector.next = next.WithAttrs(attrs)
	}
	return newCollector
}

// WithGroup implements slog.Handler.
func (lc *LogCollector) WithGroup(name string) slog.Handler {
	lc.mu.RLock()
	next := lc.next
	lc.mu.RUnlock()

	newCollector := &LogCollector{
		enabled: lc.enabled,
	}
	if next != nil {
		newCollector.next = next.WithGroup(name)
	}
	return newCollector
}

func truncateLogMessage(msg string) string {
	if len(msg) > maxLogMsgLength {
		suffix := "... (truncated)"
		return msg[:maxLogMsgLength-len(suffix)] + suffix
	}
	return msg
}
