// Based on github.com/libp2p/universal-connectivity/go-peer

package sim

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	quicTransport "github.com/libp2p/go-libp2p/p2p/transport/quic"
	webtransport "github.com/libp2p/go-libp2p/p2p/transport/webtransport"
	"github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	pubsub "github.com/pancsta/go-libp2p-pubsub"

	ssp "github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim/states/peer"
)

type Peer struct {

	// sim and machine
	*am.ExceptionHandler
	id      string
	sim     *Sim
	mach    *am.Machine
	history *history.History

	// sim data (only used by Sim)
	simTopics      []string
	simTopicJoined map[string]time.Time
	simFriends     []string

	// pubsub
	host           host.Host
	ps             *pubsub.PubSub
	subs           map[string]*PeerTopic
	key            crypto.PrivKey
	bootstrapNodes []peer.AddrInfo
	msgs           []string
	notify         *network.NotifyBundle
}

func newPeer(sim *Sim, name string, hostNum int) (*Peer, error) {
	p := &Peer{
		sim:            sim,
		id:             name,
		subs:           make(map[string]*PeerTopic),
		simTopicJoined: make(map[string]time.Time),
	}
	ctx := context.WithValue(sim.Mach.Ctx, "psmonHostNum", hostNum)
	mach, err := am.NewCommon(ctx, name, ssp.States, ssp.Names, p, sim.Mach, nil)
	if err != nil {
		return nil, err
	}

	mach.SetTestLogger(log.Printf, EnvLogLevel("SIM_AM_LOG_LEVEL"))
	mach.SetLogArgs(am.NewArgsMapper(sim.machArgs, 0))
	p.mach = mach
	p.history = history.Track(mach, ssp.Names, maxHistoryEntries)

	if sim.debugAM {
		debugMach(mach)
	}

	// export metrics from the 1st peer
	if sim.exportMetrics {
		// TODO remove when Tracer API implemented
		sim.bindMachToPrometheus(p.mach)
	}

	return p, nil
}

func (p *Peer) Start(e *am.Event) {
	p.mach.Add1(ssp.Start, nil)
}

func (p *Peer) GenIdentityState(e *am.Event) {
	stateCtx := p.mach.NewStateCtx(ssp.GenIdentity)

	go func() {
		if stateCtx.Err() != nil {
			return // expired
		}

		// create dir
		dirPath := path.Join("data", "keys")
		err := os.MkdirAll(dirPath, 0700)
		if err != nil {
			p.mach.AddErr(err)
			return
		}

		// create the identity file
		filePath := path.Join(dirPath, p.id)
		identity, err := LoadIdentity(filePath)
		if err != nil {
			p.mach.AddErr(err)
			return
		}
		p.key = identity

		p.mach.Add1(ssp.IdentityReady, nil)
	}()
}

func (p *Peer) StartEnd(e *am.Event) {
	// leave all the topics
	for topic := range p.subs {
		p.mach.Add1(ssp.LeavingTopic, am.A{"Topic.id": topic})
	}
	p.mach.Dispose()
}

func (p *Peer) ConnectingState(e *am.Event) {
	var opts []libp2p.Option

	randPort := randInt(10000, 60000)
	opts = append(opts,
		libp2p.Identity(p.key),
		libp2p.Transport(quicTransport.NewTransport),
		libp2p.Transport(webtransport.New),
		libp2p.ListenAddrStrings(
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", randPort),
			fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1/webtransport", randPort),
		),
	)

	stateCtx := p.mach.NewStateCtx(ssp.Connecting)
	go func() {
		if stateCtx.Err() != nil {
			return // expired
		}

		// create a new libp2p host
		h, err := libp2p.New(opts...)
		if err != nil {
			p.mach.AddErr(err)
			return
		}
		p.host = h
		p.mach.Log("PeerID: %s", h.ID())

		// init mock discovery
		disc := &mockDiscoveryClient{h, p.sim.discServer}
		discOpts := []discovery.Option{discovery.Limit(p.sim.MaxPeers), discovery.TTL(1 * time.Minute)}
		opts := []pubsub.Option{pubsub.WithDiscovery(disc, pubsub.WithDiscoveryOpts(discOpts...))}

		// init pubsub with gossipsub router
		ps, err := pubsub.NewGossipSub(p.mach.Ctx, h, opts...)
		if err != nil {
			p.mach.AddErr(err)
			return
		}
		p.ps = ps

		if p.sim.exportMetrics {
			p.sim.bindMachToPrometheus(p.ps.Mach)
		}

		// this needs to be set manually when defined in sim.env
		p.ps.SetLogLevelAM(EnvLogLevel("PS_AM_LOG_LEVEL"))

		// using mock discovery, stop here
		// TODO config setting
		// TODO support multiple discoveries simultaneously
		p.mach.Add(am.S{ssp.Ready, ssp.Connected}, nil)
		return

		// p.setUpDiscoveries(err, h)
	}()
}

