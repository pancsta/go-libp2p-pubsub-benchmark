package pubsub

import (
	"testing"

	mon "github.com/pancsta/go-libp2p-pubsub-benchmark/pkg/psmon"
)

func TestMain(m *testing.M) {
	mon.RunTestMain(m, psmon)
}
