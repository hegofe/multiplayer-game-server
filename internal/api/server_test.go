package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aviator/internal/game"
	"aviator/internal/wallet"
)

type fakeTable struct {
	betErr   error
	testErr  error
	lastBet  game.Bet
	testRnd  game.Round
	betCalls int
}

func (f *fakeTable) PlaceBet(_ context.Context, b game.Bet) error {
	f.betCalls++
	f.lastBet = b
	if err := b.Validate(); err != nil {
		return err
	}
	return f.betErr
}

func (f *fakeTable) StartTestRound(context.Context) (game.Round, error) {
	return f.testRnd, f.testErr
}

type fakeStore struct {
	active    game.Round
	activeErr error
	counts    map[int64]int64
	history   []game.RoundResult
	pingErr   error
}

func (f *fakeStore) ActiveRound(context.Context) (game.Round, error) { return f.active, f.activeErr }

func (f *fakeStore) CountBets(_ context.Context, id int64) (int64, error) {
	n, ok := f.counts[id]
	if !ok {
		return 0, game.ErrRoundNotFound
	}
	return n, nil
}

func (f *fakeStore) History(context.Context) ([]game.RoundResult, error) { return f.history, nil }

func (f *fakeStore) Ping(context.Context) error { return f.pingErr }

type fixture struct {
	server *Server
	table  *fakeTable
	store  *fakeStore
	wallet *wallet.Wallet
}

func newFixture() *fixture {
	start := time.Date(2026, 10, 14, 12, 0, 3, 0, time.UTC)
	tbl := &fakeTable{testRnd: game.Round{ID: 9, State: game.StateOpen, StartTime: start, BettingWindow: 15 * time.Second, Test: true}}
	st := &fakeStore{
		active:  game.Round{ID: 8, State: game.StateCreated, StartTime: start, BettingWindow: 5 * time.Second},
		counts:  map[int64]int64{8: 42},
		history: []game.RoundResult{{RoundID: 7, LandingPoint: 247}, {RoundID: 6, LandingPoint: 100}},
	}
	w := wallet.New(2, 10000)
	return &fixture{server: New(tbl, st, w), table: tbl, store: st, wallet: w}
}

func (f *fixture) do(method, path, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Body)
	return rec.Code, string(out)
}

func expect(t *testing.T, code int, body string, wantCode int, wantBody string) {
	t.Helper()
	if code != wantCode || !strings.Contains(body, wantBody) {
		t.Errorf("got %d %s, want %d containing %s", code, body, wantCode, wantBody)
	}
}

func TestHealth(t *testing.T) {
	f := newFixture()
	code, body := f.do("GET", "/health", "")
	expect(t, code, body, http.StatusOK, `"status":"ok"`)

	f.store.pingErr = errors.New("connection refused")
	code, body = f.do("GET", "/health", "")
	expect(t, code, body, http.StatusServiceUnavailable, "redis unavailable")

	f.store.pingErr = nil
	f.server.SetShuttingDown()
	code, body = f.do("GET", "/health", "")
	expect(t, code, body, http.StatusServiceUnavailable, "shutting down")
}

func TestNextRound(t *testing.T) {
	f := newFixture()
	code, body := f.do("GET", "/rounds/next", "")
	expect(t, code, body, http.StatusOK, `{"roundId":8,"state":"created","startTime":"2026-10-14T12:00:03Z","bettingWindowSeconds":5}`)

	f.store.activeErr = game.ErrRoundNotFound
	code, body = f.do("GET", "/rounds/next", "")
	expect(t, code, body, http.StatusNotFound, "round not found")

	f.store.activeErr = errors.New("redis timeout")
	code, body = f.do("GET", "/rounds/next", "")
	expect(t, code, body, http.StatusInternalServerError, "internal error")
}