// setUpDiscoveries sets up the DHT and mDNS discovery mechanisms.
func (p *Peer) setUpDiscoveries(err error, h host.Host) {
	// setup DHT with empty discovery peers
	dhtPeer, err := NewDHT(p.mach, h, p.bootstrapNodes)
	if err != nil {
		p.mach.AddErr(err)
		return
	}

	// set up peer discovery
	go DHTDiscover(p.mach, h, dhtPeer, DiscoveryServiceTag)

	// setup local mDNS discovery
	if err := setupMDNSDiscovery(h); err != nil {
		p.mach.AddErr(err)
		return
	}

	for _, addr := range p.bootstrapNodes {
		// p.mach.Log("Connecting to bootstrap node: %s", addr.ID)
		// connect to the peer
		if err := h.Connect(p.mach.Ctx, addr); err != nil {
			p.mach.Log("Failed to connect to peer: %s", err.Error())
			continue
		}
	}

	for _, addr := range h.Addrs() {
		p.mach.Log("Listening on: %s/p2p/%s", addr, h.ID())
	}
	p.notify = &network.NotifyBundle{
		ConnectedF: func(n network.Network, conn network.Conn) {

			// check if dht / bootstrap node
			for _, a := range p.bootstrapNodes {
				if a.ID == conn.RemotePeer() {
					p.mach.Log("Connected to bootstrap node: %s", conn.RemotePeer())
					p.mach.Add1(ssp.BootstrapsConnected, nil)
					return
				}
			}

			// regular peer node
			p.mach.Add(am.S{ssp.EventHostConnected, ssp.Connected}, am.A{
				"peer.ID": conn.RemotePeer()})
		},
		DisconnectedF: func(n network.Network, conn network.Conn) {
			p.mach.Log("Disconnected from %s", conn.RemotePeer())
			// TODO disconnect on the last peer
			// p.mach.Add1(ssp.Disconnecting, nil)
		},
	}
	h.Network().Notify(p.notify)

	if p.mach.Is1(ssp.IsDHT) {
		// DHT is the bootstrap node
		p.mach.Add(am.S{ssp.Ready, ssp.Connected}, nil)
	}
}

func (p *Peer) EventHostConnectedState(e *am.Event) {
	p.mach.Remove1(ssp.EventHostConnected, nil)
}

func (p *Peer) JoiningTopicEnter(e *am.Event) bool {
	topic := e.Args["Topic.id"].(string)
	// TODO check if already joined
	return topic != ""
}

func (p *Peer) JoiningTopicState(e *am.Event) {
	p.mach.Remove1(ssp.JoiningTopic, nil)

	topic := e.Args["Topic.id"].(string)

	go func() {
		psTopic, err := p.ps.Join(topic)
		if err != nil {
			p.mach.Log("Error: failed to join topic: %s", err)
			return
		}

		psSub, err := psTopic.Subscribe()
		if err != nil {
			p.mach.Log("Error: failed to subscribe to topic %s", err)
			return
		}

		ctx, cancel := context.WithCancel(p.mach.Ctx)
		pt := &PeerTopic{
			ctx:     ctx,
			cancel:  cancel,
			peer:    p,
			psTopic: psTopic,
			psSub:   psSub,
			nick:    p.id,
			topic:   topic,
		}

		// try to add the subscription via the event loop
		eval := p.mach.Eval("JoiningTopicState", func() { p.subs[topic] = pt }, nil)
		if !eval {
			p.mach.AddErrStr("JoiningTopicState")
			return
		}

		// start reading messages from the subscription in a loop
		go pt.readLoop()
		p.mach.Add1(ssp.TopicJoined, e.Args)
	}()
}

func (p *Peer) TopicJoinedState(e *am.Event) {
	p.mach.Remove1(ssp.TopicJoined, nil)
	go p.sim.topicPeerChange(e.Args["Topic.id"].(string), 1)
}

func (p *Peer) LeavingTopicEnter(e *am.Event) bool {
	topic := e.Args["Topic.id"].(string)
	return topic != ""
}

func (p *Peer) LeavingTopicState(e *am.Event) {
	p.mach.Remove1(ssp.LeavingTopic, nil)

	topic := e.Args["Topic.id"].(string)
	stateCtx := p.mach.NewStateCtx(ssp.LeavingTopic)

	if stateCtx.Err() != nil {
		return // expired
	}
	pTopic, ok := p.subs[topic]
	if !ok {
		p.mach.Log("Error: peer topic not found %s", topic)
		return
	}
	delete(p.subs, topic)

	// cancel readloop
	pTopic.cancel()

	// cancel subscription
	// TODO detect msgs awaiting to be received and adjust the counters, so they seem missing
	pTopic.psSub.Cancel()
	err := pTopic.psTopic.Close()
	if err != nil {
		p.mach.Log("Error: failed to close topic %s", err)
	}
	p.mach.Add1(ssp.TopicLeft, e.Args)
}

