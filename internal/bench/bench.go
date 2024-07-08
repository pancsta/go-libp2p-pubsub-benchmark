package bench

import (
	"encoding/gob"
	"fmt"
	"image/color"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sourcegraph/conc/pool"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/trace"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
)

type Results struct {
	Time              time.Duration
	CreatedGoroutines int
	ActiveGoroutines  int
	AllocatedHeap     uint64
	MaxHeap           uint64
	LastHeap          uint64
	GCed              uint64
}

type TestCombo struct {
	Hosts int
	Msgs  int
	Iter  int
}

const (
	versionOrigin    = "origin"
	versionStates    = "states"
	versionStatesRPC = "states-rpc"
)

var (
	AllVersions = map[string]string{
		versionOrigin: "origin",
		versionStates: "states",
		// versionStatesRPC: "states-rpc",
	}
	benchmarkDir string
	startTime    = time.Now()
	testTimeout  = 3 * time.Second
)

const (
	testHostsStart = 5
	testHostsEnd   = 20
	testHostsStep  = 5
	testMsgsEnd    = 100
	testMsgsStart  = 20
	testMsgsStep   = 20
	testIters      = 15
	chartDefSizeCm = 16
	MB             = 1024 * 1024
)

type Chart struct {
	Title       string
	XLabel      string
	YLabel      string
	Filename    string
	Getter      func(*Results) (int, error)
	ShouldParse func(ver string, combo TestCombo) bool
	Result      func([]int) int
	Decor       func(chart *plot.Plot, maxVal int)
}

func isDebug() bool {
	return os.Getenv("PSB_DEBUG") != ""
}

func init() {

	// set benchmarkDir to the current working directory
	var err error
	benchmarkDir, err = os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	// check if version dirs exist
	for _, dir := range AllVersions {
		versionsDir := path.Join(benchmarkDir, "data", "go-libp2p-pubsub-repos", dir)
		if info, err := os.Stat(versionsDir); os.IsNotExist(err) || !info.IsDir() {
			panic("Missing versions directory, run task init-env")
		}
	}
}

func VersionsFromArgs() []string {

	// read traces from os.Args[2]
	if len(os.Args) < 3 {
		available := strings.Join(maps.Keys(AllVersions), ",")
		log.Fatal("Missing versions to test: " + available)
	}

	versions := strings.Split(os.Args[2], ",")
	for _, ver := range versions {
		if _, ok := AllVersions[ver]; !ok {
			log.Fatalf("Unknown version: %s", ver)
		}
	}

	return versions
}

func CmdGenTraces(testName string, versions []string) {
	log.Println("Starting libp2p-pubsub benchmark")
	if isDebug() {
		log.Println("Versions: " + strings.Join(versions, ","))
	}

	// re-create dir "{benchmarkDir}/traces"
	// join path
	tracesDir := path.Join(benchmarkDir, "data", "traces", testName)
	err := os.RemoveAll(tracesDir)
	if err != nil {
		log.Fatal(err)
	}
	err = os.MkdirAll(tracesDir, 0755)
	if err != nil && !os.IsExist(err) {
		log.Fatal(err)
	}

	completed := 0
	warmedUps := 10

	log.Println("Test: " + testName)
	for _, ver := range versions {

		// create dir "{benchmarkDir}/traces/VER"
		err := os.Mkdir(path.Join(tracesDir, ver), 0755)
		if err != nil && !os.IsExist(err) {
			log.Fatal(err)
		}

		todo := getAllTestCombos()

		log.Printf("Warming up %s: %d times\n", ver, warmedUps)

		// warm up the tester
		for i := 0; i < warmedUps; i++ {
			if err := RunDiscoveryTest(ver, todo[i]); err != nil {
				log.Println(err)
			}
		}

		log.Printf("Starting %d tests for '%s'\n", len(todo), ver)

		for _, combo := range getAllTestCombos() {
			log.Printf("Testing #%s for %v", ver, combo)
			if err := RunDiscoveryTest(ver, combo); err != nil {
				log.Println(err)
			}

			time.Sleep(10 * time.Millisecond)
			completed++
			if completed%100 == 0 {
				log.Printf("Completed %d tests", completed)
				log.Printf("Took %d secs", time.Since(startTime)/time.Second)
			}
		}
	}

	// TODO trace gen summary
}

