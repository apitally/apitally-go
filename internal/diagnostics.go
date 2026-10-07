package internal

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// warnedKeys deduplicates warnings for conditions that can recur indefinitely.
var warnedKeys sync.Map

// Diagnostics use context.Background, so the log capture handler never links
// them to a request and the application's own handlers still receive them.
func logDebug(msg string, args ...any) { logDiagnostic(slog.LevelDebug, msg, args) }
func logWarn(msg string, args ...any)  { logDiagnostic(slog.LevelWarn, msg, args) }
func logError(msg string, args ...any) { logDiagnostic(slog.LevelError, msg, args) }

func warnOnce(key, msg string, args ...any) {
	if _, loaded := warnedKeys.LoadOrStore(key, true); !loaded {
		logWarn(msg, args...)
	}
}

// recoverAndLogPanic must be deferred directly so that recover stops the panic.
func recoverAndLogPanic(operation string) {
	if p := recover(); p != nil {
		logPanic(operation, p)
	}
}

func logPanic(operation string, p any) {
	logError("Apitally "+operation+" panicked", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
}

func logDiagnostic(level slog.Level, msg string, args []any) {
	slog.Default().Log(context.Background(), level, msg, append(args, slog.String("logger", "apitally"))...)
}
