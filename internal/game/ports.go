package game

import (
	"context"
	"errors"
)

var (
	ErrRoundNotFound = errors.New("round not found")
	ErrDuplicateBet  = errors.New("user already placed a bet in this round")
	ErrWriterClosed  = errors.New("bet writer is closed")
)

type RoundResult struct {
	RoundID      int64
	LandingPoint Multiplier
}

type RoundStore interface {
	NextRoundID(ctx context.Context) (int64, error)
	ActivateRound(ctx context.Context, r Round) error
	SaveRound(ctx context.Context, r Round) error
	ActiveRound(ctx context.Context) (Round, error)
	CountBets(ctx context.Context, roundID int64) (int64, error)
	RoundBets(ctx context.Context, roundID int64) ([]Bet, error)
	DeleteRound(ctx context.Context, roundID int64) error
	PushResult(ctx context.Context, res RoundResult) error
	History(ctx context.Context) ([]RoundResult, error)
}

type BetWriter interface {
	Submit(ctx context.Context, bet Bet, done func(error)) error
	Flush(ctx context.Context) error
}

type Funds interface {
	Debit(user string, cents int64) error
	Credit(user string, cents int64) error
}

type Settler interface {
	Settle(r Round)
}
