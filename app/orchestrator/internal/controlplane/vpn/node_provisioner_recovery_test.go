package vpn

import (
	"context"
	"errors"
	"github.com/acestream/acestream/internal/config"
	"github.com/acestream/acestream/internal/state"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
	"reflect"
	"strings"
	"testing"
	"time"
)

type recoveryDockerClient struct {
	containers   []dockertypes.Container
	listErr      error
	removeErrors map[string]error
	removed      []string
}

func (c *recoveryDockerClient) ContainerList(_ context.Context, opts container.ListOptions) ([]dockertypes.Container, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	var result []dockertypes.Container
	for _, item := range c.containers {
		matches := true
		for _, name := range opts.Filters.Get("name") {
			matches = matches && len(item.Names) > 0 && strings.Contains(item.Names[0], name)
		}
		for _, label := range opts.Filters.Get("label") {
			kv := strings.SplitN(label, "=", 2)
			matches = matches && len(kv) == 2 && item.Labels[kv[0]] == kv[1]
		}
		if matches {
			result = append(result, item)
		}
	}
	return result, nil
}
func (c *recoveryDockerClient) ContainerRemove(_ context.Context, id string, _ container.RemoveOptions) error {
	c.removed = append(c.removed, id)
	if err := c.removeErrors[id]; err != nil {
		return err
	}
	for i, item := range c.containers {
		if item.ID == id {
			c.containers = append(c.containers[:i], c.containers[i+1:]...)
			break
		}
	}
	return nil
}
func (c *recoveryDockerClient) Close() error { return nil }
func ownedContainer(name string) dockertypes.Container {
	return dockertypes.Container{ID: "vpn-id", Names: []string{"/" + name}, State: "exited", Labels: map[string]string{"acestream-orchestrator.managed": "true", "role": "vpn_node"}}
}

