package game

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"aviator/internal/wallet"
)

type burstBucket struct {
	OffsetMs int `json:"offsetMs"`
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
}

type burstReport struct {
	Bets            int           `json:"bets"`
	Accepted        int           `json:"accepted"`
	RejectedClosed  int           `json:"rejectedClosed"`
	RejectedOther   int           `json:"rejectedOther"`
	AcceptedLate    int           `json:"acceptedLate"`
	StoredBets      int64         `json:"storedBets"`
	BalanceExpected int64         `json:"balanceExpectedCents"`
	BalanceActual   int64         `json:"balanceActualCents"`
	Buckets         []burstBucket `json:"buckets"`
}

func TestConcurrencyBurstAtClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const users = 1000
		const spreadMs = 50
		h := startTable(t, defaultTestConfig(), users, nil)

		time.Sleep(h.cfg.RoundPause)
		synctest.Wait()
		round := h.activeRound(t)
		if round.State != StateOpen {
			t.Fatalf("round state = %s, want open", round.State)
		}
		closesAt := round.BettingClosesAt()

		offsets := make([]int, users)
		errs := make([]error, users)
		var wg sync.WaitGroup
		for i := range users {
			offsets[i] = i%(2*spreadMs) - spreadMs
			wg.Go(func() {
				time.Sleep(time.Until(closesAt.Add(time.Duration(offsets[i]) * time.Millisecond)))
				errs[i] = h.table.PlaceBet(context.Background(), Bet{
					RoundID: round.ID,
					UserID:  wallet.UserID(i + 1),
					Amount:  1,
					Target:  200,
				})
			})
		}
		wg.Wait()

		rep := burstReport{Bets: users}
		buckets := make([]burstBucket, 2*spreadMs)
		for i := range buckets {
			buckets[i].OffsetMs = i - spreadMs
		}
		for i, err := range errs {
			b := &buckets[offsets[i]+spreadMs]
			switch {
			case err == nil:
				rep.Accepted++
				b.Accepted++
				if offsets[i] >= 0 {
					rep.AcceptedLate++
				}
			case errors.Is(err, ErrBettingClosed):
				rep.RejectedClosed++
				b.Rejected++
			default:
				rep.RejectedOther++
				b.Rejected++
				t.Errorf("bet at %+dms: unexpected error %v", offsets[i], err)
			}
			if offsets[i] < 0 && err != nil {
				t.Errorf("bet %dms before close was rejected: %v", -offsets[i], err)
			}
		}
		rep.Buckets = buckets

		stored, err := h.store.CountBets(context.Background(), round.ID)
		if err != nil {
			t.Fatalf("count bets: %v", err)
		}
		rep.StoredBets = stored
		rep.BalanceExpected = users*testInitialCents - int64(rep.Accepted)*CentsPerUnit
		rep.BalanceActual = h.totalBalance(t, users)

		if rep.AcceptedLate != 0 {
			t.Errorf("%d bets accepted at or after the close", rep.AcceptedLate)
		}
		if rep.StoredBets != int64(rep.Accepted) {
			t.Errorf("stored bets = %d, accepted = %d", rep.StoredBets, rep.Accepted)
		}
		if rep.BalanceActual != rep.BalanceExpected {
			t.Errorf("total balance = %d, want %d", rep.BalanceActual, rep.BalanceExpected)
		}
		t.Logf("burst at close: %d bets, %d accepted, %d rejected as closed, %d accepted late, %d stored, balances consistent=%t",
			rep.Bets, rep.Accepted, rep.RejectedClosed, rep.AcceptedLate, rep.StoredBets, rep.BalanceActual == rep.BalanceExpected)
		writeReport(t, "burst", rep)
	})
}

type sameUserReport struct {
	Attempts        int   `json:"attempts"`
	Accepted        int   `json:"accepted"`
	Duplicates      int   `json:"duplicates"`
	Other           int   `json:"other"`
	StoredBets      int64 `json:"storedBets"`
	BalanceExpected int64 `json:"balanceExpectedCents"`
	BalanceActual   int64 `json:"balanceActualCents"`
}

func TestConcurrencySameUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const attempts = 200
		h := startTable(t, defaultTestConfig(), 1, nil)

		time.Sleep(h.cfg.RoundPause)
		synctest.Wait()
		round := h.activeRound(t)
		user := wallet.UserID(1)

		errs := make([]error, attempts)
		var wg sync.WaitGroup
		for i := range attempts {
			wg.Go(func() {
				errs[i] = h.table.PlaceBet(context.Background(), Bet{RoundID: round.ID, UserID: user, Amount: 5, Target: 150})
			})
		}
		wg.Wait()

		rep := sameUserReport{Attempts: attempts}
		for _, err := range errs {
			switch {
			case err == nil:
				rep.Accepted++
			case errors.Is(err, ErrDuplicateBet):
				rep.Duplicates++
			default:
				rep.Other++
				t.Errorf("unexpected error %v", err)
			}
		}
		stored, err := h.store.CountBets(context.Background(), round.ID)
		if err != nil {
			t.Fatalf("count bets: %v", err)
		}
		rep.StoredBets = stored
		rep.BalanceExpected = testInitialCents - 5*CentsPerUnit
		rep.BalanceActual, _ = h.wallet.Balance(user)

		if rep.Accepted != 1 {
			t.Errorf("accepted = %d, want exactly 1", rep.Accepted)
		}
		if rep.StoredBets != 1 {
			t.Errorf("stored bets = %d, want 1", rep.StoredBets)
		}
		if rep.BalanceActual != rep.BalanceExpected {
			t.Errorf("balance = %d, want %d", rep.BalanceActual, rep.BalanceExpected)
		}
		t.Logf("same user: %d simultaneous attempts, %d accepted, %d rejected as duplicates, debited once=%t",
			rep.Attempts, rep.Accepted, rep.Duplicates, rep.BalanceActual == rep.BalanceExpected)
		writeReport(t, "same_user", rep)
	})
}

type saturationReport struct {
	QueueSize         int     `json:"queueSize"`
	Attempts          int     `json:"attempts"`
	InFlight          int     `json:"inFlight"`
	Queued            int     `json:"queued"`
	RejectedBusy      int     `json:"rejectedBusy"`
	MaxRejectWaitMs   float64 `json:"maxRejectWaitMs"`
	AcceptedAfterward int     `json:"acceptedAfterRelease"`
	Other             int     `json:"other"`
}

func TestConcurrencyQueueSaturation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const queueSize = 16
		const attempts = 500
		cfg := defaultTestConfig()
		cfg.QueueSize = queueSize
		gate := make(chan struct{})
		h := startTable(t, cfg, attempts+1, gate)

		time.Sleep(h.cfg.RoundPause)
		synctest.Wait()
		round := h.activeRound(t)

		var blocker error
		var blockerDone sync.WaitGroup
		blockerDone.Go(func() {
			blocker = h.table.PlaceBet(context.Background(), Bet{RoundID: round.ID, UserID: wallet.UserID(1), Amount: 1, Target: 200})
		})
		synctest.Wait()

		start := time.Now()
		errs := make([]error, attempts)
		waits := make([]time.Duration, attempts)
		var finished sync.WaitGroup
		for i := range attempts {
			finished.Go(func() {
				errs[i] = h.table.PlaceBet(context.Background(), Bet{RoundID: round.ID, UserID: wallet.UserID(i + 2), Amount: 1, Target: 200})
				waits[i] = time.Since(start)
			})
		}
		synctest.Wait()

		rep := saturationReport{QueueSize: queueSize, Attempts: attempts, InFlight: 1}
		for i, err := range errs {
			if errors.Is(err, ErrTableBusy) {
				rep.RejectedBusy++
				rep.MaxRejectWaitMs = max(rep.MaxRejectWaitMs, float64(waits[i])/float64(time.Millisecond))
			}
		}
		rep.Queued = attempts - rep.RejectedBusy

		close(gate)
		finished.Wait()
		blockerDone.Wait()
		if blocker != nil {
			t.Errorf("in-flight bet failed: %v", blocker)
		}
		for _, err := range errs {
			switch {
			case err == nil:
				rep.AcceptedAfterward++
			case errors.Is(err, ErrTableBusy):
			default:
				rep.Other++
				t.Errorf("unexpected error %v", err)
			}
		}

		if rep.Queued != queueSize {
			t.Errorf("queued = %d, want %d", rep.Queued, queueSize)
		}
		if rep.RejectedBusy != attempts-queueSize {
			t.Errorf("rejected as busy = %d, want %d", rep.RejectedBusy, attempts-queueSize)
		}
		if rep.MaxRejectWaitMs != 0 {
			t.Errorf("busy rejections waited up to %.3fms, want immediate", rep.MaxRejectWaitMs)
		}
		if rep.AcceptedAfterward != queueSize {
			t.Errorf("accepted after release = %d, want %d", rep.AcceptedAfterward, queueSize)
		}
		t.Logf("saturation: queue of %d, %d attempts while the table was busy, %d queued, %d rejected immediately, %d queued bets accepted after release",
			rep.QueueSize, rep.Attempts, rep.Queued, rep.RejectedBusy, rep.AcceptedAfterward)
		writeReport(t, "saturation", rep)
	})
}
