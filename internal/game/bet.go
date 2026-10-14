package game

import "errors"

const CentsPerUnit = 100

var (
	ErrMissingRound  = errors.New("roundId is required")
	ErrMissingUser   = errors.New("userId is required")
	ErrInvalidAmount = errors.New("amount must be a whole number of at least 1")
	ErrInvalidTarget = errors.New("target multiplier must be at least 1.01")
)

type Bet struct {
	RoundID int64
	UserID  string
	Amount  int64
	Target  Multiplier
}

func (b Bet) Validate() error {
	switch {
	case b.RoundID <= 0:
		return ErrMissingRound
	case b.UserID == "":
		return ErrMissingUser
	case b.Amount < 1:
		return ErrInvalidAmount
	case b.Target < MinTarget:
		return ErrInvalidTarget
	}
	return nil
}

func (b Bet) StakeCents() int64 {
	return b.Amount * CentsPerUnit
}

func (b Bet) Wins(landing Multiplier) bool {
	return b.Target <= landing
}

func (b Bet) PayoutCents() int64 {
	return b.Amount * int64(b.Target)
}
