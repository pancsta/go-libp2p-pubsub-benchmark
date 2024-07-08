package psmon

import (
	"context"
	"log"
	logStd "log"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TODO config
const (
	OtelEndpoint   = "localhost:4317"
	Timout         = 300 * time.Second
	tracerCooldown = 500 * time.Millisecond
)

type Disposable interface {
	End()
}

type NewOtelProviderFn func(ctx context.Context) (trace.Tracer, *sdktrace.TracerProvider, error)

// PSMon monitors libp2p-pubsub, including tests
type PSMon struct {
	AMTracers []*telemetry.OtelMachTracer

	// use an interface to avoid circular dependencies
	PSTracers    []Disposable
	OtelTracer   trace.Tracer
	OtelSpan     trace.Span
	TLogger      func(format string, args ...any)
	OtelProvider *sdktrace.TracerProvider
	TopSpans     []trace.Span
	Timeout      time.Duration
	LogFile      *os.File
	Log          *logStd.Logger

	// OpenTelemetry endpoint
	OtelEndpoint      string
	NewOtelProvider   NewOtelProviderFn
	AMLogLevel        am.LogLevel
	AMLogLevelVerbose am.LogLevel
}

func NewPSMon(endpoint string, newOtelProvider NewOtelProviderFn) *PSMon {

	// load bench.env, but not in sim
	isSim := strings.HasSuffix(os.Args[0], "sim") ||
		strings.HasSuffix(os.Args[0], "sim_go")
	if !isSim {
		_ = godotenv.Load("bench.env", ".env")
	}

	// load .env
	_ = godotenv.Load(".env")

	ep := endpoint
	if ep == "" {
		ep = OtelEndpoint
	}

	pm := &PSMon{
		Timeout:         Timout,
		OtelEndpoint:    ep,
		NewOtelProvider: newOtelProvider,
	}

	pm.AMLogLevel = pm.EnvLogLevel("PS_AM_LOG_LEVEL")
	pm.AMLogLevelVerbose = pm.EnvLogLevel("PS_AM_LOG_LEVEL_VERBOSE")

	return pm
}

// RunTestMain runs the main test function
func RunTestMain(m *testing.M, mon *PSMon) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var err error

	// new log file
	mon.LogFile, err = os.Create("psmon.log")
	mon.Log = logStd.New(mon.LogFile, "psmon: ", logStd.Ltime|logStd.Lmicroseconds)

	err, rootSpan := mon.startTestMain(ctx)

	// timeout
	disposed := false
	go func() {
		time.Sleep(mon.Timeout)
		mon.Log.Printf("TIMEOUT\n")
		println("TIMEOUT\n")
		if disposed {
			return
		}
		disposed = true
		mon.endTestMain(rootSpan, err, ctx)
		os.Exit(1)
	}()

	// execute tests
	m.Run()

	if disposed {
		return
	}
	disposed = true
	mon.endTestMain(rootSpan, err, ctx)
}

func (mon *PSMon) SetUpMach(mach *am.Machine, hostNum int) {

	lvl := mon.AMLogLevel
	if mon.IsAMDebug() {
		err := telemetry.TransitionsToDBG(mach, "")
		if err != nil {
			panic(err)
		}
	}

	// log common args
	mapper := am.NewArgsMapper([]string{"source", "topic"}, 0)
	mach.SetLogArgs(mapper)

	// TODO file log
	// if mon.IsAMLog() && mon.TLogger != nil {
	//	mach.SetTestLogger(mon.TLogger, lvl)
	// } else {
	mach.SetTestLogger(func(format string, args ...any) {
		// do nothing
	}, lvl)
	// }
}

// StartTest starts a new test case
func (mon *PSMon) StartTest(ctx context.Context) (
	otelCtx context.Context,
	testCtx context.Context,
	logTrace func(string) func(),
) {

	// return an empty function if not tracing
	if !mon.IsTracing() {
		return ctx, ctx, func(txt string) func() {
			return func() {
				mon.Log.Print(txt)
			}
		}
	}

	otelCtx = trace.ContextWithSpan(ctx, mon.OtelSpan)
	// TODO testcase name
	tCtx, tSpan := mon.OtelTracer.Start(otelCtx, "testcase")

	logTrace = func(txt string) func() {
		mon.Log.Print(txt)
		_, span := mon.OtelTracer.Start(tCtx, txt)
		return func() {
			span.End()
		}
	}

	// register for TestMain
	mon.TopSpans = append(mon.TopSpans, tSpan)

	return otelCtx, tCtx, logTrace
}

func (mon *PSMon) startTestMain(ctx context.Context) (error, trace.Span) {
	mon.Log.Print("startTestMain\n")
	mon.TLogger = mon.Log.Printf

	// return an empty span in not tracing
	if !mon.IsTracing() {
		var testSpan trace.Span
		return nil, testSpan
	}

	tracer, provider, err := mon.NewOtelProvider(ctx)
	mon.OtelTracer = tracer
	mon.OtelProvider = provider

	if err != nil {
		log.Fatal(err)
	}
	_, testSpan := tracer.Start(ctx, "psmon")
	mon.OtelSpan = testSpan

	return err, testSpan
}

func (mon *PSMon) endTestMain(rootSpan trace.Span, err error, ctx context.Context) {
	if mon.IsTracing() {

		for _, amTracer := range mon.AMTracers {
			amTracer.End()
		}

		for _, psTracer := range mon.PSTracers {
			psTracer.End()
		}

		for _, span := range mon.TopSpans {
			span.End()
		}

		rootSpan.End()
		time.Sleep(tracerCooldown)

		// flush tracing
		err = mon.OtelProvider.ForceFlush(ctx)
		if err != nil {
			mon.Log.Println("ERROR: ", err)
		}

		time.Sleep(tracerCooldown)

		// finish tracing
		if err := mon.OtelProvider.Shutdown(ctx); err != nil {
			mon.Log.Println("failed to shutdown provider: %v", err)
		}
	}
	mon.LogFile.Close()
}

// /// UTILS

func (mon *PSMon) IsAMDebug() bool {
	v := os.Getenv("PS_AM_DEBUG")
	return v != "" && v != "0"
}

func (mon *PSMon) IsTracing() bool {
	return mon.IsTracingHost() || mon.IsTracingAM()
}

func (mon *PSMon) IsTracingHost() bool {
	v := os.Getenv("PS_TRACING_HOST")
	return v != "" && v != "0"
}

func (mon *PSMon) IsTracingAM() bool {
	v := os.Getenv("PS_TRACING_AM")
	return v != "" && v != "0"
}

func (mon *PSMon) IsTracingAMTx() bool {
	v := os.Getenv("PS_TRACING_AM")
	return v == "2"
}

// TODO cookbook, move to pkg/x/helpers.go
func (mon *PSMon) EnvLogLevel(name string) am.LogLevel {
	v, _ := strconv.Atoi(os.Getenv(name))
	return am.LogLevel(v)
}
