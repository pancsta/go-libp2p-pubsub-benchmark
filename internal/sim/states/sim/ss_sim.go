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

//#region boilerplate defs

// Names of all the states (pkg enum).

const (
	Start     = "Start"
	Heartbeat = "Heartbeat"

	AddPeer          = "AddPeer"
	RemovePeer       = "RemovePeer"
	AddTopic         = "AddTopic"
	RemoveTopic      = "RemoveTopic"
	PeakRandTopic    = "PeakRandTopic"
	AddRandomFriend  = "AddRandomFriend"
	GC               = "GC"
	JoinRandomTopic  = "JoinRandomTopic"
	JoinFriendsTopic = "JoinFriendsTopic"
	MsgRandomTopic   = "MsgRandomTopic"
	VerifyMsgsRecv   = "VerifyMsgsRecv"

	RefreshMetrics = "RefreshMetrics"
)

// Names is an ordered list of all the state names.
var Names = S{
	Start,
	Heartbeat,
	AddPeer,
	RemovePeer,
	AddTopic,
	RemoveTopic,
	PeakRandTopic,
	AddRandomFriend,
	GC,
	JoinRandomTopic,
	JoinFriendsTopic,
	MsgRandomTopic,
	VerifyMsgsRecv,
	RefreshMetrics,
	am.Exception,
}

//#endregion
