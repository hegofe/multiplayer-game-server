package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type latency struct {
	Avg float64 `json:"avg"`
	Med float64 `json:"med"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
}

type run struct {
	Name               string  `json:"name"`
	Mode               string  `json:"mode"`
	Bets               int     `json:"bets"`
	VUs                int     `json:"vus"`
	WindowMs           float64 `json:"windowMs"`
	Sent               int     `json:"sent"`
	Accepted           int     `json:"accepted"`
	RejectedClosed     int     `json:"rejectedClosed"`
	RejectedBusy       int     `json:"rejectedBusy"`
	RejectedOther      int     `json:"rejectedOther"`
	TransportErrors    int     `json:"transportErrors"`
	AcceptedAfterClose int     `json:"acceptedAfterClose"`
	BehindSchedule     int     `json:"behindSchedule"`
	Stored             int     `json:"stored"`
	Latency            latency `json:"latencyMs"`
	SendDelayP95Ms     float64 `json:"sendDelayP95Ms"`
	ErrorRate          float64 `json:"errorRate"`
	TargetRate         float64 `json:"targetRate"`
}

func (r run) errors() int { return r.RejectedOther + r.TransportErrors }

type scenario struct {
	Name   string
	Runs   []run
	Median run
	P95Min float64
	P95Max float64
}

var order = []string{"base", "burst", "stress-2x", "stress-5x", "stress-10x"}

var labels = map[string]string{
	"base":       "Base",
	"burst":      "Burst",
	"stress-2x":  "Stress 2x",
	"stress-5x":  "Stress 5x",
	"stress-10x": "Stress 10x",
}

func main() {
	dataDir := flag.String("data", "results/load/data", "directory with one folder per scenario")
	outDir := flag.String("out", "results/load", "output directory")
	flag.Parse()

	scenarios := load(*dataDir)
	if len(scenarios) == 0 {
		log.Fatal("no load test results found")
	}
	write(filepath.Join(*outDir, "latency.svg"), latencySVG(scenarios))
	write(filepath.Join(*outDir, "outcome.svg"), outcomeSVG(scenarios))
	write(filepath.Join(*outDir, "README.md"), readme(scenarios))
	fmt.Println("report written to", *outDir)
}

func load(dir string) []scenario {
	var out []scenario
	for _, name := range order {
		files, _ := filepath.Glob(filepath.Join(dir, name, "run-*.json"))
		if len(files) == 0 {
			continue
		}
		s := scenario{Name: name}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				log.Fatal(err)
			}
			var r run
			if err := json.Unmarshal(data, &r); err != nil {
				log.Fatalf("decode %s: %v", f, err)
			}
			s.Runs = append(s.Runs, r)
		}
		sorted := append([]run(nil), s.Runs...)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Latency.P95 < sorted[j].Latency.P95 })
		s.Median = sorted[len(sorted)/2]
		s.P95Min = sorted[0].Latency.P95
		s.P95Max = sorted[len(sorted)-1].Latency.P95
		out = append(out, s)
	}
	return out
}

func write(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

const svgStyle = `<style>
svg{--surface:#fcfcfb;--ink:#0b0b0b;--ink2:#52514e;--muted:#898781;--grid:#e1e0d9;--axis:#c3c2b7;--s1:#2a78d6;--s2:#eb6834}
@media (prefers-color-scheme: dark){svg{--surface:#1a1a19;--ink:#ffffff;--ink2:#c3c2b7;--muted:#898781;--grid:#2c2c2a;--axis:#383835;--s1:#3987e5;--s2:#d95926}}
text{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;fill:var(--ink2)}
.bg{fill:var(--surface)}.title{fill:var(--ink);font-size:15px;font-weight:600}.label{font-size:12px}.value{font-size:11px;fill:var(--ink)}
.tick{font-size:11px;fill:var(--muted);font-variant-numeric:tabular-nums}.grid{stroke:var(--grid);stroke-width:1}.axis{stroke:var(--axis);stroke-width:1}
.s1{fill:var(--s1)}.s2{fill:var(--s2)}
</style>`

func svgOpen(s *strings.Builder, w, h float64, title, desc string) {
	fmt.Fprintf(s, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img" aria-labelledby="t d">`, w, h, w, h)
	fmt.Fprintf(s, `<title id="t">%s</title><desc id="d">%s</desc>`, html.EscapeString(title), html.EscapeString(desc))
	s.WriteString(svgStyle)
	fmt.Fprintf(s, `<rect class="bg" width="%.0f" height="%.0f" rx="8"/>`, w, h)
}

func legend(s *strings.Builder, x, y float64, items ...string) {
	for i, it := range items {
		fmt.Fprintf(s, `<rect class="s%d" x="%.0f" y="%.0f" width="10" height="10" rx="2"/><text class="label" x="%.0f" y="%.0f">%s</text>`, i+1, x, y-9, x+16, y, html.EscapeString(it))
		x += 16 + float64(len(it))*6.6 + 28
	}
}

func roundedTop(x, y, w, h, r float64) string {
	if h <= 0 {
		return ""
	}
	r = min(r, w/2, h)
	return fmt.Sprintf("M%.2f,%.2f V%.2f Q%.2f,%.2f %.2f,%.2f H%.2f Q%.2f,%.2f %.2f,%.2f V%.2f Z",
		x, y+h, y+r, x, y, x+r, y, x+w-r, x+w, y, x+w, y+r, y+h)
}

func roundedRight(x, y, w, h, r float64) string {
	if w <= 0 {
		return ""
	}
	r = min(r, h/2, w)
	return fmt.Sprintf("M%.2f,%.2f H%.2f Q%.2f,%.2f %.2f,%.2f V%.2f Q%.2f,%.2f %.2f,%.2f H%.2f Z",
		x, y, x+w-r, x+w, y, x+w, y+r, y+h-r, x+w, y+h, x+w-r, y+h, x)
}

func niceMax(v float64) (float64, float64) {
	for _, step := range []float64{1, 2, 5, 10, 20, 25, 50, 100, 200, 250, 500, 1000, 2000, 5000} {
		if v/step <= 5 {
			return step * float64(int(v/step)+1), step
		}
	}
	return v, v / 5
}

func latencySVG(sc []scenario) string {
	const width, height = 760.0, 360.0
	const left, right, top, bottom = 56.0, 16.0, 92.0, 48.0
	plotW, plotH := width-left-right, height-top-bottom

	maxV := 0.0
	for _, s := range sc {
		maxV = max(maxV, s.Median.Latency.P95)
	}
	yMax, step := niceMax(maxV)
	y := func(v float64) float64 { return top + plotH - v/yMax*plotH }

	var s strings.Builder
	svgOpen(&s, width, height, "Bet latency by scenario", "Median (p50) and 95th percentile (p95) latency of POST /bets for each scenario, median of the runs.")
	fmt.Fprintf(&s, `<text class="title" x="%.0f" y="28">Bet latency by scenario</text>`, left)
	fmt.Fprintf(&s, `<text class="label" x="%.0f" y="48">POST /bets response time in milliseconds, median of %d runs per scenario</text>`, left, len(sc[0].Runs))
	legend(&s, left, 72, "p50", "p95")

	for v := 0.0; v <= yMax+0.001; v += step {
		cls := "grid"
		if v == 0 {
			cls = "axis"
		}
		fmt.Fprintf(&s, `<line class="%s" x1="%.0f" x2="%.0f" y1="%.2f" y2="%.2f"/>`, cls, left, width-right, y(v), y(v))
		fmt.Fprintf(&s, `<text class="tick" x="%.0f" y="%.2f" text-anchor="end">%.0f</text>`, left-8, y(v)+4, v)
	}

	band := plotW / float64(len(sc))
	const barW = 24.0
	for i, sce := range sc {
		cx := left + band*float64(i) + band/2
		m := sce.Median.Latency
		x1 := cx - barW - 1
		x2 := cx + 1
		fmt.Fprintf(&s, `<path class="s1" d="%s"><title>%s p50: %.1f ms</title></path>`, roundedTop(x1, y(m.Med), barW, y(0)-y(m.Med), 4), labels[sce.Name], m.Med)
		fmt.Fprintf(&s, `<path class="s2" d="%s"><title>%s p95: %.1f ms</title></path>`, roundedTop(x2, y(m.P95), barW, y(0)-y(m.P95), 4), labels[sce.Name], m.P95)
		fmt.Fprintf(&s, `<text class="value" x="%.2f" y="%.2f" text-anchor="middle">%s</text>`, x2+barW/2, y(m.P95)-6, ms(m.P95))
		fmt.Fprintf(&s, `<text class="label" x="%.2f" y="%.0f" text-anchor="middle">%s</text>`, cx, top+plotH+18, labels[sce.Name])
		fmt.Fprintf(&s, `<text class="tick" x="%.2f" y="%.0f" text-anchor="middle">%s bets</text>`, cx, top+plotH+34, thousands(sce.Median.Bets))
	}
	s.WriteString(`</svg>`)
	return s.String()
}

func outcomeSVG(sc []scenario) string {
	const width = 760.0
	const left, right, top = 120.0, 64.0, 92.0
	const rowH, barH = 36.0, 20.0
	height := top + rowH*float64(len(sc)) + 20
	plotW := width - left - right

	var s strings.Builder
	svgOpen(&s, width, height, "Outcome of the bets by scenario", "Share of bets accepted and share rejected because they reached the server after the betting window closed.")
	fmt.Fprintf(&s, `<text class="title" x="24" y="28">Outcome of the bets by scenario</text>`)
	fmt.Fprintf(&s, `<text class="label" x="24" y="48">Share of the bets sent in each scenario, median run</text>`)
	legend(&s, 24, 72, "Accepted", "Rejected: arrived after the window closed")

	for i, sce := range sc {
		r := sce.Median
		y := top + rowH*float64(i)
		total := float64(max(r.Sent, 1))
		accW := float64(r.Accepted) / total * plotW
		lateW := float64(r.RejectedClosed) / total * plotW
		fmt.Fprintf(&s, `<text class="label" x="%.0f" y="%.1f" text-anchor="end">%s</text>`, left-12, y+barH/2+4, labels[sce.Name])
		if r.RejectedClosed == 0 {
			fmt.Fprintf(&s, `<path class="s1" d="%s"><title>%d accepted</title></path>`, roundedRight(left, y, accW, barH, 4), r.Accepted)
		} else {
			fmt.Fprintf(&s, `<rect class="s1" x="%.2f" y="%.2f" width="%.2f" height="%.0f"><title>%d accepted</title></rect>`, left, y, max(accW-2, 0), barH, r.Accepted)
			fmt.Fprintf(&s, `<path class="s2" d="%s"><title>%d rejected: arrived after the close</title></path>`, roundedRight(left+accW, y, lateW, barH, 4), r.RejectedClosed)
		}
		fmt.Fprintf(&s, `<text class="value" x="%.0f" y="%.1f">%s</text>`, width-right+8, y+barH/2+4, pct(r.Accepted, r.Sent))
	}
	s.WriteString(`</svg>`)
	return s.String()
}

func ms(v float64) string {
	if v < 10 {
		return fmt.Sprintf("%.1f ms", v)
	}
	return fmt.Sprintf("%.0f ms", v)
}

func pct(a, b int) string {
	if b == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", float64(a)/float64(b)*100)
}

func thousands(n int) string {
	s := fmt.Sprint(n)
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, out...), ",")
}

