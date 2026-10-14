package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"aviator/internal/game"
)

func newTestClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	m := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: m.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { client.Close() })
	return m, client
}

func TestRedisStoreRoundLifecycle(t *testing.T) {
	ctx := context.Background()
	_, client := newTestClient(t)
	s := NewRedisStore(client)

	if _, err := s.ActiveRound(ctx); !errors.Is(err, game.ErrRoundNotFound) {
		t.Fatalf("ActiveRound on an empty store: %v, want ErrRoundNotFound", err)
	}

	id1, _ := s.NextRoundID(ctx)
	id2, _ := s.NextRoundID(ctx)
	if id1 != 1 || id2 != 2 {
		t.Fatalf("round ids = %d, %d; want 1, 2", id1, id2)
	}

	r := game.Round{ID: id2, State: game.StateCreated, StartTime: time.UnixMilli(1_800_000_000_123), BettingWindow: 5 * time.Second, Test: true}
	if err := s.ActivateRound(ctx, r); err != nil {
		t.Fatalf("ActivateRound: %v", err)
	}
	got, err := s.ActiveRound(ctx)
	if err != nil || got.ID != r.ID || got.State != r.State || !got.StartTime.Equal(r.StartTime) || got.BettingWindow != r.BettingWindow || !got.Test {
		t.Fatalf("ActiveRound = %+v, %v; want %+v", got, err, r)
	}

	r.State = game.StateRunning
	r.LandingPoint = 347
	if err := s.SaveRound(ctx, r); err != nil {
		t.Fatalf("SaveRound: %v", err)
	}
	if got, _ := s.ActiveRound(ctx); got.State != game.StateRunning || got.LandingPoint != 347 {
		t.Fatalf("after SaveRound: %+v", got)
	}

	if n, err := s.CountBets(ctx, r.ID); err != nil || n != 0 {
		t.Fatalf("CountBets with no bets = %d, %v", n, err)
	}
	client.HSet(ctx, betsKey(r.ID), "user-1", "10|150", "user-2", "3|420")
	if n, _ := s.CountBets(ctx, r.ID); n != 2 {
		t.Fatalf("CountBets = %d, want 2", n)
	}
	bets, err := s.RoundBets(ctx, r.ID)
	if err != nil || len(bets) != 2 {
		t.Fatalf("RoundBets = %+v, %v", bets, err)
	}
	for _, b := range bets {
		want := map[string]game.Bet{
			"user-1": {RoundID: r.ID, UserID: "user-1", Amount: 10, Target: 150},
			"user-2": {RoundID: r.ID, UserID: "user-2", Amount: 3, Target: 420},
		}[b.UserID]
		if b != want {
			t.Errorf("bet = %+v, want %+v", b, want)
		}
	}

	if err := s.DeleteRound(ctx, r.ID); err != nil {
		t.Fatalf("DeleteRound: %v", err)
	}
	if _, err := s.CountBets(ctx, r.ID); !errors.Is(err, game.ErrRoundNotFound) {
		t.Fatalf("CountBets after delete: %v, want ErrRoundNotFound", err)
	}
}

func TestRedisStoreRejectsCorruptBets(t *testing.T) {
	ctx := context.Background()
	_, client := newTestClient(t)
	client.HSet(ctx, betsKey(1), "user-1", "not-a-bet")
	if _, err := NewRedisStore(client).RoundBets(ctx, 1); err == nil {
		t.Fatal("RoundBets accepted a malformed value")
	}
}

func TestRedisStoreKeepsTheLastTenResults(t *testing.T) {
	ctx := context.Background()
	_, client := newTestClient(t)
	s := NewRedisStore(client)

	for id := int64(1); id <= 12; id++ {
		if err := s.PushResult(ctx, game.RoundResult{RoundID: id, LandingPoint: game.Multiplier(100 + id)}); err != nil {
			t.Fatalf("PushResult: %v", err)
		}
	}
	history, err := s.History(ctx)
	if err != nil || len(history) != HistorySize {
		t.Fatalf("History = %d entries, %v; want %d", len(history), err, HistorySize)
	}
	for i, r := range history {
		wantID := int64(12 - i)
		if r.RoundID != wantID || r.LandingPoint != game.Multiplier(100+wantID) {
			t.Errorf("history[%d] = %+v, want round %d", i, r, wantID)
		}
	}
	if n, _ := client.LLen(ctx, keyHistory).Result(); n != HistorySize {
		t.Errorf("stored history length = %d, want %d", n, HistorySize)
	}
}

func TestRedisStorePing(t *testing.T) {
	m, client := newTestClient(t)
	s := NewRedisStore(client)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	m.Close()
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("Ping succeeded with Redis down")
	}
}