func TestPlaceBet(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		tableErr error
		wantCode int
		wantBody string
	}{
		{"accepted", `{"roundId":8,"userId":"user-1","multiplier":1.5,"amount":10}`, nil, http.StatusCreated,
			`{"roundId":8,"userId":"user-1","multiplier":1.50,"amount":10,"balance":100.00}`},
		{"malformed json", `{"roundId":`, nil, http.StatusBadRequest, "malformed request body"},
		{"three decimals", `{"roundId":8,"userId":"user-1","multiplier":1.505,"amount":10}`, nil, http.StatusBadRequest, "at most two decimals"},
		{"decimal amount", `{"roundId":8,"userId":"user-1","multiplier":2,"amount":1.5}`, nil, http.StatusBadRequest, "malformed request body"},
		{"missing user", `{"roundId":8,"multiplier":2,"amount":1}`, nil, http.StatusBadRequest, "userId is required"},
		{"target below minimum", `{"roundId":8,"userId":"user-1","multiplier":1,"amount":1}`, nil, http.StatusBadRequest, "at least 1.01"},
		{"betting closed", `{"roundId":8,"userId":"user-1","multiplier":2,"amount":1}`, game.ErrBettingClosed, http.StatusConflict, "closed"},
		{"duplicate", `{"roundId":8,"userId":"user-1","multiplier":2,"amount":1}`, game.ErrDuplicateBet, http.StatusConflict, "already placed"},
		{"another round", `{"roundId":5,"userId":"user-1","multiplier":2,"amount":1}`, game.ErrRoundNotActive, http.StatusConflict, "not the active round"},
		{"insufficient balance", `{"roundId":8,"userId":"user-1","multiplier":2,"amount":500}`, wallet.ErrInsufficientFunds, http.StatusPaymentRequired, "insufficient balance"},
		{"unknown user", `{"roundId":8,"userId":"nobody","multiplier":2,"amount":1}`, wallet.ErrUnknownUser, http.StatusNotFound, "unknown user"},
		{"table saturated", `{"roundId":8,"userId":"user-1","multiplier":2,"amount":1}`, game.ErrTableBusy, http.StatusServiceUnavailable, "saturated"},
		{"shutting down", `{"roundId":8,"userId":"user-1","multiplier":2,"amount":1}`, game.ErrTableClosed, http.StatusServiceUnavailable, "shutting down"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture()
			f.table.betErr = c.tableErr
			code, body := f.do("POST", "/bets", c.body)
			expect(t, code, body, c.wantCode, c.wantBody)
		})
	}

	f := newFixture()
	f.do("POST", "/bets", `{"roundId":8,"userId":"user-2","multiplier":2.35,"amount":7}`)
	if want := (game.Bet{RoundID: 8, UserID: "user-2", Amount: 7, Target: 235}); f.table.lastBet != want {
		t.Errorf("bet passed to the table = %+v, want %+v", f.table.lastBet, want)
	}
}

func TestCountBets(t *testing.T) {
	f := newFixture()
	code, body := f.do("GET", "/rounds/8/bets/count", "")
	if code != http.StatusOK || body != "42" {
		t.Errorf("got %d %q, want 200 \"42\"", code, body)
	}
	code, body = f.do("GET", "/rounds/99/bets/count", "")
	expect(t, code, body, http.StatusNotFound, "round not found")
	code, body = f.do("GET", "/rounds/abc/bets/count", "")
	expect(t, code, body, http.StatusBadRequest, "positive integer")
	code, body = f.do("GET", "/rounds/0/bets/count", "")
	expect(t, code, body, http.StatusBadRequest, "positive integer")
}

func TestHistory(t *testing.T) {
	f := newFixture()
	code, body := f.do("GET", "/rounds/history", "")
	expect(t, code, body, http.StatusOK, `[{"roundId":7,"multiplier":2.47},{"roundId":6,"multiplier":1.00}]`)

	f.store.history = nil
	code, body = f.do("GET", "/rounds/history", "")
	expect(t, code, body, http.StatusOK, `[]`)
}

func TestStartTestRound(t *testing.T) {
	f := newFixture()
	code, body := f.do("POST", "/rounds/test", "")
	expect(t, code, body, http.StatusCreated, `{"roundId":9,"state":"open","startTime":"2026-10-14T12:00:03Z","bettingWindowSeconds":15}`)

	f.table.testErr = game.ErrTableClosed
	code, body = f.do("POST", "/rounds/test", "")
	expect(t, code, body, http.StatusServiceUnavailable, "shutting down")
}

func TestBalance(t *testing.T) {
	f := newFixture()
	if err := f.wallet.Debit("user-1", 1050); err != nil {
		t.Fatal(err)
	}
	code, body := f.do("GET", "/players/user-1/balance", "")
	expect(t, code, body, http.StatusOK, `{"userId":"user-1","balance":89.50}`)

	code, body = f.do("GET", "/players/nobody/balance", "")
	expect(t, code, body, http.StatusNotFound, "unknown user")
}
