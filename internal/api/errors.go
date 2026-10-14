package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"aviator/internal/game"
	"aviator/internal/wallet"
)

var (
	errMalformedBody  = errors.New("malformed request body")
	errInvalidRoundID = errors.New("roundId must be a positive integer")
)

func statusFor(err error) int {
	switch {
	case errors.Is(err, errMalformedBody),
		errors.Is(err, errInvalidRoundID),
		errors.Is(err, game.ErrMissingRound),
		errors.Is(err, game.ErrMissingUser),
		errors.Is(err, game.ErrInvalidAmount),
		errors.Is(err, game.ErrInvalidTarget),
		errors.Is(err, game.ErrMalformedMultiplier):
		return http.StatusBadRequest
	case errors.Is(err, wallet.ErrInsufficientFunds):
		return http.StatusPaymentRequired
	case errors.Is(err, wallet.ErrUnknownUser),
		errors.Is(err, game.ErrRoundNotFound):
		return http.StatusNotFound
	case errors.Is(err, game.ErrRoundNotActive),
		errors.Is(err, game.ErrBettingClosed),
		errors.Is(err, game.ErrDuplicateBet):
		return http.StatusConflict
	case errors.Is(err, game.ErrTableBusy),
		errors.Is(err, game.ErrTableClosed),
		errors.Is(err, game.ErrWriterClosed),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError
}

func writeError(c *gin.Context, err error) {
	status := statusFor(err)
	msg := err.Error()
	if status == http.StatusInternalServerError {
		_ = c.Error(err)
		msg = "internal error"
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}
