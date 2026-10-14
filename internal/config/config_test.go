package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c != Default() {
		t.Fatalf("without environment variables expected Default(), got %+v", c)
	}
	if c.Users != 10 || c.InitialBalanceCents != 10000 {
		t.Errorf("default users and balance: %d users, %d cents", c.Users, c.InitialBalanceCents)
	}
	if c.RoundPause != 3*time.Second || c.BettingWindow != 5*time.Second ||
		c.PreFlightWait != 2*time.Second || c.TestBettingWindow != 15*time.Second {
		t.Errorf("wrong default durations: %+v", c)
	}
}

func TestLoadOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"USERS":           "5000",
		"INITIAL_BALANCE": "250",
		"BETTING_WINDOW":  "800ms",
		"LANDING_MAX":     "2.35",
		"REDIS_ADDR":      "redis:6379",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Users != 5000 {
		t.Errorf("Users = %d, want 5000", c.Users)
	}
	if c.InitialBalanceCents != 25000 {
		t.Errorf("InitialBalanceCents = %d, want 25000", c.InitialBalanceCents)
	}
	if c.BettingWindow != 800*time.Millisecond {
		t.Errorf("BettingWindow = %v, want 800ms", c.BettingWindow)
	}
	if c.LandingMax != 235 {
		t.Errorf("LandingMax = %d, want 235", c.LandingMax)
	}
	if c.RedisAddr != "redis:6379" {
		t.Errorf("RedisAddr = %q", c.RedisAddr)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"malformed integer":      {"USERS": "ten"},
		"malformed duration":     {"BETTING_WINDOW": "5"},
		"no users":               {"USERS": "0"},
		"inverted landing range": {"LANDING_MIN": "5", "LANDING_MAX": "2"},
		"landing below 1x":       {"LANDING_MIN": "0.5"},
	}
	for name, vars := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(vars)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
