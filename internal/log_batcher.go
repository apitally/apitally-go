package internal

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	root "github.com/apitally/apitally-go"
)

// The log batcher uses the same settings as the span batch processor.
const (
	batchQueueSize = 2048
	batchMaxSize   = 512
	batchDelay     = time.Second
)

// logRecord is a captured application log record or an SDK event, which has
// an event name and body and no request linkage.
type logRecord struct {
	Record       root.LogRecord
	eventName    string
	eventBody    attribute.Value
	traceID      trace.TraceID
	spanID       trace.SpanID
	traceFlags   trace.TraceFlags
	serverSpanID trace.SpanID
	codeFunction string
	codeFile     string
	codeLine     int
}

func (r *logRecord) scopeName() string {
	if r.eventName != "" {
		return sdkScopeName
	}
	return "slog"
}

// logBatcher encodes log records on its own goroutine and appends them to
// the logs spool about once per second.
type logBatcher struct {
	spool         *spool
	resource      *resourcepb.Resource
	queue         chan *logRecord
	flushRequests chan chan struct{}
	stop          chan struct{}
	done          chan struct{}
}

func newLogBatcher(sp *spool, res *resourcepb.Resource) *logBatcher {
	return &logBatcher{
		spool:         sp,
		resource:      res,
		queue:         make(chan *logRecord, batchQueueSize),
		flushRequests: make(chan chan struct{}),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// add never blocks: records are dropped while the queue is full.
func (b *logBatcher) add(r *logRecord) {
	select {
	case b.queue <- r:
	default:
		warnOnce("log-queue-full", "Apitally log queue is full, some log records are dropped")
	}
}

func (b *logBatcher) emitEvent(name string, body attribute.Value) {
	b.add(&logRecord{Record: root.LogRecord{Time: time.Now()}, eventName: name, eventBody: body})
}

// flush appends all queued records to the spool.
func (b *logBatcher) flush(ctx context.Context) {
	done := make(chan struct{})
	select {
	case b.flushRequests <- done:
	case <-b.done:
		return
	case <-ctx.Done():
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// shutdown appends all queued records to the spool and stops the goroutine.
func (b *logBatcher) shutdown(ctx context.Context) {
	close(b.stop)
	select {
	case <-b.done:
	case <-ctx.Done():
	}
}

func (b *logBatcher) run() {
	defer close(b.done)
	timer := time.NewTimer(batchDelay)
	defer timer.Stop()
	var batch []*logRecord
	for {
		select {
		case r := <-b.queue:
			if batch = append(batch, r); len(batch) >= batchMaxSize {
				b.export(batch)
				batch = nil
			}
		case <-timer.C:
			b.export(batch)
			batch = nil
			timer.Reset(batchDelay)
		case done := <-b.flushRequests:
			b.export(b.drainQueue(batch))
			batch = nil
			close(done)
		case <-b.stop:
			b.export(b.drainQueue(batch))
			return
		}
	}
}

func (b *logBatcher) drainQueue(batch []*logRecord) []*logRecord {
	for {
		select {
		case r := <-b.queue:
			batch = append(batch, r)
		default:
			return batch
		}
	}
}

func (b *logBatcher) export(batch []*logRecord) {
	defer recoverAndLogPanic("log export")
	for len(batch) > 0 {
		n := min(len(batch), recordsPerEncodedChunk)
		b.spool.appendMessage(signalLogs, encodeLogs(b.resource, batch[:n]))
		batch = batch[n:]
	}
}
