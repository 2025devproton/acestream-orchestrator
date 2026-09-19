package docker

import (
	"context"
	"github.com/acestream/acestream/internal/state"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

func TestStopAndReindexPreserveDynamicRecoveryRecord(t *testing.T) {
	r := miniredis.RunT(t)
	cli := redis.NewClient(&redis.Options{Addr: r.Addr()})
	defer cli.Close()
	w := &EventWatcher{pub: state.NewRedisPublisher(cli)}
	for _, eventFirst := range []bool{true, false} {
		name := "gluetun-dyn-retained"
		state.Global.UpsertVPNNode(&state.VPNNode{ContainerName: name, ManagedDynamic: true, Healthy: true})
		now := time.Now().Add(time.Minute)
		if eventFirst {
			w.handleVPNStop(context.Background(), name)
		}
		reconcileVPNPresence(state.Global, map[string]bool{}, now)
		if !eventFirst {
			w.handleVPNStop(context.Background(), name)
		}
		n, ok := state.Global.GetVPNNode(name)
		if !ok || n.Healthy || n.UnhealthySince == nil {
			t.Fatalf("lost recovery record: %+v", n)
		}
		state.Global.RemoveVPNNode(name)
	}
}

func TestClassificationDoesNotEnrollUnmanagedVPN(t *testing.T) {
	if isManagedVPNContainer("external", map[string]string{"role": "vpn_node"}) {
		t.Fatal("role alone grants management")
	}
	if isManagedVPNContainer("gluetun-dyn-external", nil) {
		t.Fatal("name alone grants management")
	}
}
