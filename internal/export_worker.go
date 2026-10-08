package internal

import (
	"context"
	"math/rand/v2"
	"strconv"
	"time"
)

const (
	initialExportDelay    = 2 * time.Second
	defaultExportInterval = 15 * time.Second
	// Files closed in earlier cycles are sent at most this many per cycle, which
	// spreads backlog delivery after an outage over several cycles.
	maxBacklogSendsPerCycle = 10
	// Shutdown hooks without a context flush within this deadline.
	shutdownHookFlushTimeout = 5 * time.Second
)

// Flush delivers the telemetry released so far, within a fixed deadline,
// and keeps Apitally running. Fiber's shutdown hooks call it, because they
// receive no context.
func Flush() {
	r := currentRuntime.Load()
	if r == nil || !r.isActive.Load() {
		return
	}
	defer recoverAndLogPanic("flush")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownHookFlushTimeout)
	defer cancel()
	select {
	case r.cycleLock <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-r.cycleLock }()
	// Shutdown can complete while Flush waits.
	if !r.isActive.Load() {
		return
	}
	r.flushToSpool(ctx)
	r.spool.closeCurrentFiles()
	r.sendPendingFiles(ctx, -1)
}

// runExportLoop runs export cycles independently of request traffic until
// ctx is canceled. Retry pacing comes only from the cycle schedule.
func (r *sdkRuntime) runExportLoop(ctx context.Context) {
	defer recoverAndLogPanic("export loop")
	defer close(r.exportLoopDone)
	timer := time.NewTimer(initialExportDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		r.runExportCycle(ctx)
		// Jitter keeps processes that started together from staying aligned.
		timer.Reset(time.Duration(float64(r.currentExportInterval()) * (0.9 + 0.2*rand.Float64())))
	}
}

func (r *sdkRuntime) runExportCycle(ctx context.Context) {
	defer recoverAndLogPanic("export cycle")
	r.cycleLock <- struct{}{}
	defer func() { <-r.cycleLock }()
	r.flushToSpool(ctx)
	budget := maxBacklogSendsPerCycle + r.spool.rotateForExport()
	r.spool.touchFiles()
	r.sendPendingFiles(ctx, budget)
}

// flushToSpool moves all buffered telemetry into the spool. Error events are
// log records, so they are emitted before the log flush.
func (r *sdkRuntime) flushToSpool(ctx context.Context) {
	r.emitErrorEvents()
	r.logs.flush(ctx)
	_ = r.batchProcessor.ForceFlush(ctx)
	r.metrics.collect()
}

// sendPendingFiles sends closed files oldest first, pausing between sends,
// until the budget is spent or a retryable failure occurs, so an outage costs
// one request per cycle. A negative budget sends everything without pauses.
func (r *sdkRuntime) sendPendingFiles(ctx context.Context, budget int) {
	sent := 0
	for _, f := range r.spool.pendingFiles() {
		if ctx.Err() != nil || (budget >= 0 && sent >= budget) {
			return
		}
		if budget >= 0 && sent > 0 && !sleep(ctx, time.Duration(100+rand.IntN(400))*time.Millisecond) {
			return
		}
		body, ok := r.spool.readForSend(f)
		if !ok {
			continue
		}
		sent++
		resp := r.client.post(ctx, f.signal, body)
		if resp.interval > 0 {
			r.exportInterval = resp.interval
		}
		switch resp.outcome {
		case exportAccepted:
			r.spool.delete(f)
		case exportRejected:
			warnOnce("export-rejected-"+strconv.Itoa(resp.status), "Apitally rejected buffered "+f.signal+" with HTTP status "+strconv.Itoa(resp.status)+" and they were dropped")
			r.spool.delete(f)
		default:
			return
		}
	}
}

func (r *sdkRuntime) emitErrorEvents() {
	for key, counts := range r.validationErrors.drain() {
		r.logs.emitEvent(validationErrorEventName, validationErrorEventBody(key, counts))
	}
	for key, counts := range r.serverErrors.drain() {
		r.logs.emitEvent(serverErrorEventName, serverErrorEventBody(key, counts))
	}
}

func (r *sdkRuntime) currentExportInterval() time.Duration {
	r.cycleLock <- struct{}{}
	defer func() { <-r.cycleLock }()
	return r.exportInterval
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
