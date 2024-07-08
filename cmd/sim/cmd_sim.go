package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim"
	ss "github.com/pancsta/go-libp2p-pubsub-benchmark/internal/sim/states/sim"

	"github.com/joho/godotenv"
	"os/signal"
	"syscall"
)

func main() {
	// load .env
	_ = godotenv.Load("sim.env", ".env")

	// init the simulator
	ctx := context.Background()
	// TODO pass the log level here
	s, err := sim.NewSim(ctx, isMetrics(), isAMDebug())
	if err != nil {
		log.Fatal(err)
	}

	// parse max peers
	maxPeers := 30
	if os.Getenv("SIM_MAX_PEERS") != "" {
		v, err := strconv.Atoi(os.Getenv("SIM_MAX_PEERS"))
		if err == nil {
			maxPeers = v
		}
	}
	s.MaxPeers = maxPeers

	// parse duration
	duration := 5 * time.Minute
	if os.Getenv("SIM_DURATION") != "" {
		v, err := time.ParseDuration(os.Getenv("SIM_DURATION"))
		if err == nil {
			duration = v
		}
	}

	fmt.Printf("Starting for %dmin...\n", int(duration.Minutes()))

	// start and wait X time
	s.Mach.Add1(ss.Start, nil)
	select {
	case <-time.After(time.Second):
		fmt.Printf("Cant start, check log\n")
		os.Exit(1)
	case <-s.Mach.When1(ss.Start, nil):
	}

	// create a channel to receive OS signals.
	osExit := make(chan os.Signal, 1)
	signal.Notify(osExit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-osExit:
	case <-time.After(duration):
	case <-s.Mach.WhenErr(nil):
	case <-s.Mach.WhenNot1(ss.Start, nil):
	}

	// the end
	println("the end")

	// wait for everything to finish
	s.Mach.Remove1(ss.Start, nil)
	s.Mach.Dispose()
	<-s.Mach.WhenDisposed()
}

func isMetrics() bool {
	v := os.Getenv("SIM_METRICS")
	return v != "" && v != "0"
}

func isTracing() bool {
	v := os.Getenv("SIM_TRACING")
	return v != "" && v != "0"
}

func tracingLimit() int {
	if !isTracing() {
		return -1
	}
	if os.Getenv("SIM_TRACING_LIMIT") == "" {
		return 99
	}

	v, err := strconv.Atoi(os.Getenv("SIM_TRACING_LIMIT"))
	if err != nil {
		return 99
	}
	return v
}

func isAMDebug() bool {
	v := os.Getenv("SIM_AM_DEBUG")
	return v != "" && v != "0"
}
