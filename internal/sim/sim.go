// Based on github.com/libp2p/universal-connectivity/go-peer

package sim

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	ssp "github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim/states/peer"
	ss "github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim/states/sim"
	sst "github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim/states/topic"
	"github.com/pancsta/go-libp2p-pubsub-benchmark/pkg/psmon"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	"github.com/pancsta/asyncmachine-go/pkg/telemetry"
	amPrometheus "github.com/pancsta/asyncmachine-go/pkg/telemetry/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/samber/lo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/exp/maps"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// TODO config file

// config vars
var (
	//amLogLevel = am.LogOps
	//amLogLevel = am.LogDecisions
	//amLogLevel  = am.LogEverything
	maxHistoryEntries  = 1000
	hbFreq             = 1 * time.Second
	metricsFreq        = 5 * time.Second
	metricsPrintFreq   = 10 * time.Second
	discoveryFreq      = 1 * time.Second
	verifyMsgsDelay    = 100 * time.Millisecond
	msgDeliveryTimeout = 5 * time.Second
	peakTopicPeers     = 5
	defaultFreq        = Freq{D: 0, Up: 0.75, Down: 1.25, Unit: time.Second}
	printMetrics       = true
	promUrl            = "http://localhost:9091"

	// start values
	initialTopics        = 10
	initialPeers         = 50
	initialPeersPerTopic = 5

	// limits
	maxTopics         = 20
	maxTopicPerPeer   = 15
	maxPeersPerTopic  = 50
	maxFriendsPerPeer = 10
	// buffer size
	maxMsgsPerPeer = 100

	// sim states frequencies (smaller D happens more often)
	addRandomFriendFreq  = Freq{D: 2}
	gcFreq               = Freq{D: 100}
	joinRandomTopicFreq  = Freq{D: 2}
	joinFriendsTopicFreq = Freq{D: 4}
	msgRandomTopicFreq   = Freq{D: 3}
	addPeerFreq          = Freq{D: 3}
	removePeerFreq       = Freq{D: 50}
	addTopicFreq         = Freq{D: 3}
	removeTopicFreq      = Freq{D: 80}
	peakRandTopicFreq    = Freq{D: 10}
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

///////////////
///// TOPIC /////
///////////////

type Topic struct {
	*am.ExceptionHandler

	sim     *Sim
	mach    *am.Machine
	history *history.History

	id    string
	peers []string
}

// TODO bind peer info to states
func newTopic(sim *Sim, name string) (*Topic, error) {
	mach, err := am.NewCommon(sim.Mach.Ctx, "sim-"+name, sst.States, sst.Names, nil, sim.Mach, nil)
	if err != nil {
		return nil, err
	}

	debugMach(mach)
	t := &Topic{
		sim: sim,
		id:  name,
	}
	t.mach = mach
	t.history = history.Track(mach, sst.Names, maxHistoryEntries)

	return t, nil
}

///////////////
///// SIM /////
///////////////

type Sim struct {
	*am.ExceptionHandler

	exportMetrics bool
	debugAM       bool

	Mach     *am.Machine
	machArgs []string
	history  *history.History
	p        *message.Printer

	topics      map[string]*Topic
	peers       map[string]*Peer
	nextPeerNum int
	dht         *Peer

	metrics      *Metrics
	rootSpan     trace.Span
	metricsProm  *PrometheusMetrics
	OtelTracer   trace.Tracer
	OtelProvider *sdktrace.TracerProvider
	amTracer     *telemetry.OtelMachTracer
	promPusher   *push.Pusher
	machMetrics  map[string]*amPrometheus.Metrics
	promUrl      string
	discServer   *mockDiscoveryServer
	MaxPeers     int
}

func NewSim(ctx context.Context, exportMetrics, debugAM bool) (*Sim, error) {
	sim := &Sim{
		topics:        make(map[string]*Topic),
		peers:         make(map[string]*Peer),
		machArgs:      []string{"Topic.id", "Peer.id", "amount", "token", "[]Peer.id"},
		machMetrics:   make(map[string]*amPrometheus.Metrics),
		exportMetrics: exportMetrics,
		promUrl:       promUrl,
		debugAM:       debugAM,
		MaxPeers:      30,
	}

	opts := &am.Opts{}
	// TODO sim tracing?
	//if traceAM == "1" || traceAM == "2" {
	//	sim.initTracing(ctx)
	//	sim.traceMach(opts, traceAM == "2")
	//}

	mach, err := am.NewCommon(ctx, "sim", ss.States, ss.Names, sim, nil, opts)
	if err != nil {
		return nil, err
	}
	mach.SetLogArgs(am.NewArgsMapper(sim.machArgs, 0))
	mach.SetLogLevel(am.LogChanges)
	sim.Mach = mach
	sim.history = history.Track(mach, ss.Names, maxHistoryEntries)
	sim.metrics = &Metrics{sim: sim}

	if debugAM {
		debugMach(mach)
	}
	if exportMetrics {
		sim.initPrometheus(mach)
		mach.RegisterDisposalHandler(func() {
			sim.metricsProm.Close()
			err := sim.promPusher.Push()
			if err != nil {
				fmt.Println("Error pushing metrics: ", err)
			}
		})
	}

	return sim, nil
}

func (s *Sim) initPrometheus(mach *am.Machine) {
	s.metricsProm = NewPrometheusMetrics()
	s.promPusher = push.New(s.promUrl, "sim")

	s.promPusher.Collector(s.metricsProm.Peers)
	s.promPusher.Collector(s.metricsProm.Topics)
	s.promPusher.Collector(s.metricsProm.Connections)
	s.promPusher.Collector(s.metricsProm.Streams)
	s.promPusher.Collector(s.metricsProm.Friendships)
	s.promPusher.Collector(s.metricsProm.PeersWithTopics)
	s.promPusher.Collector(s.metricsProm.PeersPerTopic)
	s.promPusher.Collector(s.metricsProm.MsgsRecv)
	s.promPusher.Collector(s.metricsProm.MsgsMiss)

	s.bindMachToPrometheus(mach)
}

func (s *Sim) bindMachToPrometheus(mach *am.Machine) {
	mm := amPrometheus.TransitionsToPrometheus(mach, metricsFreq)
	s.machMetrics[mach.ID] = mm

	s.promPusher.Collector(mm.StatesAmount)
	s.promPusher.Collector(mm.RelAmount)
	s.promPusher.Collector(mm.RefStatesAmount)
	s.promPusher.Collector(mm.QueueSize)
	s.promPusher.Collector(mm.StepsAmount)
	s.promPusher.Collector(mm.HandlersAmount)
	s.promPusher.Collector(mm.TxTime)
	s.promPusher.Collector(mm.StatesActiveAmount)
	s.promPusher.Collector(mm.StatesInactiveAmount)
	s.promPusher.Collector(mm.TxTick)
	s.promPusher.Collector(mm.ExceptionsCount)
	s.promPusher.Collector(mm.StatesAdded)
	s.promPusher.Collector(mm.StatesRemoved)
	s.promPusher.Collector(mm.StatesTouched)
}

func (s *Sim) initTracing(ctx context.Context) {
	tracer, provider, err := NewOtelProvider(ctx)
	s.OtelTracer = tracer
	s.OtelProvider = provider
	if err != nil {
		log.Fatal(err)
	}
	_, s.rootSpan = tracer.Start(ctx, "sim")
}

func (s *Sim) traceMach(opts *am.Opts, traceTransitions bool) {
	tracer := telemetry.NewOtelMachTracer(s.OtelTracer, &telemetry.OtelMachTracerOpts{
		SkipTransitions: !traceTransitions,
		Logf:            logNoop,
	})
	s.amTracer = tracer
	opts.Tracers = []am.Tracer{tracer}
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

// HANDLERS

func (s *Sim) StartState(e *am.Event) {
	s.p = message.NewPrinter(language.English)

	// TODO DHT
	// init a DHT peer, which doesn't take part in the sim as a peer
	//if err := s.initDHTPeer(); err != nil {
	//	// TODO use AddErr
	//	s.Mach.Remove1(ss.Start, nil)
	//	s.Mach.Log("Error creating peer: %s", err)
	//	return
	//}

	s.discServer = newMockDiscoveryServer()

	// init the initial simulation
	s.Mach.Add1(ss.AddPeer, am.A{"amount": initialPeers})
	s.Mach.Add1(ss.AddTopic, am.A{"amount": initialTopics})

	// start the metrics loop
	stateCtx := e.Machine.NewStateCtx(ss.Start)
	var lastMetricsUpdate time.Time
	var lastMetricsPrint time.Time

	go func() {
		if stateCtx.Err() != nil {
			return // expired
		}
		s.Mach.Add1(ss.Heartbeat, nil)
		for stateCtx.Err() == nil {
			select {

			case <-stateCtx.Done():
				return // expired

			case <-time.After(hbFreq):

				if s.exportMetrics && time.Since(lastMetricsUpdate) > metricsFreq {
					s.Mach.Add1(ss.RefreshMetrics, nil)
					lastMetricsUpdate = time.Now()
				}

				if s.exportMetrics && printMetrics && time.Since(lastMetricsPrint) > metricsPrintFreq {
					fmt.Println(s.metrics)
					lastMetricsPrint = time.Now()
				}

				s.Mach.Add1(ss.Heartbeat, nil)
			}
		}
	}()
}

func (s *Sim) initDHTPeer() error {
	dhtPeer, err := newPeer(s, "sim-dht", -1)
	if err != nil {
		return err
	}

	s.Mach.Log("Starting DHT")
	dhtPeer.mach.Add(am.S{ssp.Start, ssp.IsDHT}, nil)
	s.dht = dhtPeer
	<-dhtPeer.mach.When1(ssp.Ready, nil)

	return nil
}

func (s *Sim) HeartbeatState(e *am.Event) {
	s.Mach.Remove1(ss.Heartbeat, nil)

	randomStates := am.S{
		ss.AddPeer,
		ss.RemovePeer,
		ss.AddTopic,
		ss.RemoveTopic,
		ss.PeakRandTopic,
		ss.AddRandomFriend,
		ss.GC,
		ss.JoinRandomTopic,
		ss.JoinFriendsTopic,
		ss.MsgRandomTopic,
	}
	toActivate := s.getRandStatesToActive(randomStates, s.history)

	for _, state := range toActivate {
		s.Mach.Add1(state, nil)
	}
}

func (s *Sim) AddPeerEnter(e *am.Event) bool {
	_, ok := e.Args["amount"].(int)
	return ok && len(s.peers) < s.MaxPeers
}

func (s *Sim) AddPeerState(e *am.Event) {
	s.Mach.Remove1(ss.AddPeer, nil)

	amount := e.Args["amount"].(int)
	if amount == 0 {
		amount = 1
	}

	// TODO automate
	//addrs := s.getBootstrapNodes(5)
	// use DHT as the only bootstrap node
	//addrs := []peer.AddrInfo{{
	//	ID:    s.dht.host.ID(),
	//	Addrs: s.dht.host.Addrs(),
	//}}

	for i := 0; i < amount; i++ {
		if len(s.peers) >= s.MaxPeers {
			break
		}

		num := s.nextPeerNum
		id := "sim-p" + strconv.Itoa(num)
		p, err := newPeer(s, id, num)
		if err != nil {
			s.Mach.Log("Error creating peer: %s", err)
			return
		}

		// store and start
		s.peers[p.id] = p
		// TODO automate
		//p.bootstrapNodes = addrs

		// peer.Start
		p.mach.Add1(ssp.Start, nil)
		s.nextPeerNum++
	}
}

func (s *Sim) RemovePeerEnter(e *am.Event) bool {
	return len(s.peers) > initialPeers
}

func (s *Sim) RemovePeerState(e *am.Event) {
	s.Mach.Remove1(ss.RemovePeer, nil)

	randID := maps.Keys(s.peers)[rand.Intn(len(s.peers))]
	p := s.peers[randID]
	delete(s.peers, randID)

	// remove from friends lists
	for _, fid := range p.simFriends {
		friend := s.peers[fid]
		if friend == nil {
			s.Mach.Log("Error: friend not found: %s", fid)
			continue
		}
		friend.simFriends = lo.Without(friend.simFriends, p.id)
	}

	// shutdown peer
	p.mach.Remove1(ssp.Start, nil)
}

func (s *Sim) AddTopicEnter(e *am.Event) bool {
	p := s.pickRandPeerCond(func(p *Peer) bool {
		return len(p.simTopics) < maxTopicPerPeer
	})
	return len(s.topics) < maxTopics && p != nil
}

func (s *Sim) AddTopicState(e *am.Event) {
	s.Mach.Remove1(ss.AddTopic, nil)

	amount, ok := e.Args["amount"].(int)
	if !ok {
		amount = 1
	}

	for i := 0; i < amount; i++ {
		if len(s.topics) >= maxTopics {
			break
		}
		name := randTopicName()
		if _, ok := s.topics[name]; ok {
			continue
		}
		topic, err := newTopic(s, name)
		if err != nil {
			s.Mach.AddErr(err)
			return
		}
		s.topics[topic.id] = topic

		// add random peers
		for i := 0; i < initialPeersPerTopic && i < len(s.peers); i++ {
			p := s.pickRandPeerCond(func(p *Peer) bool {
				return len(p.simTopics) < maxTopicPerPeer
			})
			if p == nil {
				s.Mach.Log("Error: no peer found for topic %s", topic.id)
				break
			}
			if slices.Contains(p.simTopics, topic.id) {
				s.Mach.Log("Error: DUP topic %s for peer %s", topic.id, p.id)
				continue
			}

			p.mach.Add1(ssp.JoiningTopic, am.A{"Topic.id": topic.id})
		}
	}
}

func (s *Sim) RemoveTopicEnter(e *am.Event) bool {
	return len(s.topics) != 0
}

func (s *Sim) RemoveTopicState(e *am.Event) {
	s.Mach.Remove1(ss.RemoveTopic, nil)

	// remove topic from sim
	randTID := maps.Keys(s.topics)[rand.Intn(len(s.topics))]
	topic := s.topics[randTID]
	delete(s.topics, randTID)

	// remove topic from peers
	for _, p := range s.GetTopicPeers(topic.id) {
		p.mach.Add1(ssp.LeavingTopic, am.A{"Topic.id": topic.id})
		idx := slices.Index(p.simTopics, topic.id)
		if idx == -1 {
			s.Mach.Log("Error: topic not found %s", topic.id)
			return
		}
		p.simTopics = slices.Delete(p.simTopics, idx, idx+1)
		delete(p.simTopicJoined, topic.id)
	}
	// shutdown
	topic.mach.Dispose()
}

func (s *Sim) AddRandomFriendEnter(e *am.Event) bool {
	p1 := s.pickRandPeer()
	p2 := s.pickRandPeer()
	if p1 == nil || p2 == nil || p1 == p2 ||
		slices.Contains(p1.simFriends, p2.id) ||
		len(p1.simTopics) < 1 ||
		len(p1.simFriends) >= maxFriendsPerPeer ||
		len(p2.simFriends) >= maxFriendsPerPeer {

		return false
	}
	e.Args["[]Peer.id"] = []string{p1.id, p2.id}
	return true
}

// AddRandomFriendState random peer adds another random peer as a friend
func (s *Sim) AddRandomFriendState(e *am.Event) {
	s.Mach.Remove1(ss.AddRandomFriend, nil)

	ids := e.Args["[]Peer.id"].([]string)
	p1 := s.peers[ids[0]]
	p2 := s.peers[ids[1]]

	p1.simFriends = append(p1.simFriends, p2.id)
	p2.simFriends = append(p2.simFriends, p1.id)
	randTopic := p1.simTopics[rand.Intn(len(p1.simTopics))]

	// join the topic with p2
	if !slices.Contains(p2.simTopics, randTopic) {
		p2.simTopics = append(p2.simTopics, randTopic)
		p2.simTopicJoined[randTopic] = time.Now()
		p2.mach.Add1(ssp.JoiningTopic, am.A{"Topic.id": randTopic})
	}
	s.Mach.Log("Added friend: %s + %s", p1.id, p2.id)
}

// GCState does various periodic cleanups.
func (s *Sim) GCState(e *am.Event) {
	s.Mach.Remove1(ss.GC, nil)

	// peers remove friends which are not sharing any topic with them
	for _, p1 := range s.peers {
		var newFriends []string
		for _, fid := range p1.simFriends {
			p2 := s.peers[fid]
			if p2 == nil {
				s.Mach.Log("Friend not found: %s", fid)
				continue
			}
			if len(lo.Intersect(p1.simTopics, p2.simTopics)) > 0 {
				newFriends = append(newFriends, fid)
			} else {
				p2.simFriends = lo.Without(p2.simFriends, p1.id)
				p1.simFriends = lo.Without(p1.simFriends, p2.id)
				s.Mach.Log("Removed friend: %s - %s", p1.id, p2.id)
			}
		}
		p1.simFriends = newFriends
	}

	// peers keep only last N msgs
	for _, p := range s.peers {
		if len(p.msgs) > maxMsgsPerPeer {
			p.msgs = p.msgs[len(p.msgs)-maxMsgsPerPeer:]
		}
	}
}

func (s *Sim) JoinRandomTopicEnter(e *am.Event) bool {
	p := s.pickRandPeer()
	topic := s.pickRandTopic()

	if p == nil || topic == nil || slices.Contains(p.simTopics, topic.id) || len(topic.peers) >= maxPeersPerTopic {
		return false
	}

	e.Args["Topic.id"] = topic.id
	e.Args["Peer.id"] = p.id

	return true
}

// JoinRandomTopicState random peer joins a random topic
func (s *Sim) JoinRandomTopicState(e *am.Event) {
	s.Mach.Remove1(ss.JoinRandomTopic, nil)

	pid := e.Args["Peer.id"].(string)
	tid := e.Args["Topic.id"].(string)
	p := s.peers[pid]

	p.simTopics = append(p.simTopics, tid)
	p.simTopicJoined[tid] = time.Now()
	p.mach.Add1(ssp.JoiningTopic, am.A{"Topic.id": tid})
}

func (s *Sim) JoinFriendsTopicEnter(e *am.Event) bool {
	p := s.pickRandPeer()
	if p == nil || len(p.simFriends) == 0 {
		return false
	}

	friend := s.peers[p.simFriends[rand.Intn(len(p.simFriends))]]
	if friend == nil {
		s.Mach.Log("Error: friend not found")
		return false
	}
	if len(friend.simTopics) == 0 {
		return false
	}

	topic := friend.simTopics[rand.Intn(len(friend.simTopics))]
	if slices.Contains(p.simTopics, topic) {
		return false
	}

	e.Args["Topic.id"] = topic
	e.Args["Peer.id"] = p.id

	return true
}

// JoinFriendsTopicState random peer joins random friends topic
func (s *Sim) JoinFriendsTopicState(e *am.Event) {
	s.Mach.Remove1(ss.JoinFriendsTopic, nil)

	pid := e.Args["Peer.id"].(string)
	topic := e.Args["Topic.id"].(string)

	p := s.peers[pid]

	p.simTopics = append(p.simTopics, topic)
	p.simTopicJoined[topic] = time.Now()

	p.mach.Add1(ssp.JoiningTopic, am.A{"Topic.id": topic})
}

func (s *Sim) MsgRandomTopicEnter(e *am.Event) bool {

	p := s.pickRandPeer()
	if p == nil || len(p.simTopics) == 0 {
		// not enough topics
		return false
	}
	randTopic := p.simTopics[rand.Intn(len(p.simTopics))]

	if len(s.GetTopicPeers(randTopic)) < 2 {
		// Not enough peers in topic
		return false
	}

	// pass the chosen peer and topic
	e.Args["Topic.id"] = randTopic
	e.Args["Peer.id"] = p.id

	return len(s.topics) > 0
}

// MsgRandomTopicState random peer msgs a random topic
func (s *Sim) MsgRandomTopicState(e *am.Event) {
	s.Mach.Remove1(ss.MsgRandomTopic, nil)

	pid := e.Args["Peer.id"].(string)
	topic := e.Args["Topic.id"].(string)
	p := s.peers[pid]

	randMsg := strconv.Itoa(time.Now().Nanosecond())
	msgs := []string{p.id, randMsg}

	go func() {
		token := strconv.Itoa(rand.Intn(5000000))
		whenSent := p.mach.WhenArgs(ssp.MsgsSent, am.A{"token": token}, nil)
		p.mach.Add1(ssp.SendingMsgs, am.A{
			"Topic.id": topic,
			"msgs":     msgs,
			"token":    token,
		})
		sendT := time.Now()
		select {
		case <-time.After(msgDeliveryTimeout):
			s.Mach.Log("Timeout sending msgs to %s", topic)
		case <-whenSent:
			time.Sleep(verifyMsgsDelay)
			s.Mach.Add1(ss.VerifyMsgsRecv, am.A{
				"Peer.id":  p.id,
				"Topic.id": topic,
				"msgs":     msgs,
				"time":     sendT,
			})
		}
	}()
}

func (s *Sim) VerifyMsgsRecvEnter(e *am.Event) bool {
	_, ok1 := e.Args["Peer.id"].(string)
	_, ok2 := e.Args["Topic.id"].(string)
	_, ok3 := e.Args["msgs"].([]string)
	return ok1 && ok2 && ok3
}

func (s *Sim) VerifyMsgsRecvState(e *am.Event) {
	s.Mach.Remove1(ss.VerifyMsgsRecv, nil)

	pid := e.Args["Peer.id"].(string)
	topic := e.Args["Topic.id"].(string)
	msgs := e.Args["msgs"].([]string)
	sendT := e.Args["time"].(time.Time)

	// verify that all topic peers received the msg (besides the sender pid)
	recv := 0
	miss := 0
	for _, p := range s.GetTopicPeers(topic) {
		// skip the author and peers which connected after the send time
		if p.id == pid || p.simTopicJoined[topic].After(sendT) {
			continue
		}
		// check msgs
		for _, msg := range msgs {
			if slices.Contains(p.msgs, msg) {
				recv++
				continue
			}
			miss++
		}
	}
	s.metrics.MsgsRecv += recv
	s.metrics.MsgsMiss += miss
	s.Mach.Log("%d received and %d missing for %d peers", recv, miss, len(s.GetTopicPeers(topic))-1)
}

func (s *Sim) PeakRandTopicEnter(e *am.Event) bool {
	if len(s.topics) < 3 || len(s.GetReadyPeers()) < peakTopicPeers*2 {
		return false
	}
	return true
}

func (s *Sim) PeakRandTopicState(e *am.Event) {
	s.Mach.Remove1(ss.PeakRandTopic, nil)

	topic := s.pickRandTopic()
	if topic == nil {
		return
	}
	s.Mach.Log("Peaking topic: %s", topic.id)
	// add N random peers into that random topic
	for i := 0; i < peakTopicPeers; i++ {
		if len(topic.peers) >= maxPeersPerTopic {
			break
		}
		p := s.pickRandPeer()
		if p == nil {
			break
		}
		topic.peers = append(topic.peers, p.id)
		p.mach.Add1(ssp.JoiningTopic, am.A{"Topic.id": topic.id})
	}
}

///////////////
///// METHODS
///////////////

func (s *Sim) RefreshMetricsState(e *am.Event) {
	s.Mach.Remove1(ss.RefreshMetrics, nil)

	friends := 0
	joined := 0
	conns := 0
	streams := 0
	pWithTopics := 0
	for _, p := range s.peers {
		friends += len(p.simFriends)
		if p.mach.Not1(ssp.Connected) {
			continue
		}
		if len(p.simTopics) > 0 {
			pWithTopics++
		}
		joined += len(p.simTopics)
		for _, conn := range p.host.Network().Conns() {
			conns++
			streams += conn.Stat().NumStreams
		}
	}
	s.metrics.Peers = len(s.peers)
	s.metrics.Topics = len(s.topics)
	s.metrics.Conns = conns
	s.metrics.Streams = streams
	s.metrics.Friendships = friends / 2
	s.metrics.PeersWithTopics = pWithTopics
	s.metrics.PeersPerTopic = joined / max(len(s.topics), 1)

	if s.exportMetrics {
		s.metricsProm.Refresh(s.metrics)
		err := s.promPusher.Push()
		if err != nil {
			s.Mach.Log("Error pushing metrics: %s", err)
		}
	}
}

func (s *Sim) GetTopicPeers(topic string) []*Peer {
	var ret []*Peer
	for _, p := range s.GetReadyPeers() {
		if slices.Contains(p.simTopics, topic) {
			ret = append(ret, p)
		}
	}
	return ret
}

func (s *Sim) GetReadyPeers() []*Peer {
	var ret []*Peer
	for _, p := range s.peers {
		if p.mach.Is1(ssp.Ready) {
			ret = append(ret, p)
		}
	}
	return ret
}

func (s *Sim) getRandStatesToActive(states []string, history *history.History) []string {
	var ret []string
	// try to activate each random state according to its (randomized) frequency
	for _, state := range states {
		freq, err := getStateFreq(state)
		if err != nil {
			log.Println(err)
			continue
		}
		if freq.D == 0 {
			continue
		}
		randModifier := randFloatRange(freq.Down, freq.Up)
		delay := time.Duration(float64(freq.D) * float64(freq.Unit) * randModifier)
		//log.Println(s.p.Sprintf(
		//	"Delay for %s: %v ms", state, number.Decimal(delay.Milliseconds())))
		if delay == 0 {
			log.Println("Ignoring 0 delay for state", state)
			continue
		}
		if history.ActivatedRecently(state, delay) {
			continue
		}
		ret = append(ret, state)
	}
	return ret
}

func (s *Sim) pickRandPeer() *Peer {
	peers := s.GetReadyPeers()
	l := len(peers)
	if l == 0 {
		return nil
	}
	return peers[rand.Intn(l)]
}

func (s *Sim) pickRandPeerCond(cond func(p *Peer) bool) *Peer {
	var conn []*Peer
	for _, p := range s.GetReadyPeers() {
		if cond(p) {
			conn = append(conn, p)
		}
	}
	if len(conn) == 0 {
		return nil
	}
	return conn[rand.Intn(len(conn))]
}

func (s *Sim) pickRandTopic() *Topic {
	l := len(s.topics)
	if l == 0 {
		return nil
	}
	return s.topics[maps.Keys(s.topics)[rand.Intn(l)]]
}

func (s *Sim) getPeerMach(id string) *am.Machine {
	return s.peers[id].mach
}

func (s *Sim) getBootstrapNodes(amount int) []peer.AddrInfo {
	var addrs []peer.AddrInfo
	for _, p := range s.peers {
		if p.mach.Not1(ssp.Connected) {
			continue
		}
		addrs = append(addrs, peer.AddrInfo{
			ID:    p.host.ID(),
			Addrs: p.host.Addrs(),
		})
		if len(addrs) >= amount {
			break
		}
	}
	return addrs
}

///////////////
///// METRICS
///////////////

type Metrics struct {
	sim             *Sim
	Peers           int
	Topics          int
	PeersWithTopics int
	Conns           int
	Streams         int
	Friendships     int
	PeersPerTopic   int
	MsgsMiss        int
	MsgsRecv        int
}

func (m *Metrics) String() string {
	connsPerPeerAvg := m.Conns / max(m.Peers, 1)

	return m.sim.p.Sprintf(
		"Peers: %d \nTopics: %d \nConnections: %d \nConns / peer: %d \nStreams: %d \nFriendships: %d \nPeers With Topics: %d \nPeersPerTopic: %d \n"+
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

///////////////
///// HELPERS
///////////////

var topicRegex = regexp.MustCompile("[^a-zA-Z0-9]+")

func randTopicName() string {
	name := strings.ToLower(gofakeit.MovieName())
	name = topicRegex.ReplaceAllString(name, "-")
	return "t-" + name
}

func randFloatRange(min, max float64) float64 {
	return min + rand.Float64()*(max-min)
}
