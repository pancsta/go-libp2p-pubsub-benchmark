package pubsub

import (
	"context"
	"encoding/base64"
	logStd "log"
	"strconv"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	mon "github.com/pancsta/go-libp2p-pubsub-benchmark/pkg/psmon"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

var psmon *mon.PSMon

func init() {
	psmon = mon.NewPSMon("", NewOtelProvider)
}

func psmonStartPubsub(psCtx context.Context, hostNum int, h host.Host, opts []Option) (context.Context, []Option) {
	// TODO remove
	hostName := strconv.Itoa(hostNum + 1)
	psmonCtx := context.WithValue(psCtx, "psmonHostNum", hostNum+1)

	if psmon.IsTracing() {
		hostCtx, hostSpan := psmon.OtelTracer.Start(psmonCtx, "host:"+hostName, trace.WithAttributes(
			attribute.String("host_id", h.ID().String()),
		))
		psmon.TopSpans = append(psmon.TopSpans, hostSpan)

		if psmon.IsTracingHost() {
			opts = append(opts, WithOtelPubsubTracer(psmon.OtelTracer))
		}

		return hostCtx, opts
	}

	return psmonCtx, opts
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

func WithOtelPubsubTracer(otelTracer trace.Tracer) Option {
	return func(p *PubSub) error {
		attach := WithRawTracer(NewOtelPubsubTracer(p.ctx, otelTracer))
		return attach(p)
	}
}

type OtelPubsubTracer struct {
	ctx        context.Context
	span       trace.Span
	otelTracer trace.Tracer

	groups   map[string]trace.Span
	groupsMx sync.Mutex
	topics   map[string]trace.Span
	topicsMx sync.Mutex
}

func NewOtelPubsubTracer(ctx context.Context, otelTracer trace.Tracer) *OtelPubsubTracer {
	psCtx, psSpan := otelTracer.Start(ctx, "pubsub")

	tracer := &OtelPubsubTracer{
		ctx:        psCtx,
		span:       psSpan,
		otelTracer: otelTracer,
		topics:     make(map[string]trace.Span),
		groups:     make(map[string]trace.Span),
		groupsMx:   sync.Mutex{},
		topicsMx:   sync.Mutex{},
	}
	psmon.PSTracers = append(psmon.PSTracers, tracer)

	return tracer
}
func (t *OtelPubsubTracer) getGroupCtx(name string) context.Context {
	t.groupsMx.Lock()
	defer t.groupsMx.Unlock()

	span, ok := t.groups[name]
	if !ok {
		_, span = t.otelTracer.Start(t.ctx, name)
		t.groups[name] = span
		span.End()
	}

	return trace.ContextWithSpan(t.ctx, span)
}

func (t *OtelPubsubTracer) AddPeer(p peer.ID, proto protocol.ID) {
	ctx := t.getGroupCtx("add_peer")
	_, span := t.otelTracer.Start(ctx, "add_peer", trace.WithAttributes(
		attribute.String("peer_id", p.String()),
	))
	span.End()
}

func (t *OtelPubsubTracer) RemovePeer(p peer.ID) {
	ctx := t.getGroupCtx("remove_peer")
	_, span := t.otelTracer.Start(ctx, "remove_peer", trace.WithAttributes(
		attribute.String("peer_id", p.String()),
	))

	span.End()
}

func (t *OtelPubsubTracer) Join(topic string) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	ctx := t.getGroupCtx("topic")
	_, span := t.otelTracer.Start(ctx, "topic:"+topic, trace.WithAttributes(
		attribute.String("topic", topic),
	))
	t.topics[topic] = span
}

func (t *OtelPubsubTracer) Leave(topic string) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	topicSpan, ok := t.topics[topic]
	if !ok {
		logStd.Println("MISS: leave without a topic")
		return
	}

	topicSpan.End()
	delete(t.topics, topic)
}

func (t *OtelPubsubTracer) Graft(p peer.ID, topic string) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	topicSpan, ok := t.topics[topic]
	if !ok {
		logStd.Println("MISS: graft without a topic")
		return
	}

	ctx := trace.ContextWithSpan(t.ctx, topicSpan)
	_, span := t.otelTracer.Start(ctx, "graft", trace.WithAttributes(
		attribute.String("peer_id", p.String()),
	))

	span.End()
}

func (t *OtelPubsubTracer) Prune(p peer.ID, topic string) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	topicSpan, ok := t.topics[topic]
	if !ok {
		logStd.Println("MISS: prine without a topic")
		return
	}

	ctx := trace.ContextWithSpan(t.ctx, topicSpan)
	_, span := t.otelTracer.Start(ctx, "prune", trace.WithAttributes(
		attribute.String("peer_id", p.String()),
	))

	span.End()
}