func (p *Peer) TopicLeftState(e *am.Event) {
	p.mach.Remove1(ssp.TopicLeft, nil)
	go p.sim.topicPeerChange(e.Args["Topic.id"].(string), -1)
}

func (p *Peer) SendingMsgsEnter(e *am.Event) bool {
	_, ok1 := e.Args["Topic.id"].(string)
	_, ok2 := e.Args["msgs"].([]string)
	_, ok3 := e.Args["token"].(string)

	return ok1 && ok2 && ok3
}

func (p *Peer) SendingMsgsState(e *am.Event) {
	p.mach.Remove1(ssp.SendingMsgs, nil)

	tid := e.Args["Topic.id"].(string)
	msgs := e.Args["msgs"].([]string)
	token := e.Args["token"].(string)

	topic := p.subs[tid]
	if topic == nil {
		p.mach.Log("Error: peer topic not found %s", tid)
		return
	}

	go func() {
		for _, msg := range msgs {
			err := topic.Publish(msg)
			if err != nil {
				p.mach.Log("Error: failed to publish message %s", err)
				return
			}
		}

		p.mach.Add1(ssp.MsgsSent, am.A{
			"Topic.id": tid,
			"token":    token,
			"amount":   len(msgs),
		})
	}()
}

func (p *Peer) MsgsSentState(e *am.Event) {
	p.mach.Remove1(ssp.MsgsSent, e.Args)
	p.sim.topicMsgs(e.Args["Topic.id"].(string), e.Args["amount"].(int))
}

func (p *Peer) storeMsg(cm *ChatMessage) {
	p.mach.Log("New msg: %s", cm.Message)
	p.msgs = append(p.msgs, cm.Message)

	if len(p.msgs) > maxMsgsPerPeer {
		p.msgs = p.msgs[len(p.msgs)-maxMsgsPerPeer:]
	}
}

// ///// ///// /////
// ///// PEER TOPIC
// ///// ///// /////

// ChatMessage gets converted to/from JSON and sent in the body of pubsub messages.
type ChatMessage struct {
	Message    string
	SenderID   string
	SenderNick string
}

// PeerTopic represents a subscription to a single PubSub topic. Messages
// can be published to the topic with PeerTopic.Publish, and received
// messages are pushed to the Messages channel.
type PeerTopic struct {
	ctx     context.Context
	cancel  context.CancelFunc
	peer    *Peer
	psTopic *pubsub.Topic
	psSub   *pubsub.Subscription

	topic string
	nick  string
}

// Publish sends a message to the pubsub topic.
func (pt *PeerTopic) Publish(message string) error {
	return pt.psTopic.Publish(pt.ctx, []byte(message))
}

func (pt *PeerTopic) ListPeers() []peer.ID {
	return pt.peer.ps.ListPeers(pt.topic)
}

// readLoop pulls messages from the pubsub chat topic and pushes them onto the Messages channel.
func (pt *PeerTopic) readLoop() {
	for pt.ctx.Err() == nil {

		msg, err := pt.psSub.Next(pt.ctx)
		if err != nil {
			pt.peer.mach.Log("Error: failed to read message %s", err)
			continue
		}

		// skip own msgs
		if msg.ReceivedFrom == pt.peer.host.ID() {
			continue
		}

		cm := new(ChatMessage)
		cm.Message = string(msg.Data)
		cm.SenderID = msg.ID
		cm.SenderNick = string(msg.ID[len(msg.ID)-8])

		// store
		pt.peer.storeMsg(cm)
	}

	pt.peer.mach.Log("exiting readLoop...")
}

// ///// ///// /////
// ///// IDENTITY
// ///// ///// /////

// TODO switch to `identity "github.com/libp2p/go-libp2p-relay-daemon"`

// LoadIdentity reads a private key from the given path and, if it does not
// exist, generates a new one.
func LoadIdentity(idPath string) (crypto.PrivKey, error) {

	if _, err := os.Stat(idPath); err == nil {

		return ReadIdentity(idPath)
	} else if os.IsNotExist(err) {
		fmt.Printf("Generating peer identity in %s\n", idPath)

		return GenerateIdentity(idPath)
	} else {

		return nil, err
	}
}

// ReadIdentity reads a private key from the given path.
func ReadIdentity(path string) (crypto.PrivKey, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return crypto.UnmarshalPrivateKey(bytes)
}

// GenerateIdentity writes a new random private key to the given path.
func GenerateIdentity(path string) (crypto.PrivKey, error) {
	privk, _, err := crypto.GenerateKeyPair(crypto.Ed25519, 0)
	if err != nil {
		return nil, err
	}

	bytes, err := crypto.MarshalPrivateKey(privk)
	if err != nil {
		return nil, err
	}

	err = os.WriteFile(path, bytes, 0400)

	return privk, err
}

// Borrowed from https://medium.com/rahasak/libp2p-pubsub-peer-discovery-with-kademlia-dht-c8b131550ac7

func randInt(min int, max int) int {
	return min + rand.Intn(max-min)
}
