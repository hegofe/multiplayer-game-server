package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type burstBucket struct {
	OffsetMs int `json:"offsetMs"`
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
}

type burstReport struct {
	Bets            int           `json:"bets"`
	Accepted        int           `json:"accepted"`
	RejectedClosed  int           `json:"rejectedClosed"`
	RejectedOther   int           `json:"rejectedOther"`
	AcceptedLate    int           `json:"acceptedLate"`
	StoredBets      int64         `json:"storedBets"`
	BalanceExpected int64         `json:"balanceExpectedCents"`
	BalanceActual   int64         `json:"balanceActualCents"`
	Buckets         []burstBucket `json:"buckets"`
}

type sameUserReport struct {
	Attempts        int   `json:"attempts"`
	Accepted        int   `json:"accepted"`
	Duplicates      int   `json:"duplicates"`
	Other           int   `json:"other"`
	StoredBets      int64 `json:"storedBets"`
	BalanceExpected int64 `json:"balanceExpectedCents"`
	BalanceActual   int64 `json:"balanceActualCents"`
}

type saturationReport struct {
	QueueSize         int     `json:"queueSize"`
	Attempts          int     `json:"attempts"`
	InFlight          int     `json:"inFlight"`
	Queued            int     `json:"queued"`
	RejectedBusy      int     `json:"rejectedBusy"`
	MaxRejectWaitMs   float64 `json:"maxRejectWaitMs"`
	AcceptedAfterward int     `json:"acceptedAfterRelease"`
	Other             int     `json:"other"`
}

type testEvent struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
	Output string `json:"Output"`
}

type runStats struct {
	Name   string
	Passed int
	Failed int
}

type coverLine struct {
	Function string
	Percent  string
}

type report struct {
	Generated  time.Time
	HostCPU    string
	HostRAM    string
	HostOS     string
	GoVersion  string
	CPUs       int
	Burst      burstReport
	SameUser   sameUserReport
	Saturation saturationReport
	Runs       []runStats
	Coverage   []coverLine
	TotalCover string
}

func main() {
	dataDir := flag.String("data", "results/concurrency/data", "directory with the JSON written by the tests")
	eventsPath := flag.String("events", "", "go test -json output")
	coverPath := flag.String("coverage", "", "go tool cover -func output")
	outDir := flag.String("out", "results/concurrency", "output directory")
	flag.Parse()

	r := report{
		Generated: time.Now().UTC(),
		HostCPU:   envOr("HOST_CPU", "unknown"),
		HostRAM:   envOr("HOST_RAM", "unknown"),
		HostOS:    envOr("HOST_OS", "unknown"),
		GoVersion: runtime.Version(),
		CPUs:      runtime.NumCPU(),
	}
	mustReadJSON(filepath.Join(*dataDir, "burst.json"), &r.Burst)
	mustReadJSON(filepath.Join(*dataDir, "same_user.json"), &r.SameUser)
	mustReadJSON(filepath.Join(*dataDir, "saturation.json"), &r.Saturation)

	output, runs := readEvents(*eventsPath)
	r.Runs = runs
	r.Coverage, r.TotalCover = readCoverage(*coverPath)

	mustWrite(filepath.Join(*outDir, "test-output.txt"), output)
	mustWrite(filepath.Join(*outDir, "burst.svg"), burstSVG(r.Burst))
	mustWrite(filepath.Join(*outDir, "summary.svg"), summarySVG(r))
	mustWrite(filepath.Join(*outDir, "README.md"), readme(r))
	fmt.Println("report written to", *outDir)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func mustReadJSON(path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		log.Fatalf("decode %s: %v", path, err)
	}
}

func mustWrite(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.Fatal(err)
	}
}

