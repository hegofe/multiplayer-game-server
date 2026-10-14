package store

import (
	"fmt"
	"strconv"
	"strings"

	"aviator/internal/game"
)

const (
	keyRoundSeq    = "round:seq"
	keyActiveRound = "round:active"
	keyHistory     = "rounds:history"

	HistorySize = 10
)

func roundKey(id int64) string { return "round:" + strconv.FormatInt(id, 10) }

func betsKey(id int64) string { return "bets:" + strconv.FormatInt(id, 10) }

func encodePair(a, b int64) string {
	return strconv.FormatInt(a, 10) + "|" + strconv.FormatInt(b, 10)
}

func decodePair(s string) (int64, int64, error) {
	left, right, ok := strings.Cut(s, "|")
	if !ok {
		return 0, 0, fmt.Errorf("malformed value %q", s)
	}
	a, err := strconv.ParseInt(left, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("malformed value %q: %w", s, err)
	}
	b, err := strconv.ParseInt(right, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("malformed value %q: %w", s, err)
	}
	return a, b, nil
}

func encodeBet(b game.Bet) string {
	return encodePair(b.Amount, int64(b.Target))
}

func decodeBet(roundID int64, user, value string) (game.Bet, error) {
	amount, target, err := decodePair(value)
	if err != nil {
		return game.Bet{}, err
	}
	return game.Bet{RoundID: roundID, UserID: user, Amount: amount, Target: game.Multiplier(target)}, nil
}

func encodeResult(r game.RoundResult) string {
	return encodePair(r.RoundID, int64(r.LandingPoint))
}

func decodeResult(value string) (game.RoundResult, error) {
	id, landing, err := decodePair(value)
	if err != nil {
		return game.RoundResult{}, err
	}
	return game.RoundResult{RoundID: id, LandingPoint: game.Multiplier(landing)}, nil
}
