package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"aviator/internal/game"
)

type results struct {
	mu   sync.Mutex
	errs map[string][]error
	wg   sync.WaitGroup
}

func newResults() *results { return &results{errs: map[string][]error{}} }

func (r *results) callback(user string) func(error) {
	r.wg.Add(1)
	return func(err error) {
		r.mu.Lock()
		r.errs[user] = append(r.errs[user], err)
		r.mu.Unlock()
		r.wg.Done()
	}
}

func (r *results) wait(t *testing.T) {
	t.Helper()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the writer to report results")
	}
}

func startWriter(t *testing.T, maxSize int, maxWait time.Duration) (*BatchBetWriter, func(roundID int64) int64) {
	t.Helper()
	_, client := newTestClient(t)
	w := NewBatchBetWriter(client, maxSize, maxWait)
	go w.Run()
	t.Cleanup(w.Close)
	count := func(roundID int64) int64 { return client.HLen(context.Background(), betsKey(roundID)).Val() }
	return w, count
}

func bet(user string) game.Bet {
	return game.Bet{RoundID: 1, UserID: user, Amount: 2, Target: 150}
}

func TestBatchBetWriterReportsEachBet(t *testing.T) {
	w, count := startWriter(t, 100, 10*time.Millisecond)
	res := newResults()
	ctx := context.Background()

	for i := range 50 {
		user := fmt.Sprintf("user-%d", i)
		if err := w.Submit(ctx, bet(user), res.callback(user)); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if err := w.Submit(ctx, bet("user-7"), res.callback("user-7")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	res.wait(t)

	for i := range 50 {
		user := fmt.Sprintf("user-%d", i)
		errs := res.errs[user]
		if user == "user-7" {
			if len(errs) != 2 || errs[0] != nil || !errors.Is(errs[1], game.ErrDuplicateBet) {
				t.Errorf("user-7 results = %v, want [nil, ErrDuplicateBet]", errs)
			}
			continue
		}
		if len(errs) != 1 || errs[0] != nil {
			t.Errorf("%s results = %v, want [nil]", user, errs)
		}
	}
	if n := count(1); n != 50 {
		t.Errorf("stored bets = %d, want 50", n)
	}
}

func TestBatchBetWriterSendsAFullBatchWithoutWaiting(t *testing.T) {
	w, count := startWriter(t, 5, time.Hour)
	res := newResults()
	for i := range 5 {
		user := fmt.Sprintf("user-%d", i)
		if err := w.Submit(context.Background(), bet(user), res.callback(user)); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	res.wait(t)
	if n := count(1); n != 5 {
		t.Errorf("stored bets = %d, want 5", n)
	}
}

func TestBatchBetWriterFlushWaitsForPendingBets(t *testing.T) {
	w, count := startWriter(t, 100, time.Hour)
	res := newResults()
	for i := range 3 {
		user := fmt.Sprintf("user-%d", i)
		if err := w.Submit(context.Background(), bet(user), res.callback(user)); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if n := count(1); n != 3 {
		t.Fatalf("stored bets right after Flush = %d, want 3", n)
	}
	res.wait(t)
}

func TestBatchBetWriterCloseWritesPendingBets(t *testing.T) {
	w, count := startWriter(t, 100, time.Hour)
	res := newResults()
	for _, user := range []string{"user-1", "user-2"} {
		if err := w.Submit(context.Background(), bet(user), res.callback(user)); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	w.Close()
	res.wait(t)
	if n := count(1); n != 2 {
		t.Errorf("stored bets after Close = %d, want 2", n)
	}
	if err := w.Submit(context.Background(), bet("user-3"), func(error) {}); !errors.Is(err, game.ErrWriterClosed) {
		t.Errorf("Submit after Close: %v, want ErrWriterClosed", err)
	}
	if err := w.Flush(context.Background()); !errors.Is(err, game.ErrWriterClosed) {
		t.Errorf("Flush after Close: %v, want ErrWriterClosed", err)
	}
}

func TestBatchBetWriterReportsRedisFailures(t *testing.T) {
	m, client := newTestClient(t)
	w := NewBatchBetWriter(client, 100, time.Hour)
	go w.Run()
	t.Cleanup(w.Close)
	m.Close()

	res := newResults()
	if err := w.Submit(context.Background(), bet("user-1"), res.callback("user-1")); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := w.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	res.wait(t)
	if errs := res.errs["user-1"]; len(errs) != 1 || errs[0] == nil {
		t.Fatalf("results = %v, want one error", errs)
	}
}