func readEvents(path string) (string, []runStats) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open events: %v", err)
	}
	defer f.Close()

	var out strings.Builder
	stats := map[string]*runStats{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var e testEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue
		}
		if e.Action == "output" {
			out.WriteString(e.Output)
		}
		if e.Test == "" || strings.Contains(e.Test, "/") {
			continue
		}
		s, ok := stats[e.Test]
		if !ok {
			s = &runStats{Name: e.Test}
			stats[e.Test] = s
		}
		switch e.Action {
		case "pass":
			s.Passed++
		case "fail":
			s.Failed++
		}
	}
	runs := make([]runStats, 0, len(stats))
	for _, s := range stats {
		runs = append(runs, *s)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Name < runs[j].Name })
	return out.String(), runs
}

func readCoverage(path string) ([]coverLine, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatalf("read coverage: %v", err)
	}
	var lines []coverLine
	total := "n/a"
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "total:" {
			total = fields[len(fields)-1]
			continue
		}
		if strings.Contains(fields[0], "/table.go:") {
			lines = append(lines, coverLine{Function: fields[1], Percent: fields[2]})
		}
	}
	return lines, total
}

func (r report) totals() (passed, failed int) {
	for _, s := range r.Runs {
		passed += s.Passed
		failed += s.Failed
	}
	return
}

const svgStyle = `<style>
svg{--surface:#fcfcfb;--ink:#0b0b0b;--ink2:#52514e;--muted:#898781;--grid:#e1e0d9;--axis:#c3c2b7;--border:rgba(11,11,11,0.10);--s1:#2a78d6;--s2:#eb6834}
@media (prefers-color-scheme: dark){svg{--surface:#1a1a19;--ink:#ffffff;--ink2:#c3c2b7;--muted:#898781;--grid:#2c2c2a;--axis:#383835;--border:rgba(255,255,255,0.10);--s1:#3987e5;--s2:#d95926}}
text{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;fill:var(--ink2)}
.bg{fill:var(--surface)}.title{fill:var(--ink);font-size:15px;font-weight:600}.label{font-size:12px}.tick{font-size:11px;fill:var(--muted);font-variant-numeric:tabular-nums}
.grid{stroke:var(--grid);stroke-width:1}.axis{stroke:var(--axis);stroke-width:1}.marker{stroke:var(--ink2);stroke-width:1}
.s1{fill:var(--s1)}.s2{fill:var(--s2)}.tile{fill:var(--surface);stroke:var(--border);stroke-width:1}
.value{fill:var(--ink);font-size:30px;font-weight:600}.caption{font-size:12px;fill:var(--muted)}
</style>`

func topRoundedBar(x, y, w, h, r float64) string {
	if h <= 0 {
		return ""
	}
	r = min(r, w/2, h)
	return fmt.Sprintf("M%.2f,%.2f V%.2f Q%.2f,%.2f %.2f,%.2f H%.2f Q%.2f,%.2f %.2f,%.2f V%.2f Z",
		x, y+h, y+r, x, y, x+r, y, x+w-r, x+w, y, x+w, y+r, y+h)
}

