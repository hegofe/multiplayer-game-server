package payout

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"aviator/internal/game"
)

const settleTimeout = 30 * time.Second

type BetSource interface {
	RoundBets(ctx context.Context, roundID int64) ([]game.Bet, error)
	DeleteRound(ctx context.Context, roundID int64) error
}

var _ game.Settler = (*Payer)(nil)

type Payer struct {
	bets  BetSource
	funds game.Funds
	log   *slog.Logger
	wg    sync.WaitGroup
}

func NewPayer(bets BetSource, funds game.Funds, log *slog.Logger) *Payer {
	return &Payer{bets: bets, funds: funds, log: log}
}

func (p *Payer) Settle(r game.Round) {
	p.wg.Go(func() { p.settle(r) })
}

func (p *Payer) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Payer) settle(r game.Round) {
	ctx, cancel := context.WithTimeout(context.Background(), settleTimeout)
	defer cancel()

	started := time.Now()
	bets, err := p.bets.RoundBets(ctx, r.ID)
	if err != nil {
		p.log.Error("payout aborted: could not read bets", "round", r.ID, "error", err)
		return
	}

	winners, failed := 0, 0
	var paid int64
	for _, b := range bets {
		if !b.Wins(r.LandingPoint) {
			continue
		}
		amount := b.PayoutCents()
		if err := p.funds.Credit(b.UserID, amount); err != nil {
			failed++
			p.log.Error("payout failed", "round", r.ID, "user", b.UserID, "cents", amount, "error", err)
			continue
		}
		winners++
		paid += amount
	}

	if failed > 0 {
		p.log.Error("round left in store for review: some payouts failed", "round", r.ID, "failed", failed)
		return
	}
	if err := p.bets.DeleteRound(ctx, r.ID); err != nil {
		p.log.Error("could not delete settled round", "round", r.ID, "error", err)
	}
	p.log.Info("round settled", "round", r.ID, "multiplier", r.LandingPoint.String(),
		"bets", len(bets), "winners", winners, "paidCents", paid, "took", time.Since(started))
}
