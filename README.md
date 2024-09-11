# libp2p-pubsub on asyncmachine

**libp2p-pubsub-benchmark** is a case study and stress test for [asyncmachine-go](http://github.com/pancsta/asyncmachine-go), it's telemetry and integrations. Although it's current purpose is pure research, it may develop over time into something usable.

> **asyncmachine-go** is a general purpose state machine for managing complex asynchronous workflows in a safe and structured way

## libp2p-pubsub benchmark

- ### [See results](bench.md)

| Jaeger                                   | am-dbg                                   |
|------------------------------------------|------------------------------------------|
| ![bench jaeger](assets/bench-jaeger.png) | ![bench am-dbg](assets/bench-am-dbg.png) |

| Test duration                                   | Max memory                                                                       |
|------------------------------------------|----------------------------------------------------------------------------------|
| ![Test duration chart](assets/charts/TestSimpleDiscovery/origin-states-time.png) | ![Max memory chart](assets/charts/TestSimpleDiscovery/origin-states-mem-max.png) |


`cmd/bench` compares the default [go-libp2p-pubsub](https://github.com/libp2p/go-libp2p-pubsub) implementation to the [asyncmachine version](https://github.com/pancsta/go-libp2p-pubsub/). It runs `TestSimpleDiscovery` for various host/msg configurations and presents a median for each iteration. The best way to view the results is [bench.md](bench.md), [bench.pdf](assets/bench.pdf?raw=true). Single runs can be viewed in Jaeger and am-dbg after `task test-discovery`. Benchmark uses go1.22 traces, thus needs at least this version.

Check [`assets/bench-jaeger-3h-10m.traces.json`](/assets/bench-jaeger-3h-10m.traces.json) for sample data.

State machines can be found in [go-libp2p-pubsub/states](https://github.com/pancsta/go-libp2p-pubsub/tree/psmon-states/states):

- **pubsub host** - eg `ps-17` (20 states)<br />
  PubSub state machine is a simple event loop with Multi states which get responses via arg channels. Heavy use of `Eval`.
- **discovery** - eg `ps-17-disc` (10 states)<br />
  Discovery state machine is a simple event loop with Multi states and a periodic refresh state.
- **discovery bootstrap** - eg `ps-17-disc-bf3` (5 states)<br />
  `BootstrapFlow` is a non-linear flow for topic bootstrapping with some retry logic.

<details>

<summary>See states structure and relations (pubsub host)</summary>

```go
package states

import am "github.com/pancsta/asyncmachine-go/pkg/machine"

// States define relations between states
var States = am.Struct{
    // peers
    PeersPending: {},
    PeersDead:    {},
    GetPeers:     {Multi: true},

    // peer
    PeerNewStream:   {Multi: true},
    PeerCloseStream: {Multi: true},
    PeerError:       {Multi: true},
    PublishMessage:  {Multi: true},
    BlacklistPeer:   {Multi: true},

    // topic
    GetTopics:       {Multi: true},
    AddTopic:        {Multi: true},
    RemoveTopic:     {Multi: true},
    AnnouncingTopic: {Multi: true},
    TopicAnnounced:  {Multi: true},

    // subscription
    RemoveSubscription: {Multi: true},
    AddSubscription:    {Multi: true},

    // misc
    AddRelay:        {Multi: true},
    RemoveRelay:     {Multi: true},
    IncomingRPC:     {Multi: true},
    AddValidator:    {Multi: true},
    RemoveValidator: {Multi: true},
}
```

</details>

<details>

<summary>See states structure and relations (discovery & bootstrap)</summary>

```go
package discovery

import am "github.com/pancsta/asyncmachine-go/pkg/machine"

// S is a type alias for a list of state names.
type S = am.S

// States define relations between states.
var States = am.Struct{
    Start: {
        Add: S{PoolTimer},
    },
    PoolTimer: {},
    RefreshingDiscoveries: {
        Require: S{Start},
    },
    DiscoveriesRefreshed: {
        Require: S{Start},
    },

    // topics

    DiscoveringTopic: {
        Multi: true,
    },
    TopicDiscovered: {
        Multi: true,
    },

    BootstrappingTopic: {
        Multi: true,
    },
    TopicBootstrapped: {
        Multi: true,
    },

    AdvertisingTopic: {
        Multi: true,
    },
    StopAdvertisingTopic: {
        Multi: true,
    },
}

// StatesBootstrapFlow define relations between states for the bootstrap flow.
var StatesBootstrapFlow = am.Struct{
    Start: {
        Add: S{BootstrapChecking},
    },
    BootstrapChecking: {
        Remove: BootstrapGroup,
    },
    DiscoveringTopic: {
        Remove: BootstrapGroup,
    },
    BootstrapDelay: {
        Remove: BootstrapGroup,
    },
    TopicBootstrapped: {
        Remove: BootstrapGroup,
    },
}

// Groups of mutually exclusive states.

var (
    BootstrapGroup = S{DiscoveringTopic, BootstrapDelay, BootstrapChecking, TopicBootstrapped}
)
```

</details>

Configuration file [bench.env](bench.env):

- `NUM_HOSTS` - number of hosts in the test
- `RAND_MSGS` - number of rand msgs to broadcast
- `PS_AM_DEBUG` - am-dbg telemetry for pubsub state machines
- `PS_AM_LOG_LEVEL` - AM logging level for pubsub state machines (0-4)
- `PS_AM_LOG_LEVEL_VERBOSE` - AM logging level for verbose pubsub state machines (0-4)
- `PS_TRACING_HOST` - pubsub otel tracing (native tracing)
- `PS_TRACING_AM` - pubsub otel tracing (states and transitions)

See [bench.md](bench.md) for detailed results.

### Benchmark diff

- [go-libp2p-pubsub benchmark integration](https://github.com/libp2p/go-libp2p-pubsub/compare/master...pancsta:go-libp2p-pubsub:psmon-origin)
  - adds psmon for otel
  - adds config

## libp2p-pubsub simulator

| Grafana                                | am-dbg                               |
|----------------------------------------|--------------------------------------|
| ![sim grafana](assets/sim-grafana.png) | ![sim am-dbg](assets/sim-am-dbg.png) |


`cmd/sim` starts a simulated pubsub network with a mocked discovery, creates random topics and makes peers join/leave them under various conditions, and also friend each other. The best way to view the simulator is in Grafana and am-dbg. Alternatively, `stdout` prints out basic metrics.

Check [`assets/am-dbg-sim.gob.br`](/assets/am-dbg-sim.gob.br) and [`assets/sim-grafana.dashboard.json`](/assets/sim-grafana.dashboard.json)
for samples.

State machines can be found in [internal/sim/states](internal/sim/states) and [go-libp2p-pubsub/states](https://github.com/pancsta/go-libp2p-pubsub/tree/psmon-states/states):

- **pubsub host** eg `ps-17` (20 states)<br />
  PubSub state machine is a simple event loop with Multi states which get responses via arg channels. Heavy use of `Eval`.
- **pubsub discovery** - eg `ps-17-disc` (10 states)<br />
  Discovery state machine is a simple event loop with Multi states and a periodic refresh state.
- **simulator** `sim` (14 states)<br />
  Root simulator state machine, initializes the network and manipulates it during heartbeats according to frequency definitions. Heavily dependent on state negotiation.
- **simulator's peer** - eg `sim-p17` (17 states)<br />
  Handles peer's connections, topics and messages. This state machine has a decent amount of relations. Each sim peer has its own pubsub host.
- **topics** - eg `sim-t-se7ev` (5 states)<br />
  State-only state machine (no handlers, no goroutine). States represent correlations with peer state machines.

<details>

<summary>See states structure and relations (simulator)</summary>

```go
package sim

import (
    am "github.com/pancsta/asyncmachine-go/pkg/machine"
)

// S is a type alias for a list of state names.
type S = am.S

// States map defines relations and properties of states.
var States = am.Struct{
    Start:     {},
    Heartbeat: {Require: S{Start}},

    // simulation

    AddPeer:       {Require: S{Start}},
    RemovePeer:    {Require: S{Start}},
    AddTopic:      {Require: S{Start}},
    RemoveTopic:   {Require: S{Start}},
    PeakRandTopic: {Require: S{Start}},

    // peer (nested) states

    AddRandomFriend:  {Require: S{Start}},
    GC:               {Require: S{Start}},
    JoinRandomTopic:  {Require: S{Start}},
    JoinFriendsTopic: {Require: S{Start}},
    MsgRandomTopic:   {Require: S{Start}},
    VerifyMsgsRecv:   {Require: S{Start}},

    // metrics

    RefreshMetrics: {Require: S{Start}},
    // TODO history-based metrics, via pairs of counters, possibly using peer histories as well
}
```

</details>

<details>

<summary>See states structure and relations (simulator's peer)</summary>

```go
package peer

import (
    am "github.com/pancsta/asyncmachine-go/pkg/machine"
)

// S is a type alias for a list of state names.
type S = am.S

// States map defines relations and properties of states.
var States = am.Struct{
    // Start (sync)
    Start: {},

    // DHT (sync)
    IsDHT: {},

    // Ready (sync auto)
    Ready: {
        Auto:    true,
        Require: S{Start, Connected},
    },

    // IdentityReady (async auto)
    IdentityReady: {Remove: groupIdentityReady},
    GenIdentity: {
        Auto:   true,
        Remove: groupIdentityReady,
    },

    BootstrapsConnected: {},

    // EventHostConnected (sync, external event)
    EventHostConnected: {
        Multi:   true,
        Require: S{Start},
        Add:     S{Connected},
    },

    // Connected (async bool auto)
    Connected: {
        Require: S{Start},
        Remove:  groupConnected,
    },
    Connecting: {
        Auto:    true,
        Require: S{Start, IdentityReady},
        Remove:  groupConnected,
    },
    Disconnecting: {
        Remove: am.SMerge(groupConnected, S{BootstrapsConnected}),
    },

    // TopicJoined (async)
    JoiningTopic: {
        Multi:   true,
        Require: S{Connected},
    },
    TopicJoined: {
        Multi:   true,
        Require: S{Connected},
        Add:     S{FwdToSim},
    },

    // TopicLeft (async)
    LeavingTopic: {
        Multi:   true,
        Require: S{Connected},
    },
    TopicLeft: {
        Multi:   true,
        Require: S{Connected},
        Add:     S{FwdToSim},
    },

    // MsgsSent (async)
    SendingMsgs: {
        Multi:   true,
        Require: S{Connected},
    },
    MsgsSent: {
        Multi:   true,
        Require: S{Connected},
        Add:     S{FwdToSim},
    },

    // call the mirror state in the main Sim machine, prefixed with Peer and peer ID added to Args
    // TODO
    FwdToSim: {},
}

//#region boilerplate defs

// Groups of mutually exclusive states.
var (
    groupConnected = S{Connecting, Connected, Disconnecting}
    //groupStarted       = S{Starting, Started}
    groupIdentityReady = S{GenIdentity, IdentityReady}
)
```

</details>

<details>

<summary>See states structure and relations (topic)</summary>

```go

package topic

import am "github.com/pancsta/asyncmachine-go/pkg/machine"

type S = am.S

// States map defines relations and properties of states.
var States = am.Struct{
    Stale: {
        Auto: true,
    },
    HasPeers: {},
    Active: {
        Remove: S{Stale},
    },
    Peaking: {
        Remove:  S{Stale},
        Require: S{HasPeers},
    },
}

```

</details>

Configuration file [sim.env](sim.env):

- `SIM_DURATION` - duration of the simulation
- `SIM_MAX_PEERS` - maximum number of peers
- `SIM_METRICS` - grafana metrics for sim and peer state machines
- `SIM_AM_DEBUG` - am-dbg telemetry for sim state machines
- `SIM_AM_LOG_LEVEL` - AM logging level for sim state machines (0-4)
- `PS_AM_DEBUG` - am-dbg telemetry for pubsub state machines
- `PS_AM_LOG_LEVEL` - AM logging level for pubsub state machines (0-4)

You can further customize the simulator in [internal/sim/sim.go](internal/sim/sim.go#L46).

### Simulator diff

- [go-libp2p-pubsub asyncmachine-go rewrite](https://github.com/libp2p/go-libp2p-pubsub/compare/master...pancsta:go-libp2p-pubsub:psmon-states)
- [pubsub state machines](https://github.com/pancsta/go-libp2p-pubsub/tree/psmon-states/states)

Main changes are in the following files:

- pubsub.go
- comm.go
- gossipsub.go
- states files

## Video Walkthrough

Walkthrough video showing metrics, debugging, and traces of both the simulator and benchmark. Text-only screencast.

[![Video Walkthrough](https://pancsta.github.io/assets/asyncmachine-go/pubsub-sim-demo1.png)](https://pancsta.github.io/assets/asyncmachine-go/pubsub-sim-demo1.m4v)

## Live Demo

Play with a live debugging session - interactively use the TUI debugger with data pre-generated by libp2p-pubsub-simulator in:

- web browser: [http://188.166.101.108:8080/wetty/ssh](http://188.166.101.108:8080/wetty/ssh/am-dbg?pass=am-dbg:8080/wetty/ssh/am-dbg?pass=am-dbg)
- terminal: `ssh 188.166.101.108 -p 4444`

## Running

Common setup for all the scenarios:

- `git clone https://github.com/pancsta/go-libp2p-pubsub-benchmark.git`
- `cd go-libp2p-pubsub-benchmark`
- `git checkout tags/v0.1.2` (latest release)
- `scripts/dep-taskfile.sh`
- `task install-deps`

### Benchmark Results

- run `task init-bench-repos`
- run `task bench-all` (~30min)
- open `./bench.md` for results

### Benchmark Traces

- run `task start-env`
- run `task start-am-dbg`
- switch TTY
- run `task test-discovery-states`
- visit the first TTY for am-dbg history
  - requires `PS_AM_DEBUG=1`
- visit http://localhost:16686/ for Jaeger
  - requires `PS_TRACING_HOST=1` or `PS_TRACING_AM=1|2`

### Simulator

- run `task start-env`
- run `task start-am-dbg`
- set up Grafana
  - visit http://localhost:3000/connections/datasources/new
    - login admin/admin, skip
    - add a Prometheus data source http://prometheus:9090
  - visit http://localhost:3000/dashboard/import
    - login admin/admin, skip
    - paste import [`assets/sim-grafana.dashboard.json`](https://raw.githubusercontent.com/pancsta/go-libp2p-pubsub-benchmark/main/assets/sim-grafana.dashboard.json)
      - or `task gen-grafana-dashboard` and provide machine IDs via `GRAFANA_IDS`
- switch TTY
- run `task start-sim`
- visit the first TTY for am-dbg history
  - requires `SIM_AM_DEBUG=1`
- visit the Grafana dashboard
  - requires `SIM_METRICS=1`

## Benchmarking a custom implementation

- add your repo to Taskfile.yml
  - to `init-bench-repos-clone`
  - to `init-bench-repos-setup`
  - to `inject-psmon`
- add an entry to `internal/bench/bench.go/AllVersions`
- init the env
  - run `task clean-bench-repos`
  - run `task init-bench-repos`
  - run `task inject-psmon`
- run `task bench-all`
  - or a specific benchmark step and versions
  - eg `go run ./cmd/bench gen-traces custom,states`
- check `assets` for resulting charts

## TODO

- update to upstream asyncmachine
- replace `sim.env` and `internal/sim/config.go` with `sim.yml`
  - allow for env overrides
- abstract discovery
  - fix DHT lag
  - enable mDNS
- reimplement more things as state machines
  - gossipsub, score, topic
- fix benchmark failures
- fix pubsub "Topic not found" err
- replace `bench.env` and all the benchmark repo defs with `bench.yml`
  - allow for env overrides
- bind [universal connectivity UI](https://github.com/libp2p/universal-connectivity/tree/main/go-peer) to see the traffic
  - needs a list of topics, doesn't need a msg form
- subtract `time.Sleep()` from benchmark results
- "delivered/missed msgs" metrics should also be averaged, not just counters