func (t *OtelPubsubTracer) DuplicateMessage(msg *Message) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	topic := *msg.Message.Topic
	topicSpan, ok := t.topics[topic]
	if !ok {
		logStd.Println("MISS: duplicate without a topic")
		return
	}

	ctx := trace.ContextWithSpan(t.ctx, topicSpan)
	id := psmonStrMsgID(msg)
	_, span := t.otelTracer.Start(ctx, "duplicate_msg", trace.WithAttributes(
		attribute.String("msg_id", id),
	))

	span.End()
}

func (t *OtelPubsubTracer) RecvRPC(rpc *RPC) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	ctx := t.getGroupCtx("recv_rpc")
	_, span := t.otelTracer.Start(ctx, "recv_rpc")

	span.End()
}

func (t *OtelPubsubTracer) SendRPC(rpc *RPC, p peer.ID) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	ctx := t.getGroupCtx("send_rpc")
	_, span := t.otelTracer.Start(ctx, "send_rpc", trace.WithAttributes(
		attribute.String("peer_id", p.String()),
	))

	span.End()
}

func (t *OtelPubsubTracer) DropRPC(rpc *RPC, p peer.ID) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	ctx := t.getGroupCtx("drop_rpc")
	_, span := t.otelTracer.Start(ctx, "drop_rpc", trace.WithAttributes(
		attribute.String("peer_id", p.String()),
	))

	span.End()
}

func (t *OtelPubsubTracer) UndeliverableMessage(msg *Message) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	topic := *msg.Message.Topic
	topicSpan, ok := t.topics[topic]
	if !ok {
		logStd.Println("MISS: undeliverable without a topic")
		return
	}

	ctx := trace.ContextWithSpan(t.ctx, topicSpan)
	id := psmonStrMsgID(msg)
	_, span := t.otelTracer.Start(ctx, "undeliverable_msg", trace.WithAttributes(
		attribute.String("msg_id", id),
	))

	span.End()
}

func (t *OtelPubsubTracer) ThrottlePeer(p peer.ID) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	ctx := t.getGroupCtx("throttle_peer")
	_, span := t.otelTracer.Start(ctx, "throttle_peer", trace.WithAttributes(
		attribute.String("peer_id", p.String()),
	))

	span.End()
}

func (t *OtelPubsubTracer) ValidateMessage(msg *Message) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	topic := *msg.Message.Topic
	topicSpan, ok := t.topics[topic]
	if !ok {
		logStd.Println("MISS: Validate without a topic")
		return
	}

	ctx := trace.ContextWithSpan(t.ctx, topicSpan)
	id := psmonStrMsgID(msg)
	_, span := t.otelTracer.Start(ctx, "validate_msg", trace.WithAttributes(
		attribute.String("msg_id", id),
	))

	span.End()
}

func (t *OtelPubsubTracer) RejectMessage(msg *Message, reason string) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	topic := *msg.Message.Topic
	topicSpan, ok := t.topics[topic]
	if !ok {
		logStd.Println("MISS: Reject without a topic")
		return
	}

	id := psmonStrMsgID(msg)
	ctx := trace.ContextWithSpan(t.ctx, topicSpan)
	_, span := t.otelTracer.Start(ctx, "reject_msg", trace.WithAttributes(
		attribute.String("msg_id", id),
		attribute.String("reason", reason),
	))

	span.End()
}

func (t *OtelPubsubTracer) DeliverMessage(msg *Message) {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	topic := *msg.Message.Topic
	topicSpan, ok := t.topics[topic]
	if !ok {
		logStd.Println("MISS: Deliver without a topic")
		return
	}

	ctx := trace.ContextWithSpan(t.ctx, topicSpan)
	id := psmonStrMsgID(msg)
	_, span := t.otelTracer.Start(ctx, "deliver_msg", trace.WithAttributes(
		attribute.String("msg_id", id),
	))

	span.End()
}

// TODO: fulfillPromise, GetBrokenPromises, AddPromise

func (t *OtelPubsubTracer) End() {
	t.topicsMx.Lock()
	defer t.topicsMx.Unlock()

	// end all open topics
	for _, topic := range t.topics {
		topic.End()
	}

	// end the main span
	t.span.End()
}

func psmonStrMsgID(msg *Message) string {
	return base64.StdEncoding.EncodeToString([]byte(msg.ID))
}