func readme(sc []scenario) string {
	var s strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&s, format, a...) }

	totalRuns, afterClose, mismatched, serverErrors := 0, 0, 0, 0
	for _, sce := range sc {
		for _, r := range sce.Runs {
			totalRuns++
			afterClose += r.AcceptedAfterClose
			serverErrors += r.errors()
			if r.Stored != r.Accepted {
				mismatched++
			}
		}
	}
	vus := sc[0].Median.VUs

	w("# Load test results\n\n")
	w("Load tests of the Aviator betting service with [k6](https://k6.io), generated on %s UTC.\n\n", time.Now().UTC().Format("2006-01-02 15:04"))
	for _, sce := range sc {
		if sce.Name == "base" {
			m := sce.Median
			w("**At the production load of %s bets per round, the median run answered with p95 %s and p99 %s, accepted %s of the bets and had %d errors.**\n\n",
				thousands(m.Bets), ms(m.Latency.P95), ms(m.Latency.P99), pct(m.Accepted, m.Sent), m.errors())
		}
	}
	w("![Bet latency by scenario](latency.svg)\n\n")
	w("![Outcome of the bets by scenario](outcome.svg)\n\n")

	w("## How the test works\n\n")
	w("Each run targets one real round of the running server. k6 waits until the round is announced, opens %d connections (one per virtual user) before betting opens, ", vus)
	w("and then sends every bet at a scheduled instant inside the betting window, each from a different user (one bet per user and round).\n\n")
	w("- **Uniform**: bets are spread evenly over the first 4.8 s of the 5 s window.\n")
	w("- **Burst**: 20%% of the bets over the first 3.8 s and 80%% in the last second before the close.\n")
	w("- **Stress**: uniform, at 2, 5 and 10 times the production volume.\n\n")
	w("The production reference is an average of 5,000 bets per round. Each scenario ran %d times; tables show the run with the median p95.\n\n", len(sc[0].Runs))

	w("## Results\n\n")
	w("| Scenario | Bets per round | Target rate | Accepted | Rejected after close | Errors | p50 | p95 | p99 | p95 across runs | Sent late by k6 |\n")
	w("|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, sce := range sc {
		m := sce.Median
		w("| %s | %s | %s | %s (%s) | %s | %d | %s | %s | %s | %s – %s | %s |\n",
			labels[sce.Name], thousands(m.Bets), rate(m),
			thousands(m.Accepted), pct(m.Accepted, m.Sent), thousands(m.RejectedClosed), m.errors(),
			ms(m.Latency.Med), ms(m.Latency.P95), ms(m.Latency.P99), ms(sce.P95Min), ms(sce.P95Max), pct(m.BehindSchedule, m.Sent))
	}
	w("\n")
	w("*Sent late by k6* is the share of bets that k6 sent more than 50 ms after their scheduled instant. ")
	w("When it is high, the load generator, not only the server, has run out of capacity.\n\n")

	w("## Correctness under load\n\n")
	w("| Check | Result |\n|---|---|\n")
	w("| Bets accepted after the window closed, across all %d runs | %d |\n", totalRuns, afterClose)
	w("| Runs where bets stored in Redis differ from bets accepted | %d |\n", mismatched)
	w("| Server or transport errors, across all runs | %d |\n\n", serverErrors)

	w("## Reading the results\n\n")
	w("- k6 and the server run on the **same laptop** and compete for its CPU. The figures describe this single-machine setup, not the server on dedicated hardware.\n")
	w("- When the requested rate exceeds what the machine can handle, latency grows, k6 falls behind schedule and some bets reach the server after the window has closed. ")
	w("The server rejects them, as it must; they are not errors.\n")
	w("- Results vary between runs on a laptop (power and thermal management, background processes); the *p95 across runs* column shows that spread.\n\n")

	w("## Environment\n\n")
	w("| | |\n|---|---|\n")
	w("| Host CPU | %s |\n| Host RAM | %s |\n| Host OS | %s |\n", envOr("HOST_CPU", "unknown"), envOr("HOST_RAM", "unknown"), envOr("HOST_OS", "unknown"))
	w("| k6 | %s, %d virtual users |\n", envOr("K6_VERSION", "unknown"), vus)
	w("| Server | native Windows binary, Redis 8 in Docker Desktop |\n\n")

	w("## Reproduce\n\n")
	w("```powershell\n.\\scripts\\load-test.ps1\n```\n\n")
	w("The script starts Redis and the server with enough users, runs every scenario, saves the raw k6 summaries in [data/](data/) and regenerates this folder.\n")
	return s.String()
}

func rate(r run) string {
	if r.Mode == "burst" {
		peak := float64(r.Bets) * 0.8
		return fmt.Sprintf("%s bets/s on average, %s bets/s in the last second", thousands(int(r.TargetRate+0.5)), thousands(int(peak+0.5)))
	}
	return thousands(int(r.TargetRate+0.5)) + " bets/s"
}
