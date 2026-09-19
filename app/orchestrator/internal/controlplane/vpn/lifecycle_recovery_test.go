package vpn

import (
	"testing"
	"time"

	"github.com/acestream/acestream/internal/state"
)

func TestIsTerminalNodeStatus(t *testing.T) {
	for status, want := range map[string]bool{
		"stopped":    true,
		"exited":     true,
		"dead":       true,
		"running":    false,
		"created":    false,
		"restarting": false,
	} {
		if got := isTerminalNodeStatus(status); got != want {
			t.Errorf("isTerminalNodeStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestNodeNeedsHealingPreservesUnhealthySince(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	since := now.Add(-2 * time.Minute)
	node := &state.VPNNode{UnhealthySince: &since, LastSeen: now}
	if !nodeNeedsHealing(node, now, time.Minute) {
		t.Fatal("old UnhealthySince should make node eligible")
	}
	node.LastSeen = now.Add(time.Hour)
	if !nodeNeedsHealing(node, now, time.Minute) {
		t.Fatal("LastSeen must not reset the unhealthy clock")
	}
	if nodeNeedsHealing(&state.VPNNode{}, now, time.Minute) {
		t.Fatal("node without UnhealthySince must not be healed")
	}
}
