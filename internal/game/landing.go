package game

import (
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

type LandingGenerator interface {
	Next() Multiplier
}

type RandomLanding struct {
	Min Multiplier
	Max Multiplier
}

func (g RandomLanding) Next() Multiplier {
	return g.Min + Multiplier(rand.Int64N(int64(g.Max-g.Min)+1))
}

type FixedLanding struct {
	mu     sync.Mutex
	values []Multiplier
	next   int
}

func NewFixedLanding(values ...Multiplier) *FixedLanding {
	return &FixedLanding{values: values}
}

func (g *FixedLanding) Next() Multiplier {
	g.mu.Lock()
	defer g.mu.Unlock()
	v := g.values[g.next%len(g.values)]
	g.next++
	return v
}

func FlightDuration(landing Multiplier, growth float64) time.Duration {
	if landing <= 100 {
		return 0
	}
	seconds := math.Log(float64(landing)/100) / growth
	return time.Duration(seconds * float64(time.Second))
}
