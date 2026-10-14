package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"aviator/internal/game"
)

const healthTimeout = time.Second

type Table interface {
	PlaceBet(ctx context.Context, bet game.Bet) error
	StartTestRound(ctx context.Context) (game.Round, error)
}

type Store interface {
	ActiveRound(ctx context.Context) (game.Round, error)
	CountBets(ctx context.Context, roundID int64) (int64, error)
	History(ctx context.Context) ([]game.RoundResult, error)
	Ping(ctx context.Context) error
}

type Balances interface {
	Balance(user string) (int64, error)
}

type Server struct {
	table        Table
	store        Store
	balances     Balances
	shuttingDown atomic.Bool
}

func New(table Table, store Store, balances Balances) *Server {
	return &Server{table: table, store: store, balances: balances}
}

func (s *Server) SetShuttingDown() {
	s.shuttingDown.Store(true)
}

func (s *Server) Handler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/health", s.health)
	r.GET("/rounds/next", s.nextRound)
	r.GET("/rounds/history", s.history)
	r.GET("/rounds/:roundId/bets/count", s.countBets)
	r.POST("/rounds/test", s.startTestRound)
	r.POST("/bets", s.placeBet)
	r.GET("/players/:userId/balance", s.balance)
	return r
}

type money int64

func (m money) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf("%d.%02d", int64(m)/game.CentsPerUnit, int64(m)%game.CentsPerUnit)), nil
}

type roundResponse struct {
	RoundID              int64           `json:"roundId"`
	State                game.RoundState `json:"state"`
	StartTime            time.Time       `json:"startTime"`
	BettingWindowSeconds float64         `json:"bettingWindowSeconds"`
}

func toRoundResponse(r game.Round) roundResponse {
	return roundResponse{
		RoundID:              r.ID,
		State:                r.State,
		StartTime:            r.StartTime.UTC(),
		BettingWindowSeconds: r.BettingWindow.Seconds(),
	}
}

type betRequest struct {
	RoundID    int64           `json:"roundId"`
	UserID     string          `json:"userId"`
	Multiplier game.Multiplier `json:"multiplier"`
	Amount     int64           `json:"amount"`
}

type betResponse struct {
	RoundID    int64           `json:"roundId"`
	UserID     string          `json:"userId"`
	Multiplier game.Multiplier `json:"multiplier"`
	Amount     int64           `json:"amount"`
	Balance    money           `json:"balance"`
}

type resultResponse struct {
	RoundID    int64           `json:"roundId"`
	Multiplier game.Multiplier `json:"multiplier"`
}

type balanceResponse struct {
	UserID  string `json:"userId"`
	Balance money  `json:"balance"`
}

func (s *Server) health(c *gin.Context) {
	if s.shuttingDown.Load() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "shutting down"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), healthTimeout)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "redis unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) nextRound(c *gin.Context) {
	r, err := s.store.ActiveRound(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toRoundResponse(r))
}

func (s *Server) history(c *gin.Context) {
	results, err := s.store.History(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	out := make([]resultResponse, len(results))
	for i, r := range results {
		out[i] = resultResponse{RoundID: r.RoundID, Multiplier: r.LandingPoint}
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) countBets(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("roundId"), 10, 64)
	if err != nil || id <= 0 {
		writeError(c, errInvalidRoundID)
		return
	}
	n, err := s.store.CountBets(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, n)
}

func (s *Server) startTestRound(c *gin.Context) {
	r, err := s.table.StartTestRound(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toRoundResponse(r))
}

func (s *Server) placeBet(c *gin.Context) {
	var req betRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeError(c, fmt.Errorf("%w: %v", errMalformedBody, err))
		return
	}
	bet := game.Bet{RoundID: req.RoundID, UserID: req.UserID, Amount: req.Amount, Target: req.Multiplier}
	if err := s.table.PlaceBet(c.Request.Context(), bet); err != nil {
		writeError(c, err)
		return
	}
	balance, err := s.balances.Balance(bet.UserID)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, betResponse{
		RoundID:    bet.RoundID,
		UserID:     bet.UserID,
		Multiplier: bet.Target,
		Amount:     bet.Amount,
		Balance:    money(balance),
	})
}

func (s *Server) balance(c *gin.Context) {
	user := c.Param("userId")
	b, err := s.balances.Balance(user)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, balanceResponse{UserID: user, Balance: money(b)})
}
