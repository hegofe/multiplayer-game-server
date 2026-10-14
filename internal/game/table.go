package game

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

var (
	ErrTableBusy      = errors.New("table is saturated, try again")
	ErrTableClosed    = errors.New("table is shutting down")
	ErrRoundNotActive = errors.New("round is not the active round")
	ErrBettingClosed  = errors.New("betting window is closed")
)

const (
	retryDelay    = time.Second
	cancelTimeout = 5 * time.Second
)

type TableConfig struct {
	RoundPause        time.Duration
	BettingWindow     time.Duration
	PreFlightWait     time.Duration
	TestBettingWindow time.Duration
	FlightGrowth      float64
	QueueSize         int
}

type phase int

const (
	phaseIdle phase = iota
	phaseCreated
	phaseOpen
	phaseWaiting
	phaseFlying
)

type command interface {
	reject(err error)
}

type placeBetCmd struct {
	bet   Bet
	reply chan error
}

func (c placeBetCmd) reject(err error) { c.reply <- err }

type testRoundResult struct {
	round Round
	err   error
}

type testRoundCmd struct {
	reply chan testRoundResult
}

func (c testRoundCmd) reject(err error) { c.reply <- testRoundResult{err: err} }

type Table struct {
	cfg     TableConfig
	store   RoundStore
	writer  BetWriter
	funds   Funds
	landing LandingGenerator
	settler Settler
	log     *slog.Logger

	cmds chan command
	done chan struct{}

	ctx     context.Context
	round   Round
	phase   phase
	bettors map[string]struct{}
	timer   *time.Timer
}

func NewTable(cfg TableConfig, store RoundStore, writer BetWriter, funds Funds, landing LandingGenerator, settler Settler, log *slog.Logger) *Table {
	return &Table{
		cfg:     cfg,
		store:   store,
		writer:  writer,
		funds:   funds,
		landing: landing,
		settler: settler,
		log:     log,
		cmds:    make(chan command, cfg.QueueSize),
		done:    make(chan struct{}),
	}
}

func (t *Table) PlaceBet(ctx context.Context, bet Bet) error {
	if err := bet.Validate(); err != nil {
		return err
	}
	cmd := placeBetCmd{bet: bet, reply: make(chan error, 1)}
	select {
	case t.cmds <- cmd:
	case <-t.done:
		return ErrTableClosed
	default:
		return ErrTableBusy
	}
	select {
	case err := <-cmd.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-t.done:
		return ErrTableClosed
	}
}

func (t *Table) StartTestRound(ctx context.Context) (Round, error) {
	cmd := testRoundCmd{reply: make(chan testRoundResult, 1)}
	select {
	case t.cmds <- cmd:
	case <-ctx.Done():
		return Round{}, ctx.Err()
	case <-t.done:
		return Round{}, ErrTableClosed
	}
	select {
	case res := <-cmd.reply:
		return res.round, res.err
	case <-ctx.Done():
		return Round{}, ctx.Err()
	case <-t.done:
		return Round{}, ErrTableClosed
	}
}

func (t *Table) Done() <-chan struct{} {
	return t.done
}

func (t *Table) Run(ctx context.Context) {
	defer close(t.done)
	defer t.drain()

	t.ctx = ctx
	t.timer = time.NewTimer(time.Hour)
	t.timer.Stop()
	defer t.timer.Stop()

	t.startRound(false)

	for {
		select {
		case <-ctx.Done():
			t.log.Info("table stopped", "round", t.round.ID, "state", t.round.State)
			return
		case cmd := <-t.cmds:
			t.handle(cmd)
		case <-t.timer.C:
			t.advance()
		}
	}
}

func (t *Table) drain() {
	for {
		select {
		case cmd := <-t.cmds:
			cmd.reject(ErrTableClosed)
		default:
			return
		}
	}
}

func (t *Table) handle(cmd command) {
	switch c := cmd.(type) {
	case placeBetCmd:
		t.placeBet(c)
	case testRoundCmd:
		r, err := t.startTestRound()
		c.reply <- testRoundResult{round: r, err: err}
	}
}

func (t *Table) advance() {
	switch t.phase {
	case phaseIdle:
		t.startRound(false)
	case phaseCreated:
		t.openBetting()
	case phaseOpen:
		t.closeBetting()
	case phaseWaiting:
		t.takeOff()
	case phaseFlying:
		t.land()
	}
}

func (t *Table) schedule(p phase, d time.Duration) {
	t.phase = p
	t.timer.Reset(max(d, 0))
}

func (t *Table) retryLater(op string, err error) {
	t.log.Error("table operation failed, retrying", "op", op, "round", t.round.ID, "error", err)
	t.schedule(phaseIdle, retryDelay)
}

func (t *Table) newRound(test bool) (Round, error) {
	id, err := t.store.NextRoundID(t.ctx)
	if err != nil {
		return Round{}, err
	}
	now := time.Now()
	r := Round{ID: id, State: StateCreated, StartTime: now.Add(t.cfg.RoundPause), BettingWindow: t.cfg.BettingWindow}
	if test {
		r.State = StateOpen
		r.StartTime = now
		r.BettingWindow = t.cfg.TestBettingWindow
		r.Test = true
	}
	if err := t.store.ActivateRound(t.ctx, r); err != nil {
		return Round{}, err
	}
	t.round = r
	t.bettors = make(map[string]struct{})
	return r, nil
}

