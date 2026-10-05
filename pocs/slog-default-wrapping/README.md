# slog default wrapping POC

## Question

Does wrapping `slog.Default().Handler()`, calling `slog.SetDefault`, then restoring the saved `log.Writer()` and `log.Flags()` preserve output and prevent recursion? See design sections 9 and 17, POC 2.

**Conclusion: only when logging is quiescent. Activation can permanently deadlock.** Custom handlers can lose standard-log source locations; pre-bound attributes are opaque. There is no public atomic setter for the slog default and log writer/flags.

## Versions and running

Tested on darwin/arm64 with Go **1.25.14**, **1.26.8**, and installed **1.27.1**. All toolchains were available; no download failures. The first two are the latest patches returned by the [complete release list](https://go.dev/dl/?mode=json&include=all) on 2026-10-04. The isolated module declares `go 1.25` and has no dependencies.

```sh
cd /Users/simon/Repos/apitally/apitally-go/pocs/slog-default-wrapping
for version in go1.25.14 go1.26.8 go1.27.1; do
  GOTOOLCHAIN="$version" go test -race -count=1 -v ./...
  GOTOOLCHAIN="$version" go test -race -count=10 \
    -run '^TestConcurrencyWindow$|^TestNoWindowAlternatives$' -v ./...
done
```

Every scenario re-executes the race-enabled test binary in a fresh process. Assertions compare stderr and custom-writer bytes, replacing only timestamps. Ordinary children have an 8s timeout; deliberate deadlocks have a 2s timeout and a goroutine dump. All tests pass because they assert the documented failures as well as successes. `go*-test.log` and `go*-stress.log` retain verbose results.

## Results

Results were the same on all three versions.

| Case | Result and evidence |
| --- | --- |
| 1. Pristine default, no concurrent activation logging | **PASS.** `Info`, `InfoContext`, `Debug`, `Printf`, `Print`, changed prefix, short/long file, message-prefix, date/time/microseconds/UTC flags produce identical normalized bytes. Debug stays filtered unless `SetLogLoggerLevel(Debug)` was called before activation; Warn filtering also remains identical. Ordinary capture observes five slog records, four linked, zero standard-log records. |
| 2. Pristine handler with application writer/flags | **PASS, quiescent.** Custom buffer alone, custom flags alone, and both together remain byte-identical. |
| 3. Existing Text/JSON handler, `AddSource: true` | **PARTIAL FAIL.** Slog output/source and standard-log output without previously captured PCs match. If `Lshortfile` was set before the application's first `SetDefault`, wrapping removes source from `Printf`/`Print`. Both bridge records reach capture without request context, but with zero PC. |
| 4. Activation window | **FAIL.** Forced `log.Print`, forced `slog.Info`, and 64 mixed workers with a 10ms window permanently block restoration. Each case failed 10/10 times per version. The pause exposes a valid preemption schedule, not unmodified-window failure frequency. |
| 5. Pristine detection | **PASS for the tested private type check, not a public guarantee.** Package path/name plus pointer kind recognize original and derived handlers; Text, JSON, capture, nil, and a same-named local type are rejected. |
| 6. Idempotence | **PASS.** A second activation leaves logger/writer/flags unchanged. Explicitly installed capture handlers and derived capture handlers are not wrapped again; each record is captured once. |
| Capture fields and post-wrap bindings | **PASS.** Message, level, time, exact PC file/line/function, all slog value kinds, resolved LogValuer, nested groups, inline groups, and `With`/`WithGroup` bindings are obtained. Captured records reproduce the forwarded JSON byte-for-byte, including timestamps. |
| Bindings already inside the next handler | **FAIL for complete capture.** Pristine, Text and JSON handlers retain `setup=kept` and the `startup` group in output, but capture sees only `[key=value]`. Public handler APIs cannot read previously bound state. |

The sink records linked and unlinked observations for assertions; only linked observations would be exported. OTLP conversion itself is outside this POC.

### Why restoration cannot recover

`log.Print` holds the standard logger's output mutex during `Writer.Write`:

```text
log.Print -> handlerWriter.Write -> capture.Handle -> defaultHandler.Handle
          -> log.Logger.output -> same output mutex
activation -> log.SetOutput -> same output mutex
```

Restoration cannot acquire the mutex to remove the recursive writer. A concurrent slog call can enter the same cycle. The runtime reports `all goroutines are asleep - deadlock!`, or the parent timeout terminates the child. An SDK-only activation mutex cannot exclude application logging.

For custom handlers, the first `SetDefault` snapshots `capturePC` from log flags and clears those flags. The second call snapshots zero instead. Restoring the existing custom writer **does** preserve quiescent output/source, but bypasses capture for standard-log records. Those records have no request context anyway. The setters still have a transient, non-atomic output window.

## Validated public-API alternatives

- **Skip pristine automatic activation; explicitly wrap an independent logger.** Global output remains identical, no setters run, and context-linked records are captured. Stress completed 10/10 times per version, with 64 workers, 500 calls each and 200 activation attempts. Do not install a wrapper around the pristine handler with `SetDefault`; use `slog.New(wrapper)` without changing globals.
- **Replace pristine forwarding with an independent TextHandler.** The same stress passes and global slog capture works, but output changes to `time=... level=... msg=...`; pristine prefix/flags/source and `SetLogLoggerLevel` filtering semantics are not preserved. This is the v0 tradeoff, not an unchanged-output solution.

A private default-format reimplementation could avoid recursion, but needs its own header/source formatting and configuration tracking. Public `log.Logger.Output` accepts call depth, not a record PC. It still cannot make global setters atomic. This option was researched, not implemented, and is not recommended as a small compatibility fix.

## Proposed design changes

1. Replace section 9's writer-restoration guarantee and section 17's POC 2 assumption with the concurrency failure. Prefer explicit logger wrapping for pristine defaults; let applications intentionally choose Text/JSON output if they want global capture.
2. Specify custom-handler standard-log behavior. Preserving its existing writer preserves steady-state source/output but skips these unlinked records; do not promise byte-identical concurrent global transitions.
3. Limit automatic attribute capture to record attributes and bindings added after wrapping. Recommend installing `NewSlogHandler` before applying `With`/`WithGroup` when all bindings are required.
4. If pristine detection is needed to skip activation, use the tested full `log/slog` package path plus `defaultHandler` pointer type name, with version-matrix tests. There is no public predicate. Startup identity can misclassify a handler installed during package initialization; pointer identity misses derived handlers; behavioral probes cannot prove ownership.

No design document was edited.

Sources: tagged Go [SetDefault/handlerWriter](https://github.com/golang/go/blob/go1.27.1/src/log/slog/logger.go), [defaultHandler](https://github.com/golang/go/blob/go1.27.1/src/log/slog/handler.go), [log output mutex](https://github.com/golang/go/blob/go1.27.1/src/log/log.go), and [public slog API](https://pkg.go.dev/log/slog).
