package docker

import (
	"testing"

	"github.com/acestream/acestream/internal/config"
)

func TestManagedContainerClassification(t *testing.T) {
	cfg := &config.Config{
		ContainerLabelKey: "acestream-orchestrator.managed",
		ContainerLabelVal: "true",
	}

	tests := []struct {
		name       string
		attrs      map[string]string
		wantVPN    bool
		wantEngine bool
	}{
		{
			name:       "gluetun-dyn-123",
			attrs:      map[string]string{"acestream-orchestrator.managed": "true"},
			wantVPN:    true,
			wantEngine: false,
		},
		{
			name: "acestream-vpn",
			attrs: map[string]string{
				"acestream-orchestrator.managed": "true",
				"role":                           "vpn_node",
			},
			wantVPN:    true,
			wantEngine: false,
		},
		{
			name:       "acestream-engine-123",
			attrs:      map[string]string{"acestream-orchestrator.managed": "true"},
			wantVPN:    false,
			wantEngine: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isManagedVPNContainer(tt.name, tt.attrs); got != tt.wantVPN {
				t.Fatalf("isManagedVPNContainer() = %v, want %v", got, tt.wantVPN)
			}
			if got := isManagedEngineContainer(tt.name, tt.attrs, cfg); got != tt.wantEngine {
				t.Fatalf("isManagedEngineContainer() = %v, want %v", got, tt.wantEngine)
			}
		})
	}
}
