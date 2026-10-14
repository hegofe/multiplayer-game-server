package game

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"
)

func TestParseMultiplier(t *testing.T) {
	cases := []struct {
		in   string
		want Multiplier
		ok   bool
	}{
		{"2", 200, true},
		{"2.5", 250, true},
		{"2.35", 235, true},
		{"0.99", 99, true},
		{" 1.01 ", 101, true},
		{"1.001", 0, false},
		{"-1", 0, false},
		{"abc", 0, false},
		{"", 0, false},
		{"2.", 0, false},
		{".5", 0, false},
	}
	for _, c := range cases {
		got, err := ParseMultiplier(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("ParseMultiplier(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
		if !c.ok && !errors.Is(err, ErrMalformedMultiplier) {
			t.Errorf("ParseMultiplier(%q) error = %v, want ErrMalformedMultiplier", c.in, err)
		}
	}
}

func TestMultiplierJSON(t *testing.T) {
	out, err := json.Marshal(struct {
		M Multiplier `json:"m"`
	}{235})
	if err != nil || string(out) != `{"m":2.35}` {
		t.Fatalf("marshal = %s, %v", out, err)
	}

	var in struct {
		M Multiplier `json:"m"`
	}
	for body, want := range map[string]Multiplier{`{"m":1.50}`: 150, `{"m":"2.00"}`: 200, `{"m":3}`: 300} {
		if err := json.Unmarshal([]byte(body), &in); err != nil || in.M != want {
			t.Errorf("unmarshal %s = %d, %v; want %d", body, in.M, err, want)
		}
	}
	if err := json.Unmarshal([]byte(`{"m":1.505}`), &in); err == nil {
		t.Error("unmarshal of three decimals succeeded, want an error")
	}
}

func TestBetValidate(t *testing.T) {
	valid := Bet{RoundID: 1, UserID: "user-1", Amount: 1, Target: MinTarget}
	cases := map[string]struct {
		mutate func(*Bet)
		want   error
	}{
		"valid":            {func(*Bet) {}, nil},
		"missing round":    {func(b *Bet) { b.RoundID = 0 }, ErrMissingRound},
		"missing user":     {func(b *Bet) { b.UserID = "" }, ErrMissingUser},
		"zero amount":      {func(b *Bet) { b.Amount = 0 }, ErrInvalidAmount},
		"negative amount":  {func(b *Bet) { b.Amount = -5 }, ErrInvalidAmount},
		"target below min": {func(b *Bet) { b.Target = 100 }, ErrInvalidTarget},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			b := valid
			c.mutate(&b)
			if err := b.Validate(); !errors.Is(err, c.want) {
				t.Fatalf("Validate() = %v, want %v", err, c.want)
			}
		})
	}
}

func TestBetOutcome(t *testing.T) {
	b := Bet{Amount: 10, Target: 235}
	if b.StakeCents() != 1000 {
		t.Errorf("StakeCents = %d, want 1000", b.StakeCents())
	}
	if b.PayoutCents() != 2350 {
		t.Errorf("PayoutCents = %d, want 2350", b.PayoutCents())
	}
	cases := []struct {
		landing Multiplier
		wins    bool
	}{{234, false}, {235, true}, {1000, true}}
	for _, c := range cases {
		if got := b.Wins(c.landing); got != c.wins {
			t.Errorf("Wins(%s) = %t, want %t", c.landing, got, c.wins)
		}
	}
}

func TestRoundStateJSON(t *testing.T) {
	for _, s := range []RoundState{StateCreated, StateOpen, StateRunning, StateClosed, StateCancelled} {
		out, err := json.Marshal(s)
		if err != nil || string(out) != `"`+string(s)+`"` {
			t.Errorf("marshal %s = %s, %v", s, out, err)
		}
	}
	for s, want := range map[RoundState]bool{StateCreated: true, StateOpen: true, StateRunning: true, StateClosed: false, StateCancelled: false} {
		if s.InProgress() != want {
			t.Errorf("%s.InProgress() = %t, want %t", s, !want, want)
		}
	}
}

func TestRoundAcceptsBetsAt(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := Round{State: StateOpen, StartTime: start, BettingWindow: 5 * time.Second}
	cases := []struct {
		at   time.Time
		want bool
	}{
		{start.Add(-time.Millisecond), false},
		{start, true},
		{start.Add(5*time.Second - time.Nanosecond), true},
		{start.Add(5 * time.Second), false},
	}
	for _, c := range cases {
		if got := r.AcceptsBetsAt(c.at); got != c.want {
			t.Errorf("AcceptsBetsAt(%v) = %t, want %t", c.at.Sub(start), got, c.want)
		}
	}
	r.State = StateCreated
	if r.AcceptsBetsAt(start) {
		t.Error("a created round accepted a bet")
	}
}

func TestRandomLandingStaysInRange(t *testing.T) {
	g := RandomLanding{Min: 100, Max: 1000}
	seen := map[Multiplier]bool{}
	for range 10000 {
		v := g.Next()
		if v < g.Min || v > g.Max {
			t.Fatalf("Next() = %d, outside [%d, %d]", v, g.Min, g.Max)
		}
		seen[v] = true
	}
	if len(seen) < 100 {
		t.Errorf("only %d distinct values in 10000 draws", len(seen))
	}
	if v := (RandomLanding{Min: 250, Max: 250}).Next(); v != 250 {
		t.Errorf("fixed range Next() = %d, want 250", v)
	}
}

func TestFixedLandingCycles(t *testing.T) {
	g := NewFixedLanding(150, 300)
	got := []Multiplier{g.Next(), g.Next(), g.Next()}
	if got[0] != 150 || got[1] != 300 || got[2] != 150 {
		t.Fatalf("sequence = %v, want [150 300 150]", got)
	}
}

func TestFlightDuration(t *testing.T) {
	if d := FlightDuration(100, 0.2); d != 0 {
		t.Errorf("FlightDuration(1.00x) = %v, want 0", d)
	}
	want := time.Duration(math.Log(10) / 0.2 * float64(time.Second))
	if d := FlightDuration(1000, 0.2); d != want {
		t.Errorf("FlightDuration(10.00x) = %v, want %v", d, want)
	}
	if FlightDuration(300, 0.2) <= FlightDuration(200, 0.2) {
		t.Error("a higher landing point must fly longer")
	}
}
