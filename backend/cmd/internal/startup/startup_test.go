// Checks shared option parsing, tracing setup, and stopping a process whose
// initialization failed after its probe listener started.

package startup

import (
	"context"
	"errors"
	"flag"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/twhAuto/autonomous_battlefield/backend/internal/httpapi"
)

func TestParseFlagsReadsConfigAndExtraFlags(t *testing.T) {
	path, err := ParseFlags("server", nil, nil)
	if err != nil || path != "config.yaml" {
		t.Fatalf("defaults = %q, %v", path, err)
	}
	var migrate bool
	path, err = ParseFlags("server", []string{"-migrate", "-config", "engine.yaml"}, func(flags *flag.FlagSet) {
		flags.BoolVar(&migrate, "migrate", false, "")
	})
	if err != nil || path != "engine.yaml" || !migrate {
		t.Fatalf("path=%q migrate=%v err=%v", path, migrate, err)
	}
}

func TestParseFlagsRejectsPositionalAndUnknownArguments(t *testing.T) {
	for _, arguments := range [][]string{{"run"}, {"-config", "a.yaml", "up"}, {"-unknown"}} {
		if _, err := ParseFlags("engine", arguments, nil); err == nil {
			t.Fatalf("ParseFlags(%v) accepted", arguments)
		}
	}
	if _, err := ParseFlags("engine", []string{"-h"}, nil); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h error = %v, want flag.ErrHelp", err)
	}
}

func TestTracingWithoutExporterStopsCleanly(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	stop, err := Tracing(context.Background(), "battlefield-test")
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

func TestTracingReportsInvalidSampler(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "2")
	if _, err := Tracing(context.Background(), "battlefield-test"); err == nil || !strings.Contains(err.Error(), "configure tracing") {
		t.Fatalf("error = %v", err)
	}
}

func TestAbortStart(t *testing.T) {
	initialization := errors.New("initialization failed")
	for _, test := range []struct {
		name         string
		signal       context.Context
		serveError   error
		wantNil      bool
		wantContains string
	}{
		{name: "initialization error", signal: context.Background(), wantContains: "initialize: initialization failed"},
		{name: "signal cancellation", signal: cancelledContext(), wantNil: true},
		{name: "listener error", signal: context.Background(), serveError: errors.New("address in use"), wantContains: "serve: address in use"},
	} {
		t.Run(test.name, func(t *testing.T) {
			serveErrors := make(chan error, 1)
			serveErrors <- test.serveError
			lifecycle := httpapi.NewLifecycleHandler(http.NotFoundHandler())
			err := AbortStart(&http.Server{}, lifecycle, serveErrors, test.signal, time.Second, initialization)
			if test.wantNil != (err == nil) || (err != nil && !strings.Contains(err.Error(), test.wantContains)) {
				t.Fatalf("error = %v, want nil=%v containing %q", err, test.wantNil, test.wantContains)
			}
		})
	}
}

func TestServeReportsListenErrorsAndGracefulShutdown(t *testing.T) {
	stopped := make(chan struct{})
	failing := Serve(&http.Server{Addr: "bad address"}, func() { close(stopped) })
	if err := <-failing; err == nil {
		t.Fatal("invalid address served")
	}
	<-stopped

	server := &http.Server{Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second}
	result := Serve(server, nil)
	time.Sleep(20 * time.Millisecond)
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("graceful shutdown reported %v", err)
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestCommandMainExitCodes(t *testing.T) {
	failure := errors.New("failed")
	load := func(path string) (string, error) {
		if path == "missing.yaml" {
			return "", failure
		}
		return path, nil
	}
	var ran string
	run := func(ctx context.Context, configuration string) error {
		if ctx == nil {
			t.Fatal("Run received no context")
		}
		ran = configuration
		if configuration == "broken.yaml" {
			return failure
		}
		return nil
	}
	command := Command[string]{Name: "test", Load: load, Run: run}
	for _, test := range []struct {
		arguments []string
		want      int
	}{
		{[]string{"-h"}, 0},
		{[]string{"positional"}, 1},
		{[]string{"-config", "missing.yaml"}, 1},
		{[]string{"-config", "broken.yaml"}, 1},
		{[]string{"-config", "good.yaml"}, 0},
	} {
		if got := command.Main(test.arguments); got != test.want {
			t.Fatalf("Main(%v) = %d, want %d", test.arguments, got, test.want)
		}
	}
	if ran != "good.yaml" {
		t.Fatalf("Run last received %q", ran)
	}
}

func TestCommandBeforeCanFinishOrFailWithoutConfiguration(t *testing.T) {
	loaded := false
	load := func(string) (string, error) { loaded = true; return "", nil }
	run := func(context.Context, string) error { return nil }
	finished := Command[string]{Name: "test", Before: func(context.Context) (bool, error) { return true, nil }, Load: load, Run: run}
	if code := finished.Main(nil); code != 0 || loaded {
		t.Fatalf("finished early: code=%d loaded=%v", code, loaded)
	}
	failed := Command[string]{Name: "test", Before: func(context.Context) (bool, error) { return false, errors.New("auth missing") }, Load: load, Run: run}
	if code := failed.Main(nil); code != 1 || loaded {
		t.Fatalf("failed early: code=%d loaded=%v", code, loaded)
	}
	continued := Command[string]{Name: "test", Before: func(context.Context) (bool, error) { return false, nil }, Load: load, Run: run}
	if code := continued.Main(nil); code != 0 || !loaded {
		t.Fatalf("continued: code=%d loaded=%v", code, loaded)
	}
}
