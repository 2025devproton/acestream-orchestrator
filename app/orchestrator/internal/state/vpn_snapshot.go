package state

// VPN snapshots prevent readers from racing locked health/status updates.
func cloneVPNNode(n *VPNNode) *VPNNode {
	if n == nil {
		return nil
	}
	copy := *n
	if n.UnhealthySince != nil {
		t := *n.UnhealthySince
		copy.UnhealthySince = &t
	}
	if n.HealthySince != nil {
		t := *n.HealthySince
		copy.HealthySince = &t
	}
	if n.DrainingSince != nil {
		t := *n.DrainingSince
		copy.DrainingSince = &t
	}
	return &copy
}
