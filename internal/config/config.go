package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port      string
	RedisAddr string

	Users               int
	InitialBalanceCents int64

	RoundPause        time.Duration
	BettingWindow     time.Duration
	PreFlightWait     time.Duration
	TestBettingWindow time.Duration

	LandingMin   int64
	LandingMax   int64
	FlightGrowth float64

	TableQueueSize int
	BatchMaxSize   int
	BatchMaxWait   time.Duration

	ShutdownTimeout time.Duration
}

func Default() Config {
	return Config{
		Port:                "8080",
		RedisAddr:           "localhost:6379",
		Users:               10,
		InitialBalanceCents: 100 * 100,
		RoundPause:          3 * time.Second,
		BettingWindow:       5 * time.Second,
		PreFlightWait:       2 * time.Second,
		TestBettingWindow:   15 * time.Second,
		LandingMin:          100,
		LandingMax:          1000,
		FlightGrowth:        0.2,
		TableQueueSize:      1024,
		BatchMaxSize:        256,
		BatchMaxWait:        5 * time.Millisecond,
		ShutdownTimeout:     10 * time.Second,
	}
}

func FromEnv() (Config, error) {
	return Load(os.Getenv)
}

func Load(getenv func(string) string) (Config, error) {
	c := Default()
	l := loader{getenv: getenv}

	l.str("PORT", &c.Port)
	l.str("REDIS_ADDR", &c.RedisAddr)
	l.int("USERS", &c.Users)
	l.balance("INITIAL_BALANCE", &c.InitialBalanceCents)
	l.duration("ROUND_PAUSE", &c.RoundPause)
	l.duration("BETTING_WINDOW", &c.BettingWindow)
	l.duration("PRE_FLIGHT_WAIT", &c.PreFlightWait)
	l.duration("TEST_BETTING_WINDOW", &c.TestBettingWindow)
	l.multiplier("LANDING_MIN", &c.LandingMin)
	l.multiplier("LANDING_MAX", &c.LandingMax)
	l.float("FLIGHT_GROWTH", &c.FlightGrowth)
	l.int("TABLE_QUEUE_SIZE", &c.TableQueueSize)
	l.int("BATCH_MAX_SIZE", &c.BatchMaxSize)
	l.duration("BATCH_MAX_WAIT", &c.BatchMaxWait)
	l.duration("SHUTDOWN_TIMEOUT", &c.ShutdownTimeout)

	if l.err != nil {
		return Config{}, l.err
	}
	return c, c.validate()
}

func (c Config) validate() error {
	switch {
	case c.Users < 1:
		return fmt.Errorf("USERS must be at least 1")
	case c.InitialBalanceCents < 0:
		return fmt.Errorf("INITIAL_BALANCE must not be negative")
	case c.RoundPause < 0, c.PreFlightWait < 0:
		return fmt.Errorf("ROUND_PAUSE and PRE_FLIGHT_WAIT must not be negative")
	case c.BettingWindow <= 0, c.TestBettingWindow <= 0:
		return fmt.Errorf("BETTING_WINDOW and TEST_BETTING_WINDOW must be positive")
	case c.LandingMin < 100:
		return fmt.Errorf("LANDING_MIN must be at least 1.00")
	case c.LandingMax < c.LandingMin:
		return fmt.Errorf("LANDING_MAX must not be lower than LANDING_MIN")
	case c.FlightGrowth <= 0:
		return fmt.Errorf("FLIGHT_GROWTH must be positive")
	case c.TableQueueSize < 1, c.BatchMaxSize < 1:
		return fmt.Errorf("TABLE_QUEUE_SIZE and BATCH_MAX_SIZE must be at least 1")
	case c.BatchMaxWait <= 0, c.ShutdownTimeout <= 0:
		return fmt.Errorf("BATCH_MAX_WAIT and SHUTDOWN_TIMEOUT must be positive")
	}
	return nil
}

type loader struct {
	getenv func(string) string
	err    error
}

func (l *loader) parse(key string, set func(string) error) {
	v := l.getenv(key)
	if v == "" || l.err != nil {
		return
	}
	if err := set(v); err != nil {
		l.err = fmt.Errorf("invalid %s=%q: %w", key, v, err)
	}
}

func (l *loader) str(key string, dst *string) {
	l.parse(key, func(v string) error { *dst = v; return nil })
}

func (l *loader) int(key string, dst *int) {
	l.parse(key, func(v string) (err error) { *dst, err = strconv.Atoi(v); return })
}

func (l *loader) float(key string, dst *float64) {
	l.parse(key, func(v string) (err error) { *dst, err = strconv.ParseFloat(v, 64); return })
}

func (l *loader) duration(key string, dst *time.Duration) {
	l.parse(key, func(v string) (err error) { *dst, err = time.ParseDuration(v); return })
}

func (l *loader) balance(key string, dst *int64) {
	l.parse(key, func(v string) error {
		n, err := strconv.ParseInt(v, 10, 64)
		*dst = n * 100
		return err
	})
}

func (l *loader) multiplier(key string, dst *int64) {
	l.parse(key, func(v string) error {
		f, err := strconv.ParseFloat(v, 64)
		*dst = int64(math.Round(f * 100))
		return err
	})
}
