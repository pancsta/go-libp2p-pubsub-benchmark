package sim

import (
	"github.com/pancsta/asyncmachine-go/pkg/history"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	sst "github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim/states/topic"
	"time"
)

type Topic struct {
	*am.ExceptionHandler

	sim     *Sim
	mach    *am.Machine
	history *history.History

	id         string
	peersCount int
	msgsLog    []TopicMsgsLog
}

type TopicMsgsLog struct {
	time   time.Time
	amount int
}

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

// IndexesToStates converts msg and peers indexes to semantic states.
func (t *Topic) IndexesToStates() {

	// HasPeers
	if t.peersCount == 0 {
		t.mach.Remove1(sst.HasPeers, nil)
	} else {
		t.mach.Add1(sst.HasPeers, nil)
	}

	// Peaking
	if t.peersCount >= topicPeakingMinPeers {
		t.mach.Add1(sst.Peaking, nil)
	} else {
		t.mach.Remove1(sst.Peaking, nil)
	}

	// Active
	t.disposeOldMsgs()
	if len(t.msgsLog) == 0 {
		t.mach.Remove1(sst.Active, nil)
	} else {
		t.mach.Add1(sst.Active, nil)
	}
}

func (t *Topic) disposeOldMsgs() {
	var msgs []TopicMsgsLog
	for _, msg := range t.msgsLog {
		if time.Since(msg.time) < topicActiveWindow {
			msgs = append(msgs, msg)
		}
	}
	t.msgsLog = msgs
}
