package wrapping

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type result struct {
	CustomOutput string `json:"custom_output,omitempty"`
	Seen         int    `json:"seen"`
	Linked       int    `json:"linked"`
	LogSeen      int    `json:"log_seen"`
	LogPC        bool   `json:"log_pc"`
	ContextAttrs string `json:"context_attrs,omitempty"`
}

type childResult struct {
	result
	stderr   []byte
	stdout   []byte
	timedOut bool
}

func TestOutput(t *testing.T) {
	cases := []string{
		"pristine", "shortfile", "longfile-msgprefix", "all-flags",
		"level-debug", "level-warn", "custom-output", "custom-output-flags",
		"custom-flags", "derived-pristine", "text", "json", "text-prebound", "json-prebound",
	}
	for _, scenario := range cases {
		t.Run(scenario, func(t *testing.T) {
			before := child(t, scenario, "baseline", false)
			after := child(t, scenario, "restore", false)
			assertSameOutput(t, before, after)
			if after.Seen == 0 || after.Linked == 0 {
				t.Fatalf("wrapper did not capture: %+v", after.result)
			}
			output := append(normalize(after.stderr), normalize([]byte(after.CustomOutput))...)
			if bytes.Contains(output, []byte("debug-filtered-by-default")) != (scenario == "level-debug") {
				t.Fatal("Debug filtering was not preserved")
			}
			if strings.HasPrefix(scenario, "level-") {
				t.Logf("delegated filtering preserved: %s", normalize(after.stderr))
			}
			if strings.HasPrefix(scenario, "text") || strings.HasPrefix(scenario, "json") {
				if after.LogSeen != 2 {
					t.Fatalf("log bridge records not seen: %+v", after.result)
				}
			} else if after.LogSeen != 0 {
				t.Fatal("restored pristine log writer should bypass capture")
			}
			t.Logf("byte-identical normalized stderr (%d bytes), custom output (%d bytes); seen=%d linked=%d log=%d",
				len(normalize(after.stderr)), len(normalize([]byte(after.CustomOutput))), after.Seen, after.Linked, after.LogSeen)
		})
	}
}

func TestCustomHandlerSourceRegression(t *testing.T) {
	for _, scenario := range []string{"text-source", "json-source"} {
		t.Run(scenario, func(t *testing.T) {
			before := child(t, scenario, "baseline", false)
			after := child(t, scenario, "restore", false)
			if bytes.Equal(normalize(before.stderr), normalize(after.stderr)) {
				t.Fatal("expected log.Printf source to disappear on the second SetDefault")
			}
			if after.LogSeen != 2 || after.LogPC {
				t.Fatalf("expected bridge records with zero PC: %+v", after.result)
			}
			beforeLines, afterLines := bytes.Split(before.stderr, []byte{'\n'}), bytes.Split(after.stderr, []byte{'\n'})
			for i := range beforeLines {
				if !bytes.Contains(beforeLines[i], []byte("bridge-")) &&
					!bytes.Equal(normalize(beforeLines[i]), normalize(afterLines[i])) {
					t.Fatalf("slog source changed:\nbefore %s\nafter  %s", beforeLines[i], afterLines[i])
				}
			}
			if !bytes.Contains(before.stderr, []byte("wrapping_test.go")) {
				t.Fatal("baseline must contain real application source locations")
			}
			t.Logf("proposal FAIL: source disappears only for log calls; before:\n%safter:\n%s", normalize(before.stderr), normalize(after.stderr))
			preserved := child(t, scenario, "preserve", false)
			assertSameOutput(t, before, preserved)
			if preserved.LogSeen != 0 || preserved.Linked == 0 {
				t.Fatalf("preserved bridge should bypass capture but slog should not: %+v", preserved.result)
			}
			t.Log("restoring the existing custom log writer preserves exact source/output, but log records bypass capture")
		})
	}
}

func TestPreboundAttributeVisibility(t *testing.T) {
	for _, scenario := range []string{"derived-pristine", "text-prebound", "json-prebound"} {
		t.Run(scenario, func(t *testing.T) {
			before := child(t, scenario, "baseline", false)
			after := child(t, scenario, "restore", false)
			assertSameOutput(t, before, after)
			if !bytes.Contains(after.stderr, []byte("setup")) || !bytes.Contains(after.stderr, []byte("startup")) ||
				after.ContextAttrs != "[key=value]" {
				t.Fatalf("expected opaque pre-bound attributes: %+v, output %s", after.result, after.stderr)
			}
			t.Logf("all-attributes guarantee FAIL: output retains setup/startup but captured context attrs are %s", after.ContextAttrs)
		})
	}
}

