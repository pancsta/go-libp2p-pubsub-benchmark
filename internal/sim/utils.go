package sim

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	ss "github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim/states/sim"
	"github.com/pancsta/go-libp2p-pubsub-benchmark/pkg/psmon"

	"github.com/brianvoe/gofakeit/v7"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	"go.opentelemetry.io/otel/trace"
	"math/rand"
	"regexp"
	"strings"
)

type Freq struct {
	// Relative delay (higher is less frequent)
	D int
	// up drift
	Up float64
	// down drift
	Down float64
	// Unit is the delay unit
	Unit time.Duration
}

func getStateFreq(state string) (Freq, error) {
	var freq Freq
	switch state {

	case ss.AddPeer:
		freq = addPeerFreq
	case ss.RemovePeer:
		freq = removePeerFreq
	case ss.AddTopic:
		freq = addTopicFreq
	case ss.RemoveTopic:
		freq = removeTopicFreq
	case ss.PeakRandTopic:
		freq = peakRandTopicFreq
	case ss.AddRandomFriend:
		freq = addRandomFriendFreq
	case ss.GC:
		freq = gcFreq
	case ss.JoinRandomTopic:
		freq = joinRandomTopicFreq
	case ss.JoinFriendsTopic:
		freq = joinFriendsTopicFreq
	case ss.MsgRandomTopic:
		freq = msgRandomTopicFreq
	default:
		return defaultFreq, fmt.Errorf("no freq for state %s", state)
	}

	ret := defaultFreq
	if freq.D != 0 {
		ret.D = freq.D
	}
	if freq.Unit != 0 {
		ret.Unit = freq.Unit
	}
	if freq.Up != 0 {
		ret.Up = freq.Up
	}
	if freq.Down != 0 {
		ret.Down = freq.Down
	}

	return ret, nil
}

func NewOtelProvider(ctx context.Context) (trace.Tracer, *sdktrace.TracerProvider, error) {
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		))

	exporter, err := otlptrace.New(ctx,
		otlptracegrpc.NewClient(
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(psmon.OtelEndpoint),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	serviceName := "ps"
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithMaxExportBatchSize(50),
			sdktrace.WithBatchTimeout(100*time.Millisecond),
		),
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(serviceName),
		)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Create a named tracer with package path as its name.
	return otel.Tracer("github.com/libp2p/go-libp2p-pubsub"), tp, nil
}

func logNoop(format string, args ...any) {
	// do nothing
}

func debugMach(mach *am.Machine) {
	err := telemetry.TransitionsToDBG(mach, "")
	if err != nil {
		panic(err)
	}
	// dont log to stdout, use the dbg UI instead
	mach.SetTestLogger(logNoop, EnvLogLevel("SIM_AM_LOG_LEVEL"))
}

// TODO cookbook, move to pkg/x/helpers.go
func EnvLogLevel(name string) am.LogLevel {
	v, _ := strconv.Atoi(os.Getenv(name))
	return am.LogLevel(v)
}

// ///// ///// /////
// ///// METRICS
// ///// ///// /////

type Metrics struct {
	sim             *Sim
	Peers           int
	Topics          int
	PeersWithTopics int
	Conns           int
	Streams         int
	Friendships     int
	PeersPerTopic   float32
	MsgsMiss        int
	MsgsRecv        int
}

func (m *Metrics) String() string {
	connsPerPeerAvg := m.Conns / max(m.Peers, 1)

	return m.sim.p.Sprintf(
		"Peers: %d \nTopics: %d \nConnections: %d \nConns / peer: %d \nStreams: %d \nFriendships: %d \nPeers With Topics: %d \nPeersPerTopic: %.2f \n"+
			"MsgsMiss: %d \nMsgsRecv: %d\n",
		m.Peers, m.Topics, m.Conns, connsPerPeerAvg, m.Streams, m.Friendships,
		m.PeersWithTopics, m.PeersPerTopic, m.MsgsMiss, m.MsgsRecv)
}

type PrometheusMetrics struct {
	Peers           prometheus.Gauge
	Topics          prometheus.Gauge
	Connections     prometheus.Gauge
	Streams         prometheus.Gauge
	Friendships     prometheus.Gauge
	PeersWithTopics prometheus.Gauge
	PeersPerTopic   prometheus.Gauge
	MsgsMiss        prometheus.Gauge
	MsgsRecv        prometheus.Gauge
}

func NewPrometheusMetrics() *PrometheusMetrics {
	return &PrometheusMetrics{
		Peers: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "peers",
			Help:      "Number of peers",
			Namespace: "sim",
		}),
		Topics: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "topics",
			Help:      "Number of topics",
			Namespace: "sim",
		}),
		Connections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "connections",
			Help:      "Number of connections",
			Namespace: "sim",
		}),
		Streams: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "streams",
			Help:      "Number of streams",
			Namespace: "sim",
		}),
		Friendships: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "friends",
			Help:      "Number of friendships",
			Namespace: "sim",
		}),
		PeersWithTopics: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "peers_with_topics",
			Help:      "Number of peers with topics",
			Namespace: "sim",
		}),
		PeersPerTopic: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "peers_per_topic",
			Help:      "Number of peers per topic",
			Namespace: "sim",
		}),
		MsgsMiss: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "msgs_miss",
			Help:      "Number of missed messages",
			Namespace: "sim",
		}),
		MsgsRecv: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:      "msgs_recv",
			Help:      "Number of received messages",
			Namespace: "sim",
		}),
	}
}

func (pm *PrometheusMetrics) Metrics() []prometheus.Collector {
	return []prometheus.Collector{
		pm.Peers,
		pm.Topics,
		pm.Connections,
		pm.Streams,
		pm.Friendships,
		pm.PeersWithTopics,
		pm.PeersPerTopic,
	}
}

func (pm *PrometheusMetrics) Refresh(m *Metrics) {
	pm.Peers.Set(float64(m.Peers))
	pm.Topics.Set(float64(m.Topics))
	pm.Connections.Set(float64(m.Conns))
	pm.Streams.Set(float64(m.Streams))
	pm.Friendships.Set(float64(m.Friendships))
	pm.PeersWithTopics.Set(float64(m.PeersWithTopics))
	pm.PeersPerTopic.Set(float64(m.PeersPerTopic))
	pm.MsgsMiss.Set(float64(m.MsgsMiss))
	pm.MsgsRecv.Set(float64(m.MsgsRecv))
}

func (pm *PrometheusMetrics) Close() {
	// zero all the metrics

	pm.Peers.Set(0)
	pm.Topics.Set(0)
	pm.Connections.Set(0)
	pm.Streams.Set(0)
	pm.Friendships.Set(0)
	pm.PeersWithTopics.Set(0)
	pm.PeersPerTopic.Set(0)
	pm.MsgsMiss.Set(0)
	pm.MsgsRecv.Set(0)
}

// ///// ///// /////
// ///// HELPERS
// ///// ///// /////

var topicRegex = regexp.MustCompile("[^a-zA-Z0-9]+")

func randTopicName() string {
	name := strings.ToLower(gofakeit.MovieName())
	name = topicRegex.ReplaceAllString(name, "-")
	return "t-" + name
}

func randFloatRange(min, max float64) float64 {
	return min + rand.Float64()*(max-min)
}
