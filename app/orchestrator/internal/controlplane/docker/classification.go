package docker

import (
	"github.com/acestream/acestream/internal/config"
	"github.com/acestream/acestream/internal/controlplane/identity"
)

// isDynamicVPNContainer identifies the names assigned to dynamically
// provisioned Gluetun nodes. It is deliberately independent of labels because
// Docker lifecycle events can be observed while labels are incomplete.
func isDynamicVPNContainer(containerName string) bool {
	return identity.DynamicVPNName(containerName)
}

// isManagedVPNContainer gives VPN classification priority over the generic
// managed-container label. Compose deployments commonly set that same label
// on engines and VPN nodes, so checking only CONTAINER_LABEL misregisters a
// Gluetun container as an engine.
func isManagedVPNContainer(containerName string, attrs map[string]string) bool {
	return identity.ManagedVPN(containerName, attrs)
}

// isManagedEngineContainer is the single engine-discovery predicate used by
// Docker events and periodic reindexing. VPN containers must never enter the
// engine branch even when they carry the configured engine label.
func isManagedEngineContainer(containerName string, attrs map[string]string, cfg *config.Config) bool {
	if identity.VPNRole(containerName, attrs) {
		return false
	}
	return attrs[cfg.ContainerLabelKey] == cfg.ContainerLabelVal
}