func ResultsToText(res *Results) string {
	ret := ""
	p := message.NewPrinter(language.English)
	ret += p.Sprintf("Time: %v\n", res.Time)
	ret += p.Sprintf("Created goroutines: %d\n", res.CreatedGoroutines)
	ret += p.Sprintf("Final goroutines: %d\n", res.ActiveGoroutines)
	ret += p.Sprintf("Total allocated heap: %v bytes\n", number.Decimal(res.ActiveGoroutines))
	ret += p.Sprintf("Max heap: %v bytes\n", number.Decimal(res.MaxHeap))
	ret += p.Sprintf("Final heap: %v bytes\n", number.Decimal(res.LastHeap))
	ret += p.Sprintf("GCed: %v bytes\n", number.Decimal(res.GCed))

	return ret
}

// ///// ///// /////
// ///// CHARTS
// ///// ///// /////

const labelMsgs = "Num. of sent messages (per host)"

var Charts = []Chart{
	{
		Title:    fmt.Sprintf("Failure rate (origin; %d iterations)", testIters),
		XLabel:   labelMsgs,
		YLabel:   "Num. of failed runs",
		Filename: "failures-origin",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 1, nil
			}
			return 0, nil
		},
		Result: func(values []int) int {
			ret := 0
			for _, v := range values {
				if v != 0 {
					ret += v
				}
			}
			return ret
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Max = float64(max) * 1.3
			chart.Y.Min = 0
			chart.Y.Max = testIters
			chart.Legend.Top = true
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return ver == versionOrigin
		},
	},
	{
		Title:    fmt.Sprintf("Failure rate (states; %d iterations)", testIters),
		XLabel:   labelMsgs,
		YLabel:   "Num. of failed runs",
		Filename: "failures-states",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 1, nil
			}
			return 0, nil
		},
		Result: func(values []int) int {
			ret := 0
			for _, v := range values {
				if v != 0 {
					ret += v
				}
			}
			return ret
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Max = float64(max) * 1.3
			chart.Y.Min = 0
			chart.Y.Max = testIters
			chart.Legend.Top = true
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return ver == versionStates
		},
	},
	{
		Title:    "Test duration (less is better)",
		XLabel:   labelMsgs,
		YLabel:   "Time (ms)",
		Filename: "time",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 0, fmt.Errorf("no results")
			}
			return int(results.Time.Milliseconds()), nil
		},
		Result: func(values []int) int {
			return int(median(values))
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Max = float64(max) * 1.3
			chart.Y.Min = 0
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return true
		},
	},
	{
		Title:    "Created goroutines (less is better)",
		XLabel:   labelMsgs,
		YLabel:   "Number of goroutines (k)",
		Filename: "go-created",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 0, fmt.Errorf("no results")
			}
			return results.CreatedGoroutines / 1000, nil
		},
		Result: func(values []int) int {
			return int(median(values))
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Max = float64(max) * 1.3
			chart.Y.Min = 0
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return true
		},
	},
	{
		Title:    "Final goroutines (less is better)",
		XLabel:   labelMsgs,
		YLabel:   "Number of goroutines",
		Filename: "go-final",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 0, fmt.Errorf("no results")
			}
			return results.ActiveGoroutines, nil
		},
		Result: func(values []int) int {
			return int(median(values))
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Max = float64(max) * 1.3
			chart.Y.Min = 0
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return true
		},
	},
	{
		Title:    "Allocated memory (less is better)",
		XLabel:   labelMsgs,
		YLabel:   "MBs",
		Filename: "mem-alloc",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 0, fmt.Errorf("no results")
			}
			return int(results.AllocatedHeap / MB), nil
		},
		Result: func(values []int) int {
			return int(median(values))
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Min = 0
			chart.Y.Max = 210
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return true
		},
	},
	{
		Title:    "Memory ceiling (less is better)",
		XLabel:   labelMsgs,
		YLabel:   "MBs",
		Filename: "mem-max",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 0, fmt.Errorf("no results")
			}
			return int(results.MaxHeap / MB), nil
		},
		Result: func(values []int) int {
			return int(median(values))
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Max = float64(max) * 1.3
			chart.Y.Min = 0
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return true
		},
	},
	{
		Title:    "Final memory (less is better)",
		XLabel:   labelMsgs,
		YLabel:   "MBs",
		Filename: "mem-final",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 0, fmt.Errorf("no results")
			}
			return int(results.LastHeap / MB), nil
		},
		Result: func(values []int) int {
			return int(median(values))
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Max = float64(max) * 1.3
			chart.Y.Min = 0
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return true
		},
	},
	{
		Title:    "GCed memory",
		XLabel:   labelMsgs,
		YLabel:   "MBs",
		Filename: "mem-gc",
		Getter: func(results *Results) (int, error) {
			if results == nil {
				return 0, fmt.Errorf("no results")
			}
			return int(results.GCed / MB), nil
		},
		Result: func(values []int) int {
			return int(median(values))
		},
		Decor: func(chart *plot.Plot, max int) {
			chart.Y.Max = float64(max) * 1.3
			chart.Y.Min = 0
			chart.X.Tick.Marker = plot.ConstantTicks(getMessagesChartTicks())
		},
		ShouldParse: func(ver string, combo TestCombo) bool {
			return true
		},
	},
}

