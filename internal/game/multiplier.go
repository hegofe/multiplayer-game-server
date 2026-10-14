package game

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Multiplier int64

const MinTarget Multiplier = 101

var ErrMalformedMultiplier = errors.New("multiplier must be a positive number with at most two decimals")

func ParseMultiplier(s string) (Multiplier, error) {
	whole, frac, hasFrac := strings.Cut(strings.TrimSpace(s), ".")
	if whole == "" || len(frac) > 2 || (hasFrac && frac == "") {
		return 0, ErrMalformedMultiplier
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.ParseUint(whole, 10, 32)
	if err != nil {
		return 0, ErrMalformedMultiplier
	}
	f, err := strconv.ParseUint(frac, 10, 8)
	if err != nil {
		return 0, ErrMalformedMultiplier
	}
	return Multiplier(int64(w)*100 + int64(f)), nil
}

func (m Multiplier) String() string {
	return fmt.Sprintf("%d.%02d", int64(m)/100, int64(m)%100)
}

func (m Multiplier) MarshalJSON() ([]byte, error) {
	return []byte(m.String()), nil
}

func (m *Multiplier) UnmarshalJSON(data []byte) error {
	v, err := ParseMultiplier(strings.Trim(string(data), `"`))
	if err != nil {
		return err
	}
	*m = v
	return nil
}
