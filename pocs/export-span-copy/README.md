# Export span copy POC

## Question

Can a struct embed `sdktrace.ReadOnlySpan`, override `Attributes()`, `Resource()`
and `SpanKind()`, retain a private body stash, and pass unchanged through stock
`BatchSpanProcessor.OnEnd`, while user exporters receive only the original?

**Yes.** All cases pass with the race detector on darwin/arm64:

| OTel (API, trace, SDK) | Go toolchain | Evidence |
| --- | --- | --- |
| v1.46.0 | go1.27.1 | [test output](results-otel-1.46-go-1.27.txt) |
| v1.47.0 | go1.27.1 | [test output](results-otel-1.47-go-1.27.txt) |
| v1.46.0 | go1.25.14 | [test output](results-otel-1.46-go-1.25.txt) |

Go 1.25.14 was the latest Go 1.25 patch listed by `go.dev/dl/?mode=json&include=all`
at execution; its toolchain downloaded successfully. OTel v1.47.0 requires Go
1.26, so it is deliberately not tested on Go 1.25. The default module retains
Go 1.25 (`go 1.25.0`, normalized by `go mod tidy`); `otel147.mod` is a separate
version selection, not a second implementation. Both sum files pin dependencies.
Ten repeated `-race` runs per matrix combination also passed.

## Run

From this directory:

```sh
GOWORK=off go test -race -v -count=1 ./...
GOWORK=off go test -race -v -count=1 -modfile=otel147.mod ./...
GOTOOLCHAIN=go1.25.14 GOWORK=off go test -race -v -count=1 ./...
```

Use Go 1.27.1 for the first two commands to reproduce the tested matrix.
No root-module changes or `go get` switching are needed.

## Results

Every row passes in all three matrix runs.

| Case | Result and assertion evidence |
| --- | --- |
| Interface/private method | PASS: compile-time assertions accept both `*exportCopy` and `ReadWriteSpan` as `ReadOnlySpan`; the embedded interface supplies `private()`. |
| Apitally-owned provider | PASS: `TestExportCopy/apitally` registers a wrapping processor feeding a stock BSP. Exporter type assertion to `*exportCopy` succeeds and reads raw private body bytes. Query is replaced, captured header added, resource instance/environment replaced, all other resource keys and schema preserved. Root stays SERVER; locally parented duplicate becomes INTERNAL only on the copy. |
| User-owned provider | PASS: `TestExportCopy/user` constructs the user's simple processor/in-memory exporter first, then calls `RegisterSpanProcessor`. Both original spans reach the user; full `SpanStub` comparisons verify unchanged attributes, resource, kind and every other field. No captured headers/body enter original attributes. |
| Passthrough data | PASS: both ownership subtests compare every `SpanStub` field after normalizing only the three overrides. Covers name, span context including trace state, parent, start/end, status, events including exception, links, scope and deprecated library alias, dropped counts, child count. Fixture asserts nonzero span/event/link attribute drops, event/link drops, and one child. |
| Live request callback | PASS: `TestOnStartReadOnlySpan` passes the live `ReadWriteSpan` to a `ReadOnlySpan` callback synchronously. Start-option attributes and attributes set by the processor are visible before `Start` returns; end time is zero. |
| BSP sampled flag | PASS: `TestBatchSampledContext` exports sampled copies and drops valid unsampled copies. A separate context-override wrapper reverses the original flag in both directions, proving BSP consults the supplied copy's `SpanContext()`. |
| BSP env precedence | PASS: `TestBatchExplicitOptionsOverrideEnvironment` sets all four `OTEL_BSP_*` variables to conflicting values. SDK logging configuration reports explicit queue=8, batch=2, delay=100ms, timeout=3s. Actual exports verify batch size 2, a roughly 3s context budget rather than 1ms, and a timer-driven single-span export without flush rather than a 60s delay. |

The queue-size assertion uses BSP's exported `MarshalLog` representation; it
is not a throughput/drop stress test. Capture/redaction values are fixed test
fixtures: this POC tests copy transport and privacy, not masking algorithms.

## Caveats (source-checked in both SDK versions)

- `recordingSpan.End` creates a `*trace.snapshot` and shares it with processors.
  The embedded object is that ended snapshot, not the live recording span.
  Its getters are field reads safe for concurrent readers, provided consumers
  honor read-only ownership. The export-goroutine reads also pass `-race`.
- `snapshot.Attributes`, `Events` and `Links` expose slices; nested event/link
  attribute slices can also be shared. Build overrides in separately owned
  storage, never mutate the original's returned data, and freeze copy fields
  and body bytes before enqueueing. Resources are immutable; merge into a new
  resource. Export callbacks must not mutate shared original data either.
- Embedding retains the whole original snapshot, including replaced attributes
  and resource, plus the stash until buffering/export references are released.
  It does not retain the recording span/provider via a snapshot backpointer.
  BSP clears its batch after each export; exporters retaining the supplied
  batch slice must copy that outer slice. This test exporter retains individual
  copy pointers solely for assertions.
- BSP forwards the supplied interface value. Its only span concrete assertions
  recognize the internal `forceFlushSpan` sentinel; no SDK snapshot concrete
  type is required. Explicit options follow environment defaults, with no
  post-option size validation: use positive sizes with batch <= queue.

Relevant source functions are `trace/span.go: End, snapshot`, getters in
`trace/snapshot.go`, and `trace/batch_span_processor.go: NewBatchSpanProcessor,
exportSpans, processQueue, drainQueue, enqueueDrop, enqueueBlockOnQueueFull`.
The relevant BSP behavior is identical in v1.46.0 and v1.47.0.

## Conclusion and design proposals

The embedding design works at the SDK floor and latest OTel version. Keep it;
no alternate span interface or custom batch processor is needed.

Proposed updates to `docs/design.md` (not applied):

- Sections 6/17: mark POC 1 verified on the exact matrix above.
- Section 6: clarify that `SampleOnRequest` sees start-option attributes and
  preceding `OnStart` enrichment, not attributes written after `Start` returns.
- Sections 6/7: document copy/stash ownership and the read-only slice contract
  for callbacks; the ended snapshot is shared with the user's processors.
- Section 10: state explicitly that BSP drops unsampled contexts, including
  recorded-but-unsampled spans from user samplers. Preserve the original
  `SpanContext()`; attaching a processor does not expand sampler coverage.
