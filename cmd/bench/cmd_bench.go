// TODO move to internal/bench
// TODO test name prefix (support other tests)

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/pancsta/go-libp2p-pubsub-benchmark/internal/bench"
	"golang.org/x/exp/maps"
)

func main() {
	cmd := "help"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	// TODO take from the cmd line
	testName := "TestSimpleDiscovery"

	switch cmd {

	// read the trace file
	case "read-trace":

		// read traces from os.Args[2]
		if len(os.Args) < 3 {
			log.Fatal("Missing trace file")
		}
		traceFile := os.Args[2]

		// check if file exists
		_, err := os.Stat(traceFile)
		if err != nil {
			log.Fatal(err)
		}
		f, err := os.Open(traceFile)
		defer f.Close()
		if err != nil {
			log.Fatal(err)
		}
		res, err := bench.TracesToResults(f)
		if err != nil {
			log.Fatal(err)
		}

		// print the results
		fmt.Print(bench.ResultsToText(res))

	case "gen-traces":
		bench.CmdGenTraces(testName, bench.VersionsFromArgs())

	case "gen-results":
		bench.CmdParseResults(testName, bench.VersionsFromArgs())

	case "gen-charts":
		bench.CmdGenCharts(testName, bench.VersionsFromArgs())

	case "all":
		log.Println("Running the full benchmark")
		all := maps.Keys(bench.AllVersions)
		bench.CmdGenTraces(testName, all)
		bench.CmdParseResults(testName, all)
		bench.CmdGenCharts(testName, all)

	case "help":
		fmt.Println("Usage: bench [read-trace|gen-traces|gen-results|gen-charts|all]")
	}
}
