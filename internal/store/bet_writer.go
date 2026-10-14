package store

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"aviator/internal/game"
)

var _ game.BetWriter = (*BatchBetWriter)(nil)

const flushTimeout = 5 * time.Second

type pendingBet struct {
	bet     game.Bet
	done    func(error)
	barrier chan struct{}
}

type BatchBetWriter struct {
	client  *redis.Client
	maxSize int
	maxWait time.Duration

	in      chan pendingBet
	mu      sync.RWMutex
	closed  bool
	stopped chan struct{}
}

func NewBatchBetWriter(client *redis.Client, maxSize int, maxWait time.Duration) *BatchBetWriter {
	return &BatchBetWriter{
		client:  client,
		maxSize: maxSize,
		maxWait: maxWait,
		in:      make(chan pendingBet, maxSize*4),
		stopped: make(chan struct{}),
	}
}

func (w *BatchBetWriter) Submit(ctx context.Context, bet game.Bet, done func(error)) error {
	return w.enqueue(ctx, pendingBet{bet: bet, done: done})
}

func (w *BatchBetWriter) Flush(ctx context.Context) error {
	barrier := make(chan struct{})
	if err := w.enqueue(ctx, pendingBet{barrier: barrier}); err != nil {
		return err
	}
	select {
	case <-barrier:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *BatchBetWriter) enqueue(ctx context.Context, p pendingBet) error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.closed {
		return game.ErrWriterClosed
	}
	select {
	case w.in <- p:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *BatchBetWriter) Close() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.in)
	}
	w.mu.Unlock()
	<-w.stopped
}

func (w *BatchBetWriter) Run() {
	defer close(w.stopped)

	batch := make([]pendingBet, 0, w.maxSize)
	var timer *time.Timer
	var deadline <-chan time.Time

	flush := func() {
		w.write(batch)
		batch = batch[:0]
		if timer != nil {
			timer.Stop()
		}
		deadline = nil
	}

	for {
		select {
		case p, ok := <-w.in:
			if !ok {
				flush()
				return
			}
			if p.barrier != nil {
				flush()
				close(p.barrier)
				continue
			}
			batch = append(batch, p)
			if len(batch) == 1 {
				timer = time.NewTimer(w.maxWait)
				deadline = timer.C
			}
			if len(batch) >= w.maxSize {
				flush()
			}
		case <-deadline:
			flush()
		}
	}
}

func (w *BatchBetWriter) write(batch []pendingBet) {
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()

	pipe := w.client.Pipeline()
	cmds := make([]*redis.BoolCmd, len(batch))
	for i, p := range batch {
		cmds[i] = pipe.HSetNX(ctx, betsKey(p.bet.RoundID), p.bet.UserID, encodeBet(p.bet))
	}
	_, _ = pipe.Exec(ctx)

	for i, p := range batch {
		inserted, err := cmds[i].Result()
		switch {
		case err != nil:
			p.done(err)
		case !inserted:
			p.done(game.ErrDuplicateBet)
		default:
			p.done(nil)
		}
	}
}
