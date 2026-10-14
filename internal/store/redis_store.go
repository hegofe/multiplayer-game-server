package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"aviator/internal/game"
)

var _ game.RoundStore = (*RedisStore)(nil)

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func (s *RedisStore) NextRoundID(ctx context.Context) (int64, error) {
	return s.client.Incr(ctx, keyRoundSeq).Result()
}

func (s *RedisStore) ActivateRound(ctx context.Context, r game.Round) error {
	_, err := s.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.HSet(ctx, roundKey(r.ID), roundFields(r))
		p.Set(ctx, keyActiveRound, r.ID, 0)
		return nil
	})
	return err
}

func (s *RedisStore) SaveRound(ctx context.Context, r game.Round) error {
	return s.client.HSet(ctx, roundKey(r.ID), roundFields(r)).Err()
}

func (s *RedisStore) ActiveRound(ctx context.Context) (game.Round, error) {
	id, err := s.client.Get(ctx, keyActiveRound).Int64()
	if errors.Is(err, redis.Nil) {
		return game.Round{}, game.ErrRoundNotFound
	}
	if err != nil {
		return game.Round{}, err
	}
	fields, err := s.client.HGetAll(ctx, roundKey(id)).Result()
	if err != nil {
		return game.Round{}, err
	}
	if len(fields) == 0 {
		return game.Round{}, game.ErrRoundNotFound
	}
	return parseRound(id, fields)
}

func (s *RedisStore) CountBets(ctx context.Context, roundID int64) (int64, error) {
	var exists *redis.IntCmd
	var count *redis.IntCmd
	_, err := s.client.Pipelined(ctx, func(p redis.Pipeliner) error {
		exists = p.Exists(ctx, roundKey(roundID))
		count = p.HLen(ctx, betsKey(roundID))
		return nil
	})
	if err != nil {
		return 0, err
	}
	if exists.Val() == 0 {
		return 0, game.ErrRoundNotFound
	}
	return count.Val(), nil
}

func (s *RedisStore) RoundBets(ctx context.Context, roundID int64) ([]game.Bet, error) {
	entries, err := s.client.HGetAll(ctx, betsKey(roundID)).Result()
	if err != nil {
		return nil, err
	}
	bets := make([]game.Bet, 0, len(entries))
	for user, value := range entries {
		b, err := decodeBet(roundID, user, value)
		if err != nil {
			return nil, fmt.Errorf("round %d, user %s: %w", roundID, user, err)
		}
		bets = append(bets, b)
	}
	return bets, nil
}

func (s *RedisStore) DeleteRound(ctx context.Context, roundID int64) error {
	return s.client.Del(ctx, roundKey(roundID), betsKey(roundID)).Err()
}

func (s *RedisStore) PushResult(ctx context.Context, res game.RoundResult) error {
	_, err := s.client.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.LPush(ctx, keyHistory, encodeResult(res))
		p.LTrim(ctx, keyHistory, 0, HistorySize-1)
		return nil
	})
	return err
}

func (s *RedisStore) History(ctx context.Context) ([]game.RoundResult, error) {
	values, err := s.client.LRange(ctx, keyHistory, 0, HistorySize-1).Result()
	if err != nil {
		return nil, err
	}
	results := make([]game.RoundResult, 0, len(values))
	for _, v := range values {
		r, err := decodeResult(v)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

func roundFields(r game.Round) map[string]any {
	test := "0"
	if r.Test {
		test = "1"
	}
	return map[string]any{
		"state":     string(r.State),
		"start":     r.StartTime.UnixMilli(),
		"window_ms": r.BettingWindow.Milliseconds(),
		"landing":   int64(r.LandingPoint),
		"test":      test,
	}
}

func parseRound(id int64, f map[string]string) (game.Round, error) {
	start, err := strconv.ParseInt(f["start"], 10, 64)
	if err != nil {
		return game.Round{}, fmt.Errorf("round %d: malformed start: %w", id, err)
	}
	window, err := strconv.ParseInt(f["window_ms"], 10, 64)
	if err != nil {
		return game.Round{}, fmt.Errorf("round %d: malformed window: %w", id, err)
	}
	landing, err := strconv.ParseInt(f["landing"], 10, 64)
	if err != nil {
		return game.Round{}, fmt.Errorf("round %d: malformed landing: %w", id, err)
	}
	return game.Round{
		ID:            id,
		State:         game.RoundState(f["state"]),
		StartTime:     time.UnixMilli(start),
		BettingWindow: time.Duration(window) * time.Millisecond,
		LandingPoint:  game.Multiplier(landing),
		Test:          f["test"] == "1",
	}, nil
}