func TestConcurrencyWindow(t *testing.T) {
	for _, scenario := range []string{"window-forced", "window-forced-slog", "window-stress"} {
		t.Run(scenario, func(t *testing.T) {
			got := child(t, scenario, "restore", true)
			if !bytes.Contains(got.stderr, []byte("log.(*Logger).SetOutput")) ||
				!bytes.Contains(got.stderr, []byte("captureHandler).Handle")) {
				t.Fatalf("expected deadlocked restoration and forwarding:\nstdout %s\nstderr %s", got.stdout, got.stderr)
			}
			t.Logf("proposal FAIL: runtime deadlock or timeout; log.SetOutput and forwarding remained blocked (%s)", bytes.TrimSpace(got.stdout))
		})
	}
}

func TestNoWindowAlternatives(t *testing.T) {
	before := child(t, "pristine", "baseline", false)
	skipped := child(t, "pristine", "skip", false)
	assertSameOutput(t, before, skipped)
	if skipped.Seen != 0 {
		t.Fatal("skip must leave global slog untouched")
	}
	text := child(t, "pristine", "text", false)
	if bytes.Equal(normalize(before.stderr), normalize(text.stderr)) {
		t.Fatal("TextHandler replacement must demonstrate its format tradeoff")
	}
	for _, strategy := range []string{"skip", "text"} {
		t.Run(strategy, func(t *testing.T) {
			child(t, "safe-stress", strategy, false)
			t.Log("64 concurrent goroutines, 500 calls each, 200 activation attempts: completed")
		})
	}
	t.Log("skip preserves output and requires explicit request-log capture; TextHandler is safe but changes format")
}

func TestCaptureMetadataAndIdempotence(t *testing.T) {
	child(t, "metadata", "restore", false)
	child(t, "idempotence", "restore", false)
	child(t, "detection", "restore", false)
}

// Each child starts with independent process-global slog/log state.
func TestScenarioProcess(t *testing.T) {
	scenario := os.Getenv("SLOG_POC_SCENARIO")
	if scenario == "" {
		return
	}
	strategy := os.Getenv("SLOG_POC_STRATEGY")
	var sink capture
	switch scenario {
	case "window-forced", "window-forced-slog", "window-stress":
		runWindow(scenario, &sink)
	case "safe-stress":
		runSafeStress(strategy, &sink)
	case "metadata":
		checkMetadata(t)
	case "idempotence":
		checkIdempotence(t)
	case "detection":
		checkDetection(t)
	default:
		custom := configure(scenario)
		if strategy != "baseline" {
			activate(strategy, &sink)
		}
		emitLogs()
		var output string
		if custom != nil {
			output = custom.String()
		}
		report(&sink, output)
		os.Exit(0)
	}
	if t.Failed() {
		os.Exit(1)
	}
	report(&sink, "")
	os.Exit(0)
}

