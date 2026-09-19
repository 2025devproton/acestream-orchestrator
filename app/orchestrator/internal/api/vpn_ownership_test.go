package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/acestream/acestream/internal/state"
)

func TestExternalVPNCannotBeDrainedOrDestroyed(t *testing.T) {
	const name = "external-vpn-review"
	state.Global.UpsertVPNNode(&state.VPNNode{ContainerName: name, Lifecycle: "active"})
	defer state.Global.RemoveVPNNode(name)
	s := &ProxyServer{st: state.Global}
	for _, handler := range []http.HandlerFunc{s.mgHandleDrainVPNNode, s.mgHandleDestroyVPNNode} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		req.SetPathValue("name", name)
		response := httptest.NewRecorder()
		handler(response, req)
		if response.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", response.Code)
		}
		if state.Global.IsVPNNodeDraining(name) {
			t.Fatal("external VPN entered lifecycle cleanup")
		}
	}
}
