package state

import "testing"

func TestUpsertVPNNodeInitializesAndPreservesUnhealthySince(t *testing.T) {
	s := newStore()
	node := &VPNNode{ContainerName: "gluetun-dyn-test", ManagedDynamic: true}
	s.UpsertVPNNode(node)
	first, _ := s.GetVPNNode(node.ContainerName)
	if first.UnhealthySince == nil {
		t.Fatal("unhealthy node did not get an unhealthy start time")
	}
	started := *first.UnhealthySince
	s.SetVPNNodeHealthy(node.ContainerName, false)
	current, _ := s.GetVPNNode(node.ContainerName)
	if current.UnhealthySince == nil || !current.UnhealthySince.Equal(started) {
		t.Fatalf("UnhealthySince changed: got %v want %v", current.UnhealthySince, started)
	}
}
