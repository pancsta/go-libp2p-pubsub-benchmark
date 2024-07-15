package sim

import "time"

// TODO config file

var (
	// basic config

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

	maxHistoryEntries = 1000
	maxTopics         = 20
	maxPeers          = 30
	maxTopicPerPeer   = 15
	maxPeersPerTopic  = 10
	maxFriendsPerPeer = 10
	// buffer size
	maxMsgsPerPeer = 100

	// frequencies for random event states
	// smaller D (relative delay) happens more often

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

	// topic states

	topicActiveWindow    = 1 * time.Minute
	topicPeakingMinPeers = 2 * peakTopicPeers
)