func getMessagesChartTicks() []plot.Tick {
	ret := []plot.Tick{}
	for _, combo := range getAllMsgsCombos() {
		ret = append(ret, plot.Tick{
			Value: float64(combo.Msgs),
			Label: fmt.Sprintf("%d", combo.Msgs),
		})
	}
	return ret
}

func CmdGenCharts(testName string, versions []string) {
	log.Println("Generating charts...")

	// re-create dir "{benchmarkDir}/assets/charts/{testName}"
	chartsDir := path.Join(benchmarkDir, "assets", "charts", testName)
	err := os.RemoveAll(chartsDir)
	if err != nil {
		log.Fatal(err)
	}
	err = os.MkdirAll(chartsDir, 0755)
	if err != nil && !os.IsExist(err) {
		log.Fatal(err)
	}

	for _, chart := range Charts {
		renderChart(chart, testName, versions, chartsDir)
	}
}

func renderChart(chart Chart, testName string, versions []string, chartsDir string) {

	// Time chart
	p := plot.New()
	p.Title.Text = chart.Title
	p.Y.Label.Text = chart.YLabel
	p.X.Label.Text = chart.XLabel
	p.Legend.Top = false
	// pTime.Legend.TextStyle.XAlign = text.XLeft
	// map[ver]map[hosts]XYs
	verPoints := map[string]map[int]plotter.XYs{}
	maxVal := 0

	for _, ver := range versions {
		verPoints[ver] = map[int]plotter.XYs{}
		var prevCombo TestCombo
		var iterVals []int

		for _, combo := range getAllTestCombos() {

			if !chart.ShouldParse(ver, combo) {
				continue
			}

			// new point
			if prevCombo.Iter > combo.Iter {
				if len(iterVals) < 2 {
					continue
				}
				updateVerPoints(verPoints, ver, prevCombo, chart.Result(iterVals))
				if result := chart.Result(iterVals); result > maxVal {
					maxVal = result
				}
				iterVals = []int{}
			}

			tracePath := getTraceFilePath(testName, ver, combo, false)
			res, err := getResultsFromFile(tracePath)
			if err != nil && isDebug() {
				log.Println(err)
			}

			val, err := chart.Getter(res)
			if err == nil {
				iterVals = append(iterVals, val)
			} else if isDebug() {
				log.Println(err)
			}
			prevCombo = combo
		}
		if len(iterVals) > 1 {
			result := chart.Result(iterVals)
			if result > maxVal {
				maxVal = result
			}
			updateVerPoints(verPoints, ver, prevCombo, result)
		}
	}

	if chart.Decor != nil {
		chart.Decor(p, maxVal)
	}

	for _, ver := range versions {
		for _, combo := range getAllHostsCombos() {
			err := addLine(ver, p, combo.Hosts, verPoints[ver][combo.Hosts])
			if err != nil {
				log.Fatal(err)
			}
		}
	}

	width := chartDefSizeCm * vg.Centimeter
	filename := fmt.Sprintf("%s-%s.png", getChartPrefix(versions), chart.Filename)
	err := p.Save(width, width/2, path.Join(chartsDir, filename))

	if err != nil {
		log.Fatal(err)
	}
	if isDebug() {
		log.Println("Time chart saved as " + filename)
	}
}

func getChartPrefix(versions []string) string {
	sort.Strings(versions)
	return strings.Join(versions, "-")
}

func updateVerPoints(
	verPoints map[string]map[int]plotter.XYs, ver string, combo TestCombo, yValue int,
) {
	if verPoints[ver][combo.Hosts] == nil {
		verPoints[ver][combo.Hosts] = plotter.XYs{}
	}
	verPoints[ver][combo.Hosts] = append(verPoints[ver][combo.Hosts],
		plotter.XY{
			X: float64(combo.Msgs),
			Y: float64(yValue),
		})
}

