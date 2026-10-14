package wallet

import (
	"errors"
	"strconv"
	"sync"
)

var (
	ErrUnknownUser       = errors.New("unknown user")
	ErrInsufficientFunds = errors.New("insufficient balance")
	ErrInvalidAmount     = errors.New("amount must be positive")
)

type Wallet struct {
	mu       sync.RWMutex
	balances map[string]int64
}

func UserID(n int) string {
	return "user-" + strconv.Itoa(n)
}

func New(users int, initialCents int64) *Wallet {
	w := &Wallet{balances: make(map[string]int64, users)}
	for i := 1; i <= users; i++ {
		w.balances[UserID(i)] = initialCents
	}
	return w
}

func (w *Wallet) Exists(user string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.balances[user]
	return ok
}

func (w *Wallet) Balance(user string) (int64, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	b, ok := w.balances[user]
	if !ok {
		return 0, ErrUnknownUser
	}
	return b, nil
}

func (w *Wallet) Debit(user string, cents int64) error {
	if cents <= 0 {
		return ErrInvalidAmount
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	b, ok := w.balances[user]
	if !ok {
		return ErrUnknownUser
	}
	if b < cents {
		return ErrInsufficientFunds
	}
	w.balances[user] = b - cents
	return nil
}

func (w *Wallet) Credit(user string, cents int64) error {
	if cents <= 0 {
		return ErrInvalidAmount
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	b, ok := w.balances[user]
	if !ok {
		return ErrUnknownUser
	}
	w.balances[user] = b + cents
	return nil
}
