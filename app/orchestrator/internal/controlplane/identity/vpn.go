// Package identity distinguishes container roles from lifecycle ownership.
package identity

import "strings"

const DynamicVPNLabel = "acestream.vpn.dynamic"

func DynamicVPNName(name string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/")), "gluetun-dyn-")
}

func VPNRole(name string, labels map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(labels["role"]), "vpn_node") || DynamicVPNName(name)
}

func ManagedVPN(name string, labels map[string]string) bool {
	return labels["acestream-orchestrator.managed"] == "true" && VPNRole(name, labels)
}

// OwnedVPN requires management labels even for legacy name-based discovery.
// The name fallback supports containers provisioned before DynamicVPNLabel.
func OwnedVPN(name string, labels map[string]string) bool {
	return ManagedVPN(name, labels) && labels["role"] == "vpn_node" &&
		(labels[DynamicVPNLabel] == "true" || DynamicVPNName(name))
}
