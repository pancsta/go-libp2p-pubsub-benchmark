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

// Names of all the states (pkg enum).

const (
	// Stale means no recent msgs
	Stale = "Stale"
	// HasPeers means there are peers
	HasPeers = "HasPeers"
	// Active means there are recent msgs
	Active = "Active"
	// Peaking means there are twice more peers than in
	Peaking = "Peaking"
)

// Names is an ordered list of all the state names.
var Names = S{Stale, HasPeers, Active, Peaking, am.Exception}
