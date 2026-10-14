package wallet

import (
	"errors"
	"sync"
	"testing"
)

func TestNewCreatesUsers(t *testing.T) {
	w := New(3, 10000)
	for _, user := range []string{"user-1", "user-2", "user-3"} {
		if b, err := w.Balance(user); err != nil || b != 10000 {
			t.Errorf("%s balance = %d, %v; want 10000", user, b, err)
		}
	}
	if w.Exists("user-4") {
		t.Error("user-4 exists, want only 3 users")
	}
	if _, err := w.Balance("user-4"); !errors.Is(err, ErrUnknownUser) {
		t.Errorf("Balance(user-4) error = %v, want ErrUnknownUser", err)
	}
}

func TestDebitAndCredit(t *testing.T) {
	w := New(1, 1000)
	if err := w.Debit("user-1", 400); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if err := w.Credit("user-1", 250); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	if b, _ := w.Balance("user-1"); b != 850 {
		t.Fatalf("balance = %d, want 850", b)
	}

	cases := map[string]struct {
		op   func() error
		want error
	}{
		"debit more than the balance": {func() error { return w.Debit("user-1", 851) }, ErrInsufficientFunds},
		"debit zero":                  {func() error { return w.Debit("user-1", 0) }, ErrInvalidAmount},
		"credit negative":             {func() error { return w.Credit("user-1", -1) }, ErrInvalidAmount},
		"debit unknown user":          {func() error { return w.Debit("nobody", 1) }, ErrUnknownUser},
		"credit unknown user":         {func() error { return w.Credit("nobody", 1) }, ErrUnknownUser},
	}
	for name, c := range cases {
		if err := c.op(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	if b, _ := w.Balance("user-1"); b != 850 {
		t.Errorf("balance after failed operations = %d, want 850", b)
	}
}

func TestConcurrentDebitsNeverOverdraw(t *testing.T) {
	const attempts = 500
	w := New(1, 10000)

	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := range attempts {
		wg.Go(func() { results[i] = w.Debit("user-1", 100) })
	}
	wg.Wait()

	succeeded := 0
	for _, err := range results {
		switch {
		case err == nil:
			succeeded++
		case !errors.Is(err, ErrInsufficientFunds):
			t.Errorf("unexpected error %v", err)
		}
	}
	if succeeded != 100 {
		t.Errorf("successful debits = %d, want 100", succeeded)
	}
	if b, _ := w.Balance("user-1"); b != 0 {
		t.Errorf("final balance = %d, want 0", b)
	}
}
