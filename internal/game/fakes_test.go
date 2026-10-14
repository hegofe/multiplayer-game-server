package game

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"aviator/internal/wallet"
)

type memStore struct {
	mu      sync.Mutex
	seq     int64
	active  int64
	rounds  map[int64]Round
	bets    map[int64]map[string]Bet
	history []RoundResult
}

func newMemStore() *memStore {
	return &memStore{rounds: map[int64]Round{}, bets: map[int64]map[string]Bet{}}
}

func (s *memStore) NextRoundID(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return s.seq, nil
}

func (s *memStore) ActivateRound(_ context.Context, r Round) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rounds[r.ID] = r
	s.active = r.ID
	return nil
}

func (s *memStore) SaveRound(_ context.Context, r Round) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rounds[r.ID] = r
	return nil
}

func (s *memStore) ActiveRound(context.Context) (Round, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rounds[s.active]
	if !ok {
		return Round{}, ErrRoundNotFound
	}
	return r, nil
}

func (s *memStore) CountBets(_ context.Context, roundID int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rounds[roundID]; !ok {
		return 0, ErrRoundNotFound
	}
	return int64(len(s.bets[roundID])), nil
}

func (s *memStore) RoundBets(_ context.Context, roundID int64) ([]Bet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bets := make([]Bet, 0, len(s.bets[roundID]))
	for _, b := range s.bets[roundID] {
		bets = append(bets, b)
	}
	return bets, nil
}

func (s *memStore) DeleteRound(_ context.Context, roundID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rounds, roundID)
	delete(s.bets, roundID)
	return nil
}

func (s *memStore) PushResult(_ context.Context, res RoundResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append([]RoundResult{res}, s.history...)
	return nil
}

func (s *memStore) History(context.Context) ([]RoundResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RoundResult(nil), s.history...), nil
}

func (s *memStore) insertBet(b Bet) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bets[b.RoundID] == nil {
		s.bets[b.RoundID] = map[string]Bet{}
	}
	if _, ok := s.bets[b.RoundID][b.UserID]; ok {
		return false
	}
	s.bets[b.RoundID][b.UserID] = b
	return true
}

type memWriter struct {
	store     *memStore
	gate      chan struct{}
	submitErr error
	writeErr  error

	mu      sync.Mutex
	flushes []time.Time
}

func (w *memWriter) Submit(ctx context.Context, b Bet, done func(error)) error {
	if w.gate != nil {
		select {
		case <-w.gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if w.submitErr != nil {
		return w.submitErr
	}
	switch {
	case w.writeErr != nil:
		done(w.writeErr)
	case w.store.insertBet(b):
		done(nil)
	default:
		done(ErrDuplicateBet)
	}
	return nil
}

func (w *memWriter) Flush(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushes = append(w.flushes, time.Now())
	return nil
}

func (w *memWriter) flushTimes() []time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Time(nil), w.flushes...)
}

type recordingSettler struct {
	mu     sync.Mutex
	rounds []Round
}

func (s *recordingSettler) Settle(r Round) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rounds = append(s.rounds, r)
}

func (s *recordingSettler) settled() []Round {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Round(nil), s.rounds...)
}

type harness struct {
	table   *Table
	store   *memStore
	wallet  *wallet.Wallet
	writer  *memWriter
	settler *recordingSettler
	cfg     TableConfig
	stop    func()
}

const testInitialCents = 100 * CentsPerUnit

func defaultTestConfig() TableConfig {
	return TableConfig{
		RoundPause:        3 * time.Second,
		BettingWindow:     5 * time.Second,
		PreFlightWait:     2 * time.Second,
		TestBettingWindow: 15 * time.Second,
		FlightGrowth:      0.2,
		QueueSize:         4096,
	}
}

func startTable(t *testing.T, cfg TableConfig, users int, gate chan struct{}) *harness {
	t.Helper()
	st := newMemStore()
	return startTableWith(t, cfg, users, st, &memWriter{store: st, gate: gate})
}

func startTableWith(t *testing.T, cfg TableConfig, users int, st *memStore, writer *memWriter) *harness {
	t.Helper()
	w := wallet.New(users, testInitialCents)
	settler := &recordingSettler{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tbl := NewTable(cfg, st, writer, w, NewFixedLanding(200), settler, log)

	ctx, cancel := context.WithCancel(context.Background())
	go tbl.Run(ctx)
	stop := sync.OnceFunc(func() {
		cancel()
		<-tbl.Done()
	})
	t.Cleanup(stop)
	return &harness{table: tbl, store: st, wallet: w, writer: writer, settler: settler, cfg: cfg, stop: stop}
}

func (h *harness) waitUntilOpen(t *testing.T) Round {
	t.Helper()
	synctest.Wait()
	time.Sleep(time.Until(h.activeRound(t).StartTime))
	synctest.Wait()
	r := h.activeRound(t)
	if r.State != StateOpen {
		t.Fatalf("round %d state = %s, want open", r.ID, r.State)
	}
	return r
}

func (h *harness) bet(r Round, user string, amount int64) error {
	return h.table.PlaceBet(context.Background(), Bet{RoundID: r.ID, UserID: user, Amount: amount, Target: 200})
}

func (h *harness) activeRound(t *testing.T) Round {
	t.Helper()
	r, err := h.store.ActiveRound(context.Background())
	if err != nil {
		t.Fatalf("active round: %v", err)
	}
	return r
}

func (h *harness) totalBalance(t *testing.T, users int) int64 {
	t.Helper()
	var total int64
	for i := 1; i <= users; i++ {
		b, err := h.wallet.Balance(wallet.UserID(i))
		if err != nil {
			t.Fatalf("balance of %s: %v", wallet.UserID(i), err)
		}
		total += b
	}
	return total
}

func writeReport(t *testing.T, name string, v any) {
	t.Helper()
	dir := os.Getenv("CONCURRENCY_REPORT_DIR")
	if dir == "" {
		return
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("encode report: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create report dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
}