func TestDestroyNodeCleanupBoundary(t *testing.T) {
	for _, scenario := range []string{"success", "already-missing", "not-found", "list-error", "vpn-error", "engine-error", "static", "wrong-id", "similar-name", "renamed"} {
		t.Run(scenario, func(t *testing.T) {
			name := "gluetun-dyn-review-" + scenario
			creds := NewCredentialManager()
			if err := creds.Configure([]map[string]interface{}{{"id": "cred", "provider": "airvpn", "firewall_vpn_input_ports": "12345"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := creds.AcquireLease(name); err != nil {
				t.Fatal(err)
			}
			port := creds.AcquireAirVPNPort(name)
			if port == 0 {
				t.Fatal("test must reserve an AirVPN port")
			}
			state.Global.UpsertVPNNode(&state.VPNNode{ContainerName: name, ContainerID: "vpn-id", ManagedDynamic: true})
			state.Global.AddEngine(&state.Engine{ContainerID: "engine-id", ContainerName: "engine", VPNContainer: name})
			t.Cleanup(func() { state.Global.RemoveVPNNode(name); state.Global.RemoveEnginesByVPN(name) })
			cfg := config.C.Load()
			engine := dockertypes.Container{ID: "engine-id", Names: []string{"/engine"}, Labels: map[string]string{cfg.ContainerLabelKey: cfg.ContainerLabelVal, "acestream.vpn_container": name}}
			fake := &recoveryDockerClient{containers: []dockertypes.Container{ownedContainer(name), engine}, removeErrors: map[string]error{}}
			wantErr := false
			switch scenario {
			case "already-missing":
				fake.containers = nil
			case "not-found":
				fake.removeErrors["vpn-id"] = errdefs.NotFound(errors.New("gone"))
			case "list-error":
				fake.listErr = errors.New("list failed")
				wantErr = true
			case "vpn-error":
				fake.removeErrors["vpn-id"] = errors.New("remove failed")
				wantErr = true
			case "engine-error":
				fake.removeErrors["engine-id"] = errors.New("engine failed")
				wantErr = true
			case "static":
				fake.containers[0].Labels = map[string]string{"role": "vpn_node"}
				wantErr = true
			case "wrong-id":
				fake.containers[0].ID = "replacement"
				wantErr = true
			case "similar-name":
				fake.containers = []dockertypes.Container{ownedContainer(name + "-other")}
				fake.containers[0].ID = "unrelated-id"
			case "renamed":
				fake.containers[0].Names = []string{"/renamed"}
				wantErr = true
			}
			hookCalls := 0
			p := &Provisioner{creds: creds, dockerFactory: func() (dockerClient, error) { return fake, nil }, onEngineRemoved: func(*state.Engine) { hookCalls++ }}
			err := p.DestroyNode(context.Background(), name)
			if (err != nil) != wantErr {
				t.Fatalf("error=%v want error=%v", err, wantErr)
			}
			_, present := state.Global.GetVPNNode(name)
			if present != wantErr {
				t.Fatalf("state retained=%v want=%v", present, wantErr)
			}
			if wantErr {
				if hookCalls != 0 {
					t.Fatal("engine resources released before confirmed cleanup")
				}
				if creds.AvailableCount() != 0 || creds.AcquireAirVPNPort("other") != 0 {
					t.Fatal("resources released before cleanup")
				}
				if scenario == "engine-error" && !reflect.DeepEqual(fake.removed, []string{"engine-id"}) {
					t.Fatalf("VPN removed before engine cleanup: %v", fake.removed)
				}
			} else {
				if hookCalls != 1 {
					t.Fatal("engine resource cleanup missing")
				}
				if creds.AvailableCount() != 1 || creds.AcquireAirVPNPort("other") != port {
					t.Fatal("resources not released")
				}
				if scenario == "similar-name" && len(fake.removed) != 0 {
					t.Fatal("removed a similarly named container")
				}
				if scenario == "success" && !reflect.DeepEqual(fake.removed, []string{"engine-id", "vpn-id"}) {
					t.Fatalf("cleanup order=%v", fake.removed)
				}
			}
			if scenario == "vpn-error" || scenario == "engine-error" || scenario == "list-error" {
				fake.listErr = nil
				fake.removeErrors = nil
				if err := p.DestroyNode(context.Background(), name); err != nil {
					t.Fatal(err)
				}
				if creds.AvailableCount() != 1 {
					t.Fatal("retry did not release lease")
				}
			}
		})
	}
}

func TestMissingNodeRecoversAfterStartupGrace(t *testing.T) {
	const name = "gluetun-dyn-missing"
	creds := NewCredentialManager()
	_ = creds.Configure([]map[string]interface{}{{"id": "cred"}})
	_, _ = creds.AcquireLease(name)
	state.Global.UpsertVPNNode(&state.VPNNode{ContainerName: name, ManagedDynamic: true, ContainerID: "gone"})
	defer state.Global.RemoveVPNNode(name)
	fake := &recoveryDockerClient{}
	p := &Provisioner{creds: creds, dockerFactory: func() (dockerClient, error) { return fake, nil }}
	lm := NewLifecycleManager(nil, p)
	if err := lm.syncManagedSnapshot(context.Background(), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if creds.AvailableCount() != 0 {
		t.Fatal("startup grace not respected")
	}
	if err := lm.syncManagedSnapshot(context.Background(), nil, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if creds.AvailableCount() != 1 {
		t.Fatal("missing node lease leaked")
	}
	if _, ok := state.Global.GetVPNNode(name); ok {
		t.Fatal("missing node retained after cleanup")
	}
}

func TestLifecycleRecoveryStates(t *testing.T) {
	for _, status := range []string{"exited", "dead", "static", "created", "restarting", "running"} {
		t.Run(status, func(t *testing.T) {
			name := "gluetun-dyn-lifecycle-" + status
			if status == "static" {
				name = "compose-vpn"
			}
			creds := NewCredentialManager()
			_ = creds.Configure([]map[string]interface{}{{"id": "cred"}})
			_, _ = creds.AcquireLease(name)
			state.Global.UpsertVPNNode(&state.VPNNode{ContainerName: name, ContainerID: "vpn-id", ManagedDynamic: status != "static"})
			defer state.Global.RemoveVPNNode(name)
			fake := &recoveryDockerClient{containers: []dockertypes.Container{ownedContainer(name)}}
			fake.containers[0].State = status
			p := &Provisioner{creds: creds, dockerFactory: func() (dockerClient, error) { return fake, nil }}
			lm := NewLifecycleManager(nil, p)
			if err := lm.syncManagedNodesToState(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, present := state.Global.GetVPNNode(name)
			wantPresent := status != "exited" && status != "dead"
			if present != wantPresent {
				t.Fatalf("present=%v expected=%v", present, wantPresent)
			}
			if wantPresent && len(fake.removed) > 0 {
				t.Fatal("nonterminal or static container removed")
			}
		})
	}
}