func (t *Table) startRound(test bool) {
	r, err := t.newRound(test)
	if err != nil {
		t.retryLater("create round", err)
		return
	}
	t.log.Info("round created", "round", r.ID, "bettingOpensAt", r.StartTime, "test", r.Test)
	if test {
		t.schedule(phaseOpen, r.BettingWindow)
		return
	}
	t.schedule(phaseCreated, time.Until(r.StartTime))
}

func (t *Table) openBetting() {
	t.round.State = StateOpen
	if err := t.store.SaveRound(t.ctx, t.round); err != nil {
		t.log.Error("could not save round state", "round", t.round.ID, "state", t.round.State, "error", err)
	}
	t.log.Info("betting open", "round", t.round.ID, "closesAt", t.round.BettingClosesAt())
	t.schedule(phaseOpen, time.Until(t.round.BettingClosesAt()))
}

func (t *Table) closeBetting() {
	closedAt := time.Now()
	t.round.State = StateRunning
	t.round.LandingPoint = t.landing.Next()
	if err := t.store.SaveRound(t.ctx, t.round); err != nil {
		t.log.Error("could not save round state", "round", t.round.ID, "state", t.round.State, "error", err)
	}
	t.log.Info("betting closed", "round", t.round.ID, "bets", len(t.bettors))

	flushTimeout := t.cfg.PreFlightWait
	if flushTimeout <= 0 {
		flushTimeout = time.Second
	}
	ctx, cancel := context.WithTimeout(t.ctx, flushTimeout)
	defer cancel()
	if err := t.writer.Flush(ctx); err != nil {
		t.log.Error("pending bets not confirmed before take-off", "round", t.round.ID, "error", err)
	}

	t.schedule(phaseWaiting, t.cfg.PreFlightWait-time.Since(closedAt))
}

func (t *Table) takeOff() {
	flight := FlightDuration(t.round.LandingPoint, t.cfg.FlightGrowth)
	t.log.Info("take-off", "round", t.round.ID)
	t.schedule(phaseFlying, flight)
}

func (t *Table) land() {
	t.round.State = StateClosed
	landed := t.round
	t.log.Info("landed", "round", landed.ID, "multiplier", landed.LandingPoint.String())

	if err := t.store.SaveRound(t.ctx, landed); err != nil {
		t.log.Error("could not save round state", "round", landed.ID, "state", landed.State, "error", err)
	}
	if err := t.store.PushResult(t.ctx, RoundResult{RoundID: landed.ID, LandingPoint: landed.LandingPoint}); err != nil {
		t.log.Error("could not record round result", "round", landed.ID, "error", err)
	}
	t.settler.Settle(landed)
	t.startRound(false)
}

func (t *Table) placeBet(c placeBetCmd) {
	bet := c.bet
	switch {
	case bet.RoundID != t.round.ID:
		c.reply <- ErrRoundNotActive
		return
	case !t.round.AcceptsBetsAt(time.Now()):
		c.reply <- ErrBettingClosed
		return
	}
	if _, ok := t.bettors[bet.UserID]; ok {
		c.reply <- ErrDuplicateBet
		return
	}
	stake := bet.StakeCents()
	if err := t.funds.Debit(bet.UserID, stake); err != nil {
		c.reply <- err
		return
	}
	t.bettors[bet.UserID] = struct{}{}

	done := func(err error) {
		if err != nil {
			t.refund(bet.UserID, stake)
		}
		c.reply <- err
	}
	if err := t.writer.Submit(t.ctx, bet, done); err != nil {
		delete(t.bettors, bet.UserID)
		t.refund(bet.UserID, stake)
		c.reply <- err
	}
}

func (t *Table) refund(user string, cents int64) {
	if err := t.funds.Credit(user, cents); err != nil {
		t.log.Error("refund failed", "user", user, "cents", cents, "error", err)
	}
}

func (t *Table) startTestRound() (Round, error) {
	if t.phase != phaseIdle && t.round.State.InProgress() {
		if err := t.cancelRound(); err != nil {
			return Round{}, err
		}
	}
	r, err := t.newRound(true)
	if err != nil {
		t.retryLater("create test round", err)
		return Round{}, err
	}
	t.log.Info("test round created", "round", r.ID, "closesAt", r.BettingClosesAt())
	t.schedule(phaseOpen, r.BettingWindow)
	return r, nil
}

func (t *Table) cancelRound() error {
	ctx, cancel := context.WithTimeout(t.ctx, cancelTimeout)
	defer cancel()

	cancelled := t.round
	cancelled.State = StateCancelled
	if err := t.store.SaveRound(ctx, cancelled); err != nil {
		return err
	}
	t.round = cancelled
	t.timer.Stop()
	t.phase = phaseIdle

	if err := t.writer.Flush(ctx); err != nil {
		return err
	}
	bets, err := t.store.RoundBets(ctx, cancelled.ID)
	if err != nil {
		return err
	}
	for _, b := range bets {
		t.refund(b.UserID, b.StakeCents())
	}
	if err := t.store.DeleteRound(ctx, cancelled.ID); err != nil {
		t.log.Error("could not delete cancelled round", "round", cancelled.ID, "error", err)
	}
	t.log.Info("round cancelled", "round", cancelled.ID, "refundedBets", len(bets))
	return nil
}