func child(t *testing.T, scenario, strategy string, expectTimeout bool) childResult {
	t.Helper()
	deadline := 8 * time.Second
	if expectTimeout {
		deadline = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestScenarioProcess$", "-test.timeout=0")
	cmd.Env = append(os.Environ(), "SLOG_POC_SCENARIO="+scenario, "SLOG_POC_STRATEGY="+strategy,
		"GORACE=atexit_sleep_ms=0")
	var stderr, stdout bytes.Buffer
	cmd.Stderr, cmd.Stdout = &stderr, &stdout
	err := cmd.Run()
	got := childResult{stderr: stderr.Bytes(), stdout: stdout.Bytes(), timedOut: ctx.Err() != nil}
	if bytes.Contains(got.stderr, []byte("WARNING: DATA RACE")) {
		t.Fatalf("race in %s/%s: %s", scenario, strategy, got.stderr)
	}
	if expectTimeout {
		if !got.timedOut && !bytes.Contains(got.stderr, []byte("fatal error: all goroutines are asleep - deadlock!")) {
			t.Fatalf("expected deadlock, child exited: %v\nstdout %s\nstderr %s", err, got.stdout, got.stderr)
		}
		return got
	}
	if err != nil {
		t.Fatalf("%s/%s failed: %v\nstdout %s\nstderr %s", scenario, strategy, err, got.stdout, got.stderr)
	}
	if err := json.Unmarshal(got.stdout, &got.result); err != nil {
		t.Fatalf("invalid child report: %v: %s", err, got.stdout)
	}
	return got
}

func assertSameOutput(t *testing.T, before, after childResult) {
	t.Helper()
	if !bytes.Equal(normalize(before.stderr), normalize(after.stderr)) ||
		!bytes.Equal(normalize([]byte(before.CustomOutput)), normalize([]byte(after.CustomOutput))) {
		t.Fatalf("output differs:\nbefore stderr %s\nafter stderr %s\nbefore custom %q\nafter custom %q",
			before.stderr, after.stderr, before.CustomOutput, after.CustomOutput)
	}
}

var timestamps = regexp.MustCompile(`\d{4}/\d{2}/\d{2}|\d{2}:\d{2}:\d{2}(?:\.\d+)?|\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)

func normalize(b []byte) []byte {
	return timestamps.ReplaceAll(b, []byte("<TIME>"))
}

func configure(scenario string) *bytes.Buffer {
	var custom *bytes.Buffer
	switch scenario {
	case "shortfile", "custom-flags":
		log.SetFlags(log.Lshortfile)
	case "longfile-msgprefix":
		log.SetFlags(log.Llongfile | log.Lmsgprefix)
	case "all-flags":
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC | log.Lshortfile | log.Lmsgprefix)
	case "level-debug":
		log.SetFlags(0)
		slog.SetLogLoggerLevel(slog.LevelDebug)
	case "level-warn":
		log.SetFlags(0)
		slog.SetLogLoggerLevel(slog.LevelWarn)
	case "custom-output", "custom-output-flags":
		custom = new(bytes.Buffer)
		log.SetOutput(custom)
		if scenario == "custom-output-flags" {
			log.SetFlags(log.Lshortfile | log.Lmsgprefix)
		}
	case "derived-pristine":
		log.SetFlags(0)
		slog.SetDefault(slog.Default().With("setup", "kept").WithGroup("startup"))
	case "text", "json", "text-source", "json-source", "text-prebound", "json-prebound":
		log.SetFlags(0)
		if strings.HasSuffix(scenario, "-source") {
			// The first SetDefault saves capturePC=true, then resets log.Flags to zero.
			log.SetFlags(log.Lshortfile)
		}
		options := &slog.HandlerOptions{AddSource: true}
		var h slog.Handler = slog.NewTextHandler(os.Stderr, options)
		if strings.HasPrefix(scenario, "json") {
			h = slog.NewJSONHandler(os.Stderr, options)
		}
		slog.SetDefault(slog.New(h))
		if strings.HasSuffix(scenario, "-prebound") {
			slog.SetDefault(slog.Default().With("setup", "kept").WithGroup("startup"))
		}
	}
	log.SetPrefix("initial: ")
	return custom
}

func emitLogs() {
	ctx := context.WithValue(context.Background(), requestKey{}, true)
	slog.Info("info", "key", "value")
	slog.InfoContext(ctx, "context-info", "key", "value")
	slog.Debug("debug-filtered-by-default")
	slog.WarnContext(ctx, "context-warning")
	slog.ErrorContext(ctx, "context-error")
	slog.Default().With("bound", "kept").WithGroup("group").InfoContext(ctx, "grouped", "n", 7)
	log.Printf("bridge-printf %d", 42)
	log.SetPrefix("changed: ")
	log.Print("bridge-print")
}

func report(sink *capture, output string) {
	r := result{CustomOutput: output}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, observed := range sink.records {
		r.Seen++
		if observed.linked {
			r.Linked++
		}
		if observed.record.Time.IsZero() || observed.record.Message == "" {
			panic("record is missing time or message")
		}
		if observed.record.Message == "context-info" {
			var attrs []slog.Attr
			observed.record.Attrs(func(a slog.Attr) bool { attrs = append(attrs, a); return true })
			r.ContextAttrs = fmt.Sprint(attrs)
		}
		if strings.Contains(observed.record.Message, "bridge-") {
			r.LogSeen++
			r.LogPC = r.LogPC || observed.record.PC != 0
			if observed.linked {
				panic("log bridge unexpectedly has a request context")
			}
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		panic(err)
	}
}

type silentWriter struct{}

func (silentWriter) Write(p []byte) (int, error) { return len(p), nil }

func runWindow(scenario string, sink *capture) {
	log.SetFlags(0)
	log.SetOutput(silentWriter{}) // Not io.Discard, which log optimizes away.
	writer, flags := log.Writer(), log.Flags()
	// Print a goroutine dump before the parent kills this deliberately stuck child.
	time.AfterFunc(300*time.Millisecond, func() {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		os.Stderr.Write(buf[:n])
	})
	if scenario == "window-forced" || scenario == "window-forced-slog" {
		entered := make(chan struct{})
		sink.entered = func(r slog.Record) {
			if scenario == "window-forced" || r.Message == "INFO window slog" {
				close(entered)
			}
		}
		slog.SetDefault(slog.New(wrap(slog.Default().Handler(), sink)))
		go func() {
			if scenario == "window-forced-slog" {
				slog.Info("window slog")
			} else {
				log.Print("window log")
			}
		}()
		<-entered // handlerWriter.Write is running under log's output mutex.
		fmt.Fprintln(os.Stdout, scenario+": entered bridge; restoring writer")
		log.SetOutput(writer) // Cannot acquire the mutex held by log.Print.
		log.SetFlags(flags)
		return
	}

	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 64; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			for j := 0; j < 500; j++ {
				if i%2 == 0 {
					log.Print("stress log")
				} else {
					slog.Info("stress slog")
				}
			}
		}(i)
	}
	close(start)
	slog.SetDefault(slog.New(wrap(slog.Default().Handler(), sink)))
	// Enlarge the scheduling window, which can also be widened by preemption.
	time.Sleep(10 * time.Millisecond)
	fmt.Fprintln(os.Stdout, "64 mixed log/slog workers; restoring writer after 10ms window")
	log.SetOutput(writer)
	log.SetFlags(flags)
	workers.Wait()
}

func runSafeStress(strategy string, sink *capture) {
	log.SetFlags(0)
	log.SetOutput(silentWriter{})
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 64; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			for j := 0; j < 500; j++ {
				if i%2 == 0 {
					log.Print("safe log")
				} else {
					slog.Info("safe slog")
				}
			}
		}(i)
	}
	close(start)
	for i := 0; i < 200; i++ {
		activate(strategy, sink)
		runtime.Gosched()
	}
	workers.Wait()
	if strategy == "skip" {
		// Explicit wrapping captures a context-linked record without touching globals.
		ctx := context.WithValue(context.Background(), requestKey{}, true)
		slog.New(wrap(slog.Default().Handler(), sink)).InfoContext(ctx, "explicit")
		if len(sink.records) != 1 || !sink.records[0].linked {
			panic("explicit pristine wrapping did not capture the linked record")
		}
	}
}

func checkIdempotence(t *testing.T) {
	log.SetOutput(silentWriter{})
	var sink capture
	activate("restore", &sink)
	first, writer, flags := slog.Default(), log.Writer(), log.Flags()
	activate("restore", &sink)
	if slog.Default() != first || log.Writer() != writer || log.Flags() != flags {
		t.Fatal("second activation changed globals")
	}
	h := first.With("bound", 1).WithGroup("group").Handler()
	if wrap(h, &capture{}) != h {
		t.Fatal("already wrapped derived handler was wrapped again")
	}
	ctx := context.WithValue(context.Background(), requestKey{}, true)
	slog.New(h).InfoContext(ctx, "once")
	if len(sink.records) != 1 {
		t.Fatalf("expected exactly one capture, got %d", len(sink.records))
	}
	var explicitSink capture
	already := slog.New(wrap(slog.NewTextHandler(silentWriter{}, nil), &explicitSink)).WithGroup("app")
	slog.SetDefault(already)
	activate("restore", &capture{})
	if slog.Default() != already {
		t.Fatal("activation replaced an explicitly installed Apitally handler")
	}
	already.InfoContext(ctx, "explicit once")
	if len(explicitSink.records) != 1 {
		t.Fatal("explicit handler captured more than once")
	}
}

// The same name outside log/slog must not be mistaken for the pristine handler.
type defaultHandler struct{ slog.Handler }

func checkDetection(t *testing.T) {
	original := slog.Default().Handler()
	for _, h := range []slog.Handler{original, original.WithAttrs([]slog.Attr{slog.Int("n", 1)}), original.WithGroup("g")} {
		if !isPristine(h) {
			t.Fatalf("missed standard handler %T", h)
		}
	}
	for _, h := range []slog.Handler{nil, slog.NewTextHandler(io.Discard, nil), slog.NewJSONHandler(io.Discard, nil),
		wrap(original, &capture{}), &defaultHandler{original}} {
		if isPristine(h) {
			t.Fatalf("false positive %T", h)
		}
	}
	appHandler := slog.NewTextHandler(io.Discard, nil)
	slog.SetDefault(slog.New(appHandler))
	startupType := reflect.TypeOf(slog.Default().Handler())
	if startupType != reflect.TypeOf(appHandler) || isPristine(appHandler) {
		t.Fatal("startup type identity demonstration failed")
	}
	t.Log("startup type identity would misclassify app-installed TextHandler; package/name check does not")
}

type resolvedValue string

func (v resolvedValue) LogValue() slog.Value { return slog.StringValue(string(v)) }

func checkMetadata(t *testing.T) {
	log.SetFlags(0)
	var forwarded bytes.Buffer
	var sink capture
	next := slog.NewJSONHandler(&forwarded, &slog.HandlerOptions{Level: slog.LevelDebug})
	h := wrap(next, &sink)
	ctx := context.WithValue(context.Background(), requestKey{}, true)
	fixed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	logger := slog.New(h).With("bound", "root").WithGroup("request").With("id", 7).
		WithGroup("inner").With("bound-inner", resolvedValue("resolved"))
	values := []slog.Attr{
		slog.String("s", "text"), slog.Int64("i", -3), slog.Uint64("u", 4),
		slog.Float64("f", 1.5), slog.Bool("b", true), slog.Duration("d", time.Second),
		slog.Time("t", fixed), slog.Any("any", []int{1, 2}),
		slog.Group("nested", slog.String("child", "value")),
		slog.Group("", slog.Int("inline", 8)), slog.Group("empty"), slog.Attr{},
	}
	_, file, line, _ := runtime.Caller(0)
	logger.LogAttrs(ctx, slog.LevelWarn, "metadata", values...)
	if len(sink.records) != 1 || !sink.records[0].linked {
		t.Fatal("missing context-linked record")
	}
	r := sink.records[0].record
	frame, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()
	if r.Message != "metadata" || r.Level != slog.LevelWarn || r.Time.IsZero() ||
		frame.File != file || frame.Line != line+1 || !strings.HasSuffix(frame.Function, ".checkMetadata") {
		t.Fatalf("missing record fields: %+v source=%+v", r, frame)
	}
	var attrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool { attrs = append(attrs, a); return true })
	if len(attrs) != 2 || attrs[0].Key != "bound" || attrs[0].Value.String() != "root" || attrs[1].Key != "request" {
		t.Fatalf("missing root or request group: %v", attrs)
	}
	request := attrs[1].Value.Group()
	if len(request) != 2 || request[0].Key != "id" || request[0].Value.Int64() != 7 || request[1].Key != "inner" {
		t.Fatalf("incorrect request scope: %v", request)
	}
	inner := request[1].Value.Group()
	keys := []string{"bound-inner", "s", "i", "u", "f", "b", "d", "t", "any", "nested", "inline"}
	kinds := []slog.Kind{slog.KindString, slog.KindString, slog.KindInt64, slog.KindUint64, slog.KindFloat64,
		slog.KindBool, slog.KindDuration, slog.KindTime, slog.KindAny, slog.KindGroup, slog.KindInt64}
	if len(inner) != len(keys) {
		t.Fatalf("incorrect nested attributes: %v", inner)
	}
	for i, a := range inner {
		if a.Key != keys[i] || a.Value.Kind() != kinds[i] {
			t.Fatalf("attribute %d: %v (%s), want %s (%s)", i, a, a.Value.Kind(), keys[i], kinds[i])
		}
	}
	if inner[0].Value.String() != "resolved" || inner[2].Value.Int64() != -3 || inner[3].Value.Uint64() != 4 ||
		inner[4].Value.Float64() != 1.5 || !inner[5].Value.Bool() || inner[6].Value.Duration() != time.Second ||
		!inner[7].Value.Time().Equal(fixed) || !reflect.DeepEqual(inner[8].Value.Any(), []int{1, 2}) ||
		inner[9].Value.Group()[0].Value.String() != "value" || inner[10].Value.Int64() != 8 {
		t.Fatal("attribute values were not preserved")
	}
	// Compare plain vs wrapped output for the exact same record, including timestamp and PC.
	var plain bytes.Buffer
	plainHandler := slog.NewJSONHandler(&plain, &slog.HandlerOptions{Level: slog.LevelDebug})
	if err := plainHandler.Handle(ctx, r); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain.Bytes(), forwarded.Bytes()) {
		t.Fatalf("capture did not match forwarded scopes:\nplain %s\nwrapped %s", plain.Bytes(), forwarded.Bytes())
	}
	if !h.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("Enabled did not delegate")
	}
	if filepath.Base(frame.File) != "wrapping_test.go" {
		t.Fatal("PC was not the application call site")
	}
	forwarded.Reset()
	plain.Reset()
	// An explicit attribute group and a WithGroup of the same name are distinct.
	slog.New(h).With(slog.Group("g", slog.Int("before", 1))).WithGroup("g").WithGroup("").
		InfoContext(ctx, "duplicate group", "after", 2)
	if err := plainHandler.Handle(ctx, sink.records[1].record); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain.Bytes(), forwarded.Bytes()) {
		t.Fatalf("explicit group was merged with WithGroup:\nplain %s\nwrapped %s", plain.Bytes(), forwarded.Bytes())
	}
}
