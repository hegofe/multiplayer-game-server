package game

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"aviator/internal/wallet"
)

func TestTableRoundCycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		h := startTable(t, defaultTestConfig(), 1, nil)
		synctest.Wait()

		r := h.activeRound(t)
		if r.State != StateCreated || !r.StartTime.Equal(start.Add(h.cfg.RoundPause)) || r.BettingWindow != h.cfg.BettingWindow {
			t.Fatalf("first round = %+v, want created, opening after the pause", r)
		}

		r = h.waitUntilOpen(t)
		closesAt := r.BettingClosesAt()

		time.Sleep(time.Until(closesAt))
		synctest.Wait()
		r = h.activeRound(t)
		if r.State != StateRunning || r.LandingPoint != 200 {
			t.Fatalf("after the close: state %s, landing %d; want running, 200", r.State, r.LandingPoint)
		}

		landsAt := closesAt.Add(h.cfg.PreFlightWait + FlightDuration(200, h.cfg.FlightGrowth))
		time.Sleep(time.Until(landsAt))
		synctest.Wait()

		settled := h.settler.settled()
		if len(settled) != 1 || settled[0].ID != r.ID || settled[0].State != StateClosed {
			t.Fatalf("settled = %+v, want round %d closed", settled, r.ID)
		}
		history, _ := h.store.History(context.Background())
		if len(history) != 1 || history[0] != (RoundResult{RoundID: r.ID, LandingPoint: 200}) {
			t.Fatalf("history = %+v", history)
		}
		next := h.activeRound(t)
		if next.ID != r.ID+1 || next.State != StateCreated || !next.StartTime.Equal(landsAt.Add(h.cfg.RoundPause)) {
			t.Fatalf("next round = %+v, want created and opening %v after the landing", next, h.cfg.RoundPause)
		}
	})
}

func TestTableFlushesPendingBetsBeforeTakeOff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := startTable(t, defaultTestConfig(), 1, nil)
		r := h.waitUntilOpen(t)
		time.Sleep(time.Until(r.BettingClosesAt()))
		synctest.Wait()

		flushes := h.writer.flushTimes()
		if len(flushes) != 1 || !flushes[0].Equal(r.BettingClosesAt()) {
			t.Fatalf("flushes = %v, want one at the close %v", flushes, r.BettingClosesAt())
		}
		if len(h.settler.settled()) != 0 {
			t.Fatal("the round landed before take-off")
		}
	})
}

func TestTableRejectsInvalidBets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := startTable(t, defaultTestConfig(), 2, nil)
		synctest.Wait()
		created := h.activeRound(t)
		if err := h.bet(created, "user-1", 1); !errors.Is(err, ErrBettingClosed) {
			t.Errorf("bet before opening: %v, want ErrBettingClosed", err)
		}

		r := h.waitUntilOpen(t)
		cases := map[string]struct {
			bet  Bet
			want error
		}{
			"invalid amount":       {Bet{RoundID: r.ID, UserID: "user-1", Amount: 0, Target: 200}, ErrInvalidAmount},
			"invalid target":       {Bet{RoundID: r.ID, UserID: "user-1", Amount: 1, Target: 100}, ErrInvalidTarget},
			"another round":        {Bet{RoundID: r.ID + 1, UserID: "user-1", Amount: 1, Target: 200}, ErrRoundNotActive},
			"unknown user":         {Bet{RoundID: r.ID, UserID: "nobody", Amount: 1, Target: 200}, wallet.ErrUnknownUser},
			"insufficient balance": {Bet{RoundID: r.ID, UserID: "user-1", Amount: 101, Target: 200}, wallet.ErrInsufficientFunds},
		}
		for name, c := range cases {
			if err := h.table.PlaceBet(context.Background(), c.bet); !errors.Is(err, c.want) {
				t.Errorf("%s: %v, want %v", name, err, c.want)
			}
		}
		if b, _ := h.wallet.Balance("user-1"); b != testInitialCents {
			t.Errorf("balance after rejected bets = %d, want %d", b, testInitialCents)
		}

		if err := h.bet(r, "user-1", 10); err != nil {
			t.Fatalf("valid bet: %v", err)
		}
		if err := h.bet(r, "user-1", 10); !errors.Is(err, ErrDuplicateBet) {
			t.Errorf("second bet: %v, want ErrDuplicateBet", err)
		}
		if b, _ := h.wallet.Balance("user-1"); b != testInitialCents-10*CentsPerUnit {
			t.Errorf("balance = %d, want %d", b, testInitialCents-10*CentsPerUnit)
		}

		time.Sleep(time.Until(r.BettingClosesAt()))
		synctest.Wait()
		if err := h.bet(r, "user-2", 1); !errors.Is(err, ErrBettingClosed) {
			t.Errorf("bet after the close: %v, want ErrBettingClosed", err)
		}
	})
}

