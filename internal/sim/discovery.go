// Borrowed from github.com/libp2p/go-libp2p-pubsub/discovery_test.go

package sim

import (
	"context"
	"log"
	"sync"
	"time"

	ssp "github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim/states/peer"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/discovery"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	discoveryUtil "github.com/libp2p/go-libp2p/p2p/discovery/util"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
)

///////////////
///// Mock Discovery
///////////////

type mockDiscoveryServer struct {
	mx sync.Mutex
	db map[string]map[peer.ID]*discoveryRegistration
}

type discoveryRegistration struct {
	info peer.AddrInfo
	ttl  time.Duration
}

func newMockDiscoveryServer() *mockDiscoveryServer {
	return &mockDiscoveryServer{
		db: make(map[string]map[peer.ID]*discoveryRegistration),
	}
}

func (s *mockDiscoveryServer) Advertise(ns string, info peer.AddrInfo, ttl time.Duration) (time.Duration, error) {
	s.mx.Lock()
	defer s.mx.Unlock()

	peers, ok := s.db[ns]
	if !ok {
		peers = make(map[peer.ID]*discoveryRegistration)
		s.db[ns] = peers
	}
	peers[info.ID] = &discoveryRegistration{info, ttl}
	return ttl, nil
}

func (s *mockDiscoveryServer) FindPeers(ns string, limit int) (<-chan peer.AddrInfo, error) {
	s.mx.Lock()
	defer s.mx.Unlock()

	peers, ok := s.db[ns]
	if !ok || len(peers) == 0 {
		emptyCh := make(chan peer.AddrInfo)
		close(emptyCh)
		return emptyCh, nil
	}

	count := len(peers)
	if count > limit {
		count = limit
	}
	ch := make(chan peer.AddrInfo, count)
	numSent := 0
	for _, reg := range peers {
		if numSent == count {
			break
		}
		numSent++
		ch <- reg.info
	}
	close(ch)

	return ch, nil
}

func (s *mockDiscoveryServer) hasPeerRecord(ns string, pid peer.ID) bool {
	s.mx.Lock()
	defer s.mx.Unlock()

	if peers, ok := s.db[ns]; ok {
		_, ok := peers[pid]
		return ok
	}
	return false
}

type mockDiscoveryClient struct {
	host   host.Host
	server *mockDiscoveryServer
}

func (d *mockDiscoveryClient) Advertise(ctx context.Context, ns string, opts ...discovery.Option) (time.Duration, error) {
	var options discovery.Options
	err := options.Apply(opts...)
	if err != nil {
		return 0, err
	}

	return d.server.Advertise(ns, *host.InfoFromHost(d.host), options.Ttl)
}

func (d *mockDiscoveryClient) FindPeers(ctx context.Context, ns string, opts ...discovery.Option) (<-chan peer.AddrInfo, error) {
	var options discovery.Options
	err := options.Apply(opts...)
	if err != nil {
		return nil, err
	}

	return d.server.FindPeers(ns, options.Limit)
}

///////////////
///// DHT
///////////////

// NewDHT attempts to connect to a bunch of bootstrap peers and returns a new DHT.
// TODO fix initial delay
func NewDHT(mach *am.Machine, host host.Host, bootstrapPeers []peer.AddrInfo) (*dht.IpfsDHT, error) {
	var options []dht.Option

	// if no bootstrap peers give this peer act as a bootstrapping node
	// other peers can use this peers ipfs address for peer discovery via dht
	if mach.Is1(ssp.IsDHT) {
		options = append(options, dht.Mode(dht.ModeServer))
	}

	options = append(options, dht.RoutingTableRefreshPeriod(10*time.Second))

	options = append(options, dht.ProtocolPrefix("/"+DiscoveryServiceTag+"/lan"))

	kdht, err := dht.New(mach.Ctx, host, options...)
	if err != nil {
		return nil, err
	}

	if err = kdht.Bootstrap(mach.Ctx); err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	for _, peerinfo := range bootstrapPeers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := host.Connect(mach.Ctx, peerinfo); err != nil {
				mach.Log("Error while connecting to node %q: %-v", peerinfo, err)
			} else {
				mach.Log("Connection established with bootstrap node: %q", peerinfo)
			}
		}()
	}
	wg.Wait()

	return kdht, nil
}

// Borrowed from https://medium.com/rahasak/libp2p-pubsub-peer-discovery-with-kademlia-dht-c8b131550ac7
func DHTDiscover(mach *am.Machine, h host.Host, dht *dht.IpfsDHT, rendezvous string) {
	routingDiscovery := routing.NewRoutingDiscovery(dht)

	discoveryUtil.Advertise(mach.Ctx, routingDiscovery, rendezvous)

	ticker := time.NewTicker(discoveryFreq)
	defer ticker.Stop()

	for {
		select {

		case <-mach.Ctx.Done():
			return

		case <-ticker.C:

			peers, err := discoveryUtil.FindPeers(mach.Ctx, routingDiscovery, rendezvous)
			if err != nil {
				mach.Log("Error: discovery %s", err)
				continue
			}

			for _, p := range peers {
				if p.ID == h.ID() {
					continue
				}

				if h.Network().Connectedness(p.ID) != network.Connected {
					_, err = h.Network().DialPeer(mach.Ctx, p.ID)
					if err != nil {
						mach.Log("Error: Failed to connect to peer (%s): %s", p.ID, err.Error())
						continue
					}
					mach.Log("Connected to peer %s", p.ID)
				}
			}
		}
	}
}

///////////////
///// mDNS
///////////////

// setupDiscovery creates an mDNS discovery service and attaches it to the libp2p Host.
// This lets us automatically discover peers on the same LAN and connect to them.
func setupMDNSDiscovery(h host.Host) error {
	// setup mDNS discovery to find local peers
	s := mdns.NewMdnsService(h, DiscoveryServiceTag, &discoveryNotifee{h: h})

	return s.Start()
}

// discoveryNotifee gets notified when we find a new peer via mDNS discovery
type discoveryNotifee struct {
	h host.Host
}

// HandlePeerFound connects to peers discovered via mDNS. Once they're connected,
// the PubSub system will automatically start interacting with them if they also
// support PubSub.
func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	log.Printf("discovered new peer %s", pi.ID)
	err := n.h.Connect(context.Background(), pi)
	if err != nil {
		log.Printf("error connecting to peer %s: %s", pi.ID, err)
	}
}
