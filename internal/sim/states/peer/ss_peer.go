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

// #region boilerplate defs

// Groups of mutually exclusive states.
var (
	groupConnected = S{Connecting, Connected, Disconnecting}
	// groupStarted       = S{Starting, Started}
	groupIdentityReady = S{GenIdentity, IdentityReady}
)

// Names of all the states (pkg enum).

const (
	Start               = "Start"
	Ready               = "Ready"
	Connected           = "Connected"
	Connecting          = "Connecting"
	Disconnecting       = "Disconnecting"
	JoiningTopic        = "JoiningTopic"
	TopicJoined         = "TopicJoined"
	SendingMsgs         = "SendingMsgs"
	MsgsSent            = "MsgsSent"
	LeavingTopic        = "LeavingTopic"
	TopicLeft           = "TopicLeft"
	IdentityReady       = "IdentityReady"
	GenIdentity         = "GenIdentity"
	FwdToSim            = "FwdToSim"
	IsDHT               = "IsDHT"
	BootstrapsConnected = "BootstrapsConnected"
	EventHostConnected  = "EventHostConnected"
)

// Names is an ordered list of all the state names.
var Names = S{
	Start,
	IsDHT,
	Ready,
	IdentityReady,
	GenIdentity,
	BootstrapsConnected,
	EventHostConnected,
	Connected,
	Connecting,
	Disconnecting,
	JoiningTopic,
	TopicJoined,
	LeavingTopic,
	TopicLeft,
	SendingMsgs,
	MsgsSent,
	FwdToSim,
	am.Exception,
}

// #endregion