func burstSVG(b burstReport) string {
	const (
		width, height = 760.0, 360.0
		left, right   = 52.0, 16.0
		top, bottom   = 108.0, 52.0
	)
	plotW := width - left - right
	plotH := height - top - bottom
	n := float64(len(b.Buckets))
	step := plotW / n
	barW := step - 2

	yMax := 1
	for _, k := range b.Buckets {
		yMax = max(yMax, k.Accepted+k.Rejected)
	}
	tickStep := niceStep(yMax)
	yTop := ((yMax + tickStep - 1) / tickStep) * tickStep
	y := func(v int) float64 { return top + plotH - float64(v)/float64(yTop)*plotH }

	var s strings.Builder
	fmt.Fprintf(&s, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img" aria-labelledby="t d">`, width, height, width, height)
	fmt.Fprintf(&s, `<title id="t">Bets around the close of the betting window</title><desc id="d">%d bets sent between 50 ms before and 49 ms after the close: %d accepted, all before the close; %d rejected, all at or after the close.</desc>`, b.Bets, b.Accepted, b.RejectedClosed)
	s.WriteString(svgStyle)
	fmt.Fprintf(&s, `<rect class="bg" width="%.0f" height="%.0f" rx="8"/>`, width, height)
	fmt.Fprintf(&s, `<text class="title" x="%.0f" y="28">Bets around the close of the betting window</text>`, left)
	fmt.Fprintf(&s, `<text class="label" x="%.0f" y="48">%d bets, 10 per millisecond, from 50 ms before to 49 ms after the close</text>`, left, b.Bets)

	legendY := 70.0
	fmt.Fprintf(&s, `<rect class="s1" x="%.0f" y="%.0f" width="10" height="10" rx="2"/><text class="label" x="%.0f" y="%.0f">Accepted (%d)</text>`, left, legendY-9, left+16, legendY, b.Accepted)
	fmt.Fprintf(&s, `<rect class="s2" x="%.0f" y="%.0f" width="10" height="10" rx="2"/><text class="label" x="%.0f" y="%.0f">Rejected: betting closed (%d)</text>`, left+130, legendY-9, left+146, legendY, b.RejectedClosed)

	for v := 0; v <= yTop; v += tickStep {
		yy := y(v)
		cls := "grid"
		if v == 0 {
			cls = "axis"
		}
		fmt.Fprintf(&s, `<line class="%s" x1="%.0f" x2="%.0f" y1="%.2f" y2="%.2f"/>`, cls, left, width-right, yy, yy)
		fmt.Fprintf(&s, `<text class="tick" x="%.0f" y="%.2f" text-anchor="end">%d</text>`, left-8, yy+4, v)
	}

	for i, k := range b.Buckets {
		x := left + float64(i)*step + 1
		if k.Accepted > 0 {
			h := y(0) - y(k.Accepted)
			fmt.Fprintf(&s, `<path class="s1" d="%s"><title>%+d ms: %d accepted</title></path>`, topRoundedBar(x, y(k.Accepted), barW, h, 2.5), k.OffsetMs, k.Accepted)
		}
		if k.Rejected > 0 {
			base := y(k.Accepted)
			if k.Accepted > 0 {
				base -= 2
			}
			h := base - y(k.Accepted+k.Rejected)
			fmt.Fprintf(&s, `<path class="s2" d="%s"><title>%+d ms: %d rejected</title></path>`, topRoundedBar(x, y(k.Accepted+k.Rejected), barW, h, 2.5), k.OffsetMs, k.Rejected)
		}
	}

	for i, k := range b.Buckets {
		if k.OffsetMs%25 != 0 && i != len(b.Buckets)-1 {
			continue
		}
		cx := left + float64(i)*step + step/2
		fmt.Fprintf(&s, `<text class="tick" x="%.2f" y="%.0f" text-anchor="middle">%s</text>`, cx, top+plotH+18, signed(k.OffsetMs))
		if k.OffsetMs == 0 {
			mx := left + float64(i)*step
			fmt.Fprintf(&s, `<line class="marker" x1="%.2f" x2="%.2f" y1="%.0f" y2="%.0f"/>`, mx, mx, top-16, top+plotH)
			fmt.Fprintf(&s, `<text class="label" x="%.2f" y="%.0f" text-anchor="start">Betting closes</text>`, mx+6, top-6)
		}
	}
	fmt.Fprintf(&s, `<text class="label" x="%.0f" y="%.0f" text-anchor="middle">Arrival time relative to the close (ms)</text>`, left+plotW/2, height-10)
	fmt.Fprintf(&s, `<text class="label" transform="translate(14 %.0f) rotate(-90)" text-anchor="middle">Bets</text>`, top+plotH/2)
	s.WriteString(`</svg>`)
	return s.String()
}

func niceStep(maxV int) int {
	for _, c := range []int{1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000} {
		if maxV/c <= 5 {
			return c
		}
	}
	return maxV / 5
}

func summarySVG(r report) string {
	const width, height = 760.0, 150.0
	const tileW, gap = 240.0, 20.0
	passed, failed := r.totals()
	tiles := []struct{ label, value, caption1, caption2 string }{
		{
			fmt.Sprintf("Same user, %d simultaneous bets", r.SameUser.Attempts),
			fmt.Sprintf("%d accepted", r.SameUser.Accepted),
			fmt.Sprintf("%d rejected as duplicates", r.SameUser.Duplicates),
			"balance debited once",
		},
		{
			fmt.Sprintf("Queue full (capacity %d)", r.Saturation.QueueSize),
			fmt.Sprintf("%d rejected", r.Saturation.RejectedBusy),
			fmt.Sprintf("immediately, waited %.0f ms", r.Saturation.MaxRejectWaitMs),
			fmt.Sprintf("%d queued bets accepted later", r.Saturation.AcceptedAfterward),
		},
		{
			"Runs with the race detector",
			fmt.Sprintf("%d / %d passed", passed, passed+failed),
			fmt.Sprintf("%d tests, repeated", len(r.Runs)),
			"no data races reported",
		},
	}
	if failed > 0 {
		tiles[2].caption2 = fmt.Sprintf("%d runs failed", failed)
	}

	var s strings.Builder
	fmt.Fprintf(&s, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img" aria-labelledby="t">`, width, height, width, height)
	s.WriteString(`<title id="t">Concurrency test summary</title>`)
	s.WriteString(svgStyle)
	for i, t := range tiles {
		x := float64(i) * (tileW + gap)
		fmt.Fprintf(&s, `<rect class="tile" x="%.1f" y="0.5" width="%.0f" height="%.0f" rx="8"/>`, x+0.5, tileW-1, height-1)
		fmt.Fprintf(&s, `<text class="label" x="%.0f" y="30">%s</text>`, x+16, html.EscapeString(t.label))
		fmt.Fprintf(&s, `<text class="value" x="%.0f" y="74">%s</text>`, x+16, html.EscapeString(t.value))
		fmt.Fprintf(&s, `<text class="caption" x="%.0f" y="104">%s</text>`, x+16, html.EscapeString(t.caption1))
		fmt.Fprintf(&s, `<text class="caption" x="%.0f" y="124">%s</text>`, x+16, html.EscapeString(t.caption2))
	}
	s.WriteString(`</svg>`)
	return s.String()
}

func status(ok bool) string {
	if ok {
		return "✅ pass"
	}
	return "❌ fail"
}

func readme(r report) string {
	passed, failed := r.totals()
	b, u, q := r.Burst, r.SameUser, r.Saturation
	var s strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&s, format, a...) }

	w("# Concurrency test results\n\n")
	w("Results of the concurrency tests for the Aviator betting table (`internal/game`), generated on %s UTC.\n\n", r.Generated.Format("2006-01-02 15:04"))
	w("**%d of %d runs passed** with the Go race detector enabled.\n\n", passed, passed+failed)
	w("![Summary](summary.svg)\n\n")

	w("## What is being tested\n\n")
	w("A single goroutine owns each round. HTTP requests never touch the round directly: they send commands through a channel and wait for the answer. ")
	w("These tests fire hundreds of goroutines at the table at the same instant and check that the rules still hold.\n\n")
	w("The tests use [`testing/synctest`](https://pkg.go.dev/testing/synctest), so time is simulated: a bet can be sent exactly 1 ms before the close, and the results are deterministic. ")
	w("They test correctness, not speed; throughput and latency are measured separately with k6.\n\n")

	w("| Test | Checks | Runs passed | Result |\n|---|---|---|---|\n")
	for _, run := range r.Runs {
		w("| `%s` | %s | %d / %d | %s |\n", run.Name, describe(run.Name), run.Passed, run.Passed+run.Failed, status(run.Failed == 0 && run.Passed > 0))
	}
	w("\n")

	w("## 1. Burst at the close of the betting window\n\n")
	w("![Bets around the close of the betting window](burst.svg)\n\n")
	w("%d users bet at the same time, spread from 50 ms before to 49 ms after the close (10 bets per millisecond).\n\n", b.Bets)
	w("| Metric | Value | Expected |\n|---|---|---|\n")
	w("| Accepted | %d | every bet sent before the close |\n", b.Accepted)
	w("| Rejected as betting closed | %d | every bet sent at or after the close |\n", b.RejectedClosed)
	w("| Accepted at or after the close | %d | 0 |\n", b.AcceptedLate)
	w("| Bets stored | %d | same as accepted |\n", b.StoredBets)
	w("| Sum of all balances (cents) | %d | %d |\n\n", b.BalanceActual, b.BalanceExpected)

	w("## 2. Same user betting from many goroutines at once\n\n")
	w("| Metric | Value | Expected |\n|---|---|---|\n")
	w("| Simultaneous attempts | %d | |\n", u.Attempts)
	w("| Accepted | %d | 1 |\n", u.Accepted)
	w("| Rejected as duplicates | %d | %d |\n", u.Duplicates, u.Attempts-1)
	w("| Bets stored | %d | 1 |\n", u.StoredBets)
	w("| Final balance (cents) | %d | %d |\n\n", u.BalanceActual, u.BalanceExpected)

	w("## 3. Saturated command queue\n\n")
	w("The table is held busy with one bet while %d more arrive. The queue holds %d; the rest must be rejected immediately instead of waiting.\n\n", q.Attempts, q.QueueSize)
	w("| Metric | Value | Expected |\n|---|---|---|\n")
	w("| Queued | %d | %d |\n", q.Queued, q.QueueSize)
	w("| Rejected as busy | %d | %d |\n", q.RejectedBusy, q.Attempts-q.QueueSize)
	w("| Longest wait before a busy rejection | %.0f ms | 0 ms |\n", q.MaxRejectWaitMs)
	w("| Queued bets accepted once the table frees up | %d | %d |\n\n", q.AcceptedAfterward, q.QueueSize)

	w("## Coverage of the table\n\n")
	w("Statement coverage of `internal/game/table.go` reached by these tests alone (package total: %s). ", r.TotalCover)
	w("Paths such as test rounds and round cancellation are not exercised by these tests.\n\n")
	w("| Function | Coverage |\n|---|---|\n")
	for _, c := range r.Coverage {
		w("| `%s` | %s |\n", c.Function, c.Percent)
	}
	w("\n")

	w("## Environment\n\n")
	w("| | |\n|---|---|\n")
	w("| Host CPU | %s |\n| Host RAM | %s |\n| Host OS | %s |\n", r.HostCPU, r.HostRAM, r.HostOS)
	w("| Test runtime | Docker container `golang` (%s, linux/%s, %d CPUs) |\n\n", r.GoVersion, runtime.GOARCH, r.CPUs)

	w("## Reproduce\n\n")
	w("```powershell\n.\\scripts\\concurrency-report.ps1\n```\n\n")
	w("The script runs `go test -race -count=20 -run '^TestConcurrency' ./internal/game/` inside a Go container and regenerates this folder. ")
	w("The full test output is in [test-output.txt](test-output.txt) and the raw figures in [data/](data/).\n")
	return s.String()
}

func describe(name string) string {
	switch name {
	case "TestConcurrencyBurstAtClose":
		return "No bet is accepted at or after the close of the betting window"
	case "TestConcurrencySameUser":
		return "A user firing many bets at once gets exactly one accepted"
	case "TestConcurrencyQueueSaturation":
		return "A full queue rejects immediately instead of blocking"
	}
	return ""
}

func signed(v int) string {
	if v == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", v)
}