func getResultsFromFile(tracePath string) (*Results, error) {
	resPath := tracePath + ".gob"
	rf, err := os.Open(resPath)
	if err != nil {
		return nil, err
	}
	decoder := gob.NewDecoder(rf)
	var res *Results
	err = decoder.Decode(&res)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func median(nums []int) float64 {
	sort.Ints(nums)
	n := len(nums)
	m := n / 2
	if n%2 == 0 {
		return float64(nums[m-1]+nums[m]) / 2
	}
	return float64(nums[m])
}

func addLine(version string, p *plot.Plot, hosts int, xys plotter.XYs) error {
	line, err := plotter.NewLine(xys)
	if err != nil {
		return err
	}

	switch version {

	case versionOrigin:
		line.LineStyle.Width = vg.Points(1)
		line.LineStyle.Color = color.RGBA{
			R: 255,
			G: uint8(255 - hosts*10),
			B: uint8(255 - hosts*10),
			A: 255,
		}
		// line.LineStyle.Dashes = []vg.Length{vg.Points(5), vg.Points(5)}

	case versionStates:
		line.LineStyle.Width = vg.Points(1)
		line.LineStyle.Color = color.RGBA{
			R: uint8(255 - hosts*10),
			G: uint8(255 - hosts*10),
			B: 255,
			A: 255,
		}
		// line.LineStyle.Dashes = []vg.Length{vg.Points(5), vg.Points(5)}
	}

	p.Add(line)
	p.Legend.Add(fmt.Sprintf("%02d-hosts-%s", hosts, version), line)

	return nil
}

// ///// ///// /////
// ///// TESTS
// ///// ///// /////

func CmdParseResults(testName string, versions []string) {

	log.Println("Parsing results...")
	// wait group for all cores
	p := pool.New().WithMaxGoroutines(runtime.NumCPU())
	if isDebug() {
		log.Println("Concurrency:", runtime.NumCPU())
	}

	for _, ver := range versions {

		for _, combo := range getAllTestCombos() {

			p.Go(func() {
				tracePath := getTraceFilePath(testName, ver, combo, false)

				if isDebug() {
					log.Println(tracePath)
				}

				tf, err := os.Open(tracePath)
				if err != nil {
					log.Println("Missing traces file", tracePath)
					return
				}

				res, err := TracesToResults(tf)
				if err != nil {
					log.Println(err, tracePath)
					return
				}
				tf.Close()

				// store the result as GOB and TEXT
				gobPath := getTraceFilePath(testName, ver, combo, false) + ".gob"
				textPath := getTraceFilePath(testName, ver, combo, false) + ".txt"

				gobHan, err := os.Create(gobPath)
				if err != nil {
					log.Fatal(err)
				}

				textHan, err := os.Create(textPath)
				if err != nil {
					log.Fatal(err)
				}

				defer gobHan.Close()
				defer textHan.Close()

				// gob
				encoder := gob.NewEncoder(gobHan)
				err = encoder.Encode(res)
				if err != nil {
					log.Println(err)
				}

				// text
				_, err = textHan.Write([]byte(ResultsToText(res)))
				if err != nil {
					log.Println(err)
				}
			})
		}
	}

	p.Wait()
	log.Println("DONE")
}

// TODO generalize for other tests

func RunDiscoveryTest(version string, combo TestCombo) error {
	testName := "TestSimpleDiscovery"

	var err error
	shell := os.Getenv("SHELL")
	if len(shell) == 0 {
		shell = "sh"
	}

	tracePath := getTraceFilePath(testName, version, combo, false)
	cmdStr := fmt.Sprintf(`go test -test.run '^\Q%s\E$' -trace=%s`, testName, tracePath)

	if isDebug() {
		log.Println(cmdStr)
	}

	cmd := exec.Command(shell, "-c", cmdStr)
	cmd.Dir = path.Join(benchmarkDir, "data", "go-libp2p-pubsub-repos", AllVersions[version])
	cmd.Env = append(cmd.Env, fmt.Sprintf("NUM_HOSTS=%d", combo.Hosts))
	cmd.Env = append(cmd.Env, fmt.Sprintf("RAND_MSGS=%d", combo.Msgs))
	// disable tracing and am-dbg
	cmd.Env = append(cmd.Env, "PS_TRACING_HOST=0")
	cmd.Env = append(cmd.Env, "PS_TRACING_AM=0")
	cmd.Env = append(cmd.Env, "PS_AM_DEBUG=0")
	cmd.Env = append(cmd.Env, "PS_AM_LOG_LEVEL=0")
	cmd.Env = append(cmd.Env, "PS_AM_LOG_LEVEL_VERBOSE=0")

	// run with a timeout
	done := make(chan error)
	go func() {

		out, err := cmd.Output()
		if err != nil {
			done <- err
		}

		if isDebug() {
			log.Println(string(out))
		}

		pass := strings.Contains(string(out), "PASS")
		err = nil
		if !pass {
			err = fmt.Errorf("test failed")
		}

		done <- nil
	}()

	select {

	case <-time.After(testTimeout):
		cmd.Process.Kill()
		time.Sleep(100 * time.Millisecond)
		killProcess(cmd.Process.Pid)
		// remove the trace file
		os.Remove(tracePath)
		return fmt.Errorf("timeout")

	case err = <-done:
	}

	// check if file exists
	_, err = os.Stat(tracePath)
	if err != nil {
		return err
	}

	return nil
}

func killProcess(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	// Force kill the process
	return process.Signal(os.Kill)
}

func getAllTestCombos() []TestCombo {
	var ret []TestCombo

	for h := testHostsStart; h <= testHostsEnd; h += testHostsStep {
		for m := testMsgsStart; m <= testMsgsEnd; m += testMsgsStep {
			for i := 1; i < testIters+1; i++ {
				ret = append(ret, TestCombo{h, m, i})
			}
		}
	}

	return ret
}

func getAllHostsCombos() []TestCombo {
	var ret []TestCombo

	for h := testHostsStart; h <= testHostsEnd; h += testHostsStep {
		ret = append(ret, TestCombo{h, 0, 0})
	}

	return ret
}

func getAllMsgsCombos() []TestCombo {
	var ret []TestCombo

	for h := testHostsStart; h <= testHostsEnd; h += testHostsStep {
		for m := testMsgsStart; m <= testMsgsEnd; m += testMsgsStep {
			ret = append(ret, TestCombo{h, m, 0})
		}
	}

	return ret
}

func getTraceFilePath(testName string, version string, combo TestCombo, relative bool) string {
	filename := fmt.Sprintf("trace-%d-%d-%d.out", combo.Hosts, combo.Msgs, combo.Iter)
	rel := path.Join("data", "traces", testName, version, filename)

	if relative {
		return rel
	}
	return path.Join(benchmarkDir, rel)
}

func TracesToResults(traces io.Reader) (*Results, error) {
	r, err := trace.NewReader(traces)
	if err != nil {
		return nil, fmt.Errorf("failed to read trace: %w", err)
	}

	var (
		createdGoroutines        = 0
		activeGoroutines         = 0
		maxHeap           uint64 = 0
		allocatedHeap     uint64 = 0
		lastHeap          uint64 = 0
		gced              uint64 = 0
		startedAt         trace.Time
		lastEventAt       trace.Time
	)

	for {
		// Read the event.
		ev, err := r.ReadEvent()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		lastEventAt = ev.Time()
		if startedAt == 0 {
			startedAt = ev.Time()
		}

		if ev.Kind() == trace.EventStateTransition {
			st := ev.StateTransition()
			if st.Resource.Kind == trace.ResourceGoroutine {
				from, to := st.Goroutine()
				if from == trace.GoNotExist && to != trace.GoUndetermined {
					createdGoroutines++
					activeGoroutines++
				}
				if from != trace.GoNotExist && to == trace.GoNotExist {
					activeGoroutines--
				}
			}
		}

		if ev.Kind() == trace.EventMetric {
			m := ev.Metric()
			if m.Name == "/memory/classes/heap/objects:bytes" {
				heap := m.Value.Uint64()
				if lastHeap < heap {
					allocatedHeap += heap - lastHeap
				}
				if heap > maxHeap {
					maxHeap = heap
				}
				lastHeap = heap
			}

		}

		if ev.Kind() == trace.EventRangeEnd {
			attrs := ev.RangeAttributes()
			for _, attr := range attrs {
				if attr.Name == "bytes reclaimed" || attr.Name == "bytes swept" {
					gced += attr.Value.Uint64()
				}
			}
		}

	}

	// calculate the time from staret to last event
	timeTaken := lastEventAt.Sub(startedAt)

	return &Results{
		Time:              timeTaken,
		CreatedGoroutines: createdGoroutines,
		ActiveGoroutines:  activeGoroutines,
		AllocatedHeap:     allocatedHeap,
		MaxHeap:           maxHeap,
		LastHeap:          lastHeap,
		GCed:              gced,
	}, nil
}
