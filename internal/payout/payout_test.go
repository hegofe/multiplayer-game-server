package payout

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"aviator/internal/game"
	"aviator/internal/wallet"
)

type fakeBets struct {
	mu      sync.Mutex
	bets    map[int64][]game.Bet
	deleted map[int64]bool
	release chan struct{}
}

func (f *fakeBets) RoundBets(_ context.Context, id int64) ([]game.Bet, error) {
	if f.release != nil {
		<-f.release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bets[id], nil
}

func (f *fakeBets) DeleteRound(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted[id] = true
	return nil
}

func (f *fakeBets) wasDeleted(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.deleted[id]
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestPayerCreditsWinnersAndDeletesTheRound(t *testing.T) {
	src := &fakeBets{deleted: map[int64]bool{}, bets: map[int64][]game.Bet{7: {
		{RoundID: 7, UserID: "user-1", Amount: 10, Target: 150},
		{RoundID: 7, UserID: "user-2", Amount: 5, Target: 200},
		{RoundID: 7, UserID: "user-3", Amount: 4, Target: 300},
	}}}
	funds := wallet.New(3, 0)
	p := NewPayer(src, funds, quietLog())

	p.Settle(game.Round{ID: 7, State: game.StateClosed, LandingPoint: 200})
	if err := p.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	for user, want := range map[string]int64{"user-1": 1500, "user-2": 1000, "user-3": 0} {
		if b, _ := funds.Balance(user); b != want {
			t.Errorf("%s balance = %d, want %d", user, b, want)
		}
	}
	if !src.wasDeleted(7) {
		t.Error("the settled round was not deleted")
	}
}

func TestPayerSettlesInTheBackground(t *testing.T) {
	src := &fakeBets{deleted: map[int64]bool{}, bets: map[int64][]game.Bet{1: {{RoundID: 1, UserID: "user-1", Amount: 1, Target: 150}}}, release: make(chan struct{})}
	funds := wallet.New(1, 0)
	p := NewPayer(src, funds, quietLog())

	returned := make(chan struct{})
	go func() {
		p.Settle(game.Round{ID: 1, LandingPoint: 500})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("Settle blocked while the payout was still running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := p.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait with a payout in progress: %v, want DeadlineExceeded", err)
	}

	close(src.release)
	if err := p.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if b, _ := funds.Balance("user-1"); b != 150 {
		t.Errorf("balance = %d, want 150", b)
	}
}

func TestPayerKeepsTheRoundWhenACreditFails(t *testing.T) {
	src := &fakeBets{deleted: map[int64]bool{}, bets: map[int64][]game.Bet{3: {
		{RoundID: 3, UserID: "user-1", Amount: 1, Target: 150},
		{RoundID: 3, UserID: "ghost", Amount: 1, Target: 150},
	}}}
	funds := wallet.New(1, 0)
	p := NewPayer(src, funds, quietLog())

	p.Settle(game.Round{ID: 3, LandingPoint: 200})
	if err := p.Wait(context.Background()); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if b, _ := funds.Balance("user-1"); b != 150 {
		t.Errorf("user-1 balance = %d, want 150", b)
	}
	if src.wasDeleted(3) {
		t.Error("the round was deleted although a payout failed")
	}
}
