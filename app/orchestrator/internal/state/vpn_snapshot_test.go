package state

import (
	"sync"
	"testing"
	"time"
)

func TestVPNUnhealthyTransitionsAndSnapshots(t *testing.T) {
	s := newStore()
	since := time.Now().Add(-time.Hour)
	s.UpsertVPNNode(&VPNNode{ContainerName: "vpn", ContainerID: "one", UnhealthySince: &since})
	s.UpsertVPNNode(&VPNNode{ContainerName: "vpn", ContainerID: "one"})
	n, _ := s.GetVPNNode("vpn")
	if n.UnhealthySince == nil || !n.UnhealthySince.Equal(since) {
		t.Fatal("same-container upsert reset health interval")
	}
	*n.UnhealthySince = time.Now()
	n.Status = "mutated"
	stored, _ := s.GetVPNNode("vpn")
	if !stored.UnhealthySince.Equal(since) || stored.Status == "mutated" {
		t.Fatal("snapshot aliases store")
	}
	s.SetVPNNodeHealthy("vpn", true)
	n, _ = s.GetVPNNode("vpn")
	if n.UnhealthySince != nil || n.HealthySince == nil {
		t.Fatal("recovery did not clear unhealthy interval")
	}
	s.UpsertVPNNode(&VPNNode{ContainerName: "vpn", ContainerID: "two"})
	n, _ = s.GetVPNNode("vpn")
	if n.UnhealthySince == nil || !n.UnhealthySince.After(since) {
		t.Fatal("replacement must get fresh grace period")
	}
}

func TestVPNReadsDoNotRaceStatusUpdates(t *testing.T) {
	s := newStore()
	s.UpsertVPNNode(&VPNNode{ContainerName: "vpn", ManagedDynamic: true})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s.SetVPNNodeHealthy("vpn", i%2 == 0)
			s.SetVPNNodeStatus("vpn", "running")
		}
	}()
	for i := 0; i < 100; i++ {
		for _, n := range s.ListVPNNodes() {
			_ = n.Status
			_ = n.Healthy
		}
		n, _ := s.GetVPNNode("vpn")
		_ = n.UnhealthySince
	}
	wg.Wait()
}
