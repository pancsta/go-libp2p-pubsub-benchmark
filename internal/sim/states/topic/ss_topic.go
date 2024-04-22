package sim

import am "github.com/pancsta/asyncmachine-go/pkg/machine"

// States map defines relations and properties of states.
var States = am.Struct{
	Stale: {
		Auto: true,
	},
	HasPeers: {},
	Active:   {},
	Peaking:  {},
}

// Names of all the states (pkg enum).

const (
	Stale    = "Stale"
	HasPeers = "HasPeers"
	Active   = "Active"
	Peaking  = "Peaking"
)

// Names is an ordered list of all the state names.
var Names = am.S{Stale, HasPeers, Active, Peaking, am.Exception}
