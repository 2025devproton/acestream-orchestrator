package config

import (
	"testing"
	"time"
)

func TestRecoveryConfiguration(t *testing.T) {
	t.Setenv("STREAM_STALL_TIMEOUT_S", "0")
	if load().StreamStallTimeout != 0 {
		t.Fatal("zero must disable recovery")
	}
	t.Setenv("STREAM_STALL_TIMEOUT_S", "-1")
	t.Setenv("STREAM_STALL_CHECK_INTERVAL_S", "0")
	t.Setenv("STREAM_STALL_COOLDOWN_S", "-1s")
	c := load()
	if c.StreamStallTimeout != 0 || c.StreamStallCheckInterval != 5*time.Second || c.StreamStallCooldown != 30*time.Second {
		t.Fatal("invalid values did not use safe defaults")
	}
	t.Setenv("STREAM_STALL_TIMEOUT_S", "25")
	if load().StreamStallTimeout != 25*time.Second {
		t.Fatal("seconds parsing failed")
	}
}