func TestTableRefundsWhenTheBetIsNotStored(t *testing.T) {
	writeFailure := errors.New("redis unavailable")
	cases := map[string]struct {
		writer     func(*memStore) *memWriter
		retryError error
	}{
		"write fails": {
			func(st *memStore) *memWriter { return &memWriter{store: st, writeErr: writeFailure} },
			ErrDuplicateBet,
		},
		"submit fails": {
			func(st *memStore) *memWriter { return &memWriter{store: st, submitErr: writeFailure} },
			writeFailure,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				st := newMemStore()
				h := startTableWith(t, defaultTestConfig(), 1, st, c.writer(st))
				r := h.waitUntilOpen(t)

				if err := h.bet(r, "user-1", 10); !errors.Is(err, writeFailure) {
					t.Fatalf("bet: %v, want the write error", err)
				}
				if b, _ := h.wallet.Balance("user-1"); b != testInitialCents {
					t.Errorf("balance = %d, want the stake refunded (%d)", b, testInitialCents)
				}
				if err := h.bet(r, "user-1", 10); !errors.Is(err, c.retryError) {
					t.Errorf("retry: %v, want %v", err, c.retryError)
				}
				if b, _ := h.wallet.Balance("user-1"); b != testInitialCents {
					t.Errorf("balance after retry = %d, want %d", b, testInitialCents)
				}
			})
		})
	}
}

func TestTestRoundCancelsTheRoundInProgress(t *testing.T) {
	advance := map[RoundState]func(*testing.T, *harness) Round{
		StateCreated: func(t *testing.T, h *harness) Round {
			synctest.Wait()
			return h.activeRound(t)
		},
		StateOpen: func(t *testing.T, h *harness) Round {
			r := h.waitUntilOpen(t)
			placeBets(t, h, r)
			return r
		},
		StateRunning: func(t *testing.T, h *harness) Round {
			r := h.waitUntilOpen(t)
			placeBets(t, h, r)
			time.Sleep(time.Until(r.BettingClosesAt()) + time.Second)
			synctest.Wait()
			return h.activeRound(t)
		},
	}
	for state, reach := range advance {
		t.Run(string(state), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := startTable(t, defaultTestConfig(), 3, nil)
				old := reach(t, h)
				if old.State != state {
					t.Fatalf("round state = %s, want %s", old.State, state)
				}

				now := time.Now()
				test, err := h.table.StartTestRound(context.Background())
				if err != nil {
					t.Fatalf("StartTestRound: %v", err)
				}
				if !test.Test || test.State != StateOpen || !test.StartTime.Equal(now) || test.BettingWindow != h.cfg.TestBettingWindow {
					t.Fatalf("test round = %+v, want open now with the test window", test)
				}
				if active := h.activeRound(t); active.ID != test.ID {
					t.Fatalf("active round = %d, want the test round %d", active.ID, test.ID)
				}
				if _, err := h.store.CountBets(context.Background(), old.ID); !errors.Is(err, ErrRoundNotFound) {
					t.Errorf("cancelled round still stored: %v", err)
				}
				for i := 1; i <= 3; i++ {
					if b, _ := h.wallet.Balance(wallet.UserID(i)); b != testInitialCents {
						t.Errorf("%s balance = %d, want the stake refunded (%d)", wallet.UserID(i), b, testInitialCents)
					}
				}

				if err := h.bet(test, "user-1", 5); err != nil {
					t.Fatalf("bet on the test round: %v", err)
				}
				landsAt := test.BettingClosesAt().Add(h.cfg.PreFlightWait + FlightDuration(200, h.cfg.FlightGrowth))
				time.Sleep(time.Until(landsAt))
				synctest.Wait()

				history, _ := h.store.History(context.Background())
				if len(history) != 1 || history[0].RoundID != test.ID {
					t.Errorf("history = %+v, want only the test round", history)
				}
				next := h.activeRound(t)
				if next.Test || next.State != StateCreated || next.BettingWindow != h.cfg.BettingWindow || !next.StartTime.Equal(landsAt.Add(h.cfg.RoundPause)) {
					t.Errorf("round after the test round = %+v, want default timings", next)
				}
			})
		})
	}
}

func placeBets(t *testing.T, h *harness, r Round) {
	t.Helper()
	for i := 1; i <= 3; i++ {
		if err := h.bet(r, wallet.UserID(i), 5); err != nil {
			t.Fatalf("bet from %s: %v", wallet.UserID(i), err)
		}
	}
}

func TestTableRejectsCommandsAfterShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := startTable(t, defaultTestConfig(), 1, nil)
		r := h.waitUntilOpen(t)
		h.stop()

		if err := h.bet(r, "user-1", 1); !errors.Is(err, ErrTableClosed) {
			t.Errorf("bet after shutdown: %v, want ErrTableClosed", err)
		}
		if _, err := h.table.StartTestRound(context.Background()); !errors.Is(err, ErrTableClosed) {
			t.Errorf("test round after shutdown: %v, want ErrTableClosed", err)
		}
	})
}
