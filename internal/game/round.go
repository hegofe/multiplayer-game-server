package game

import "time"

type RoundState string

const (
	StateCreated   RoundState = "created"
	StateOpen      RoundState = "open"
	StateRunning   RoundState = "running"
	StateClosed    RoundState = "closed"
	StateCancelled RoundState = "cancelled"
)

func (s RoundState) InProgress() bool {
	return s == StateCreated || s == StateOpen || s == StateRunning
}

type Round struct {
	ID            int64
	State         RoundState
	StartTime     time.Time
	BettingWindow time.Duration
	LandingPoint  Multiplier
	Test          bool
}

func (r Round) BettingClosesAt() time.Time {
	return r.StartTime.Add(r.BettingWindow)
}

func (r Round) AcceptsBetsAt(t time.Time) bool {
	return r.State == StateOpen && !t.Before(r.StartTime) && t.Before(r.BettingClosesAt())
}
