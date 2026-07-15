package vpn

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestFetchAllProviders_AssemblesMonolithic verifies that discovery via the
// contents API plus per-provider fetches reassemble into the monolithic
// {"version":N, "<provider>": {"servers":[...]}, ...} shape the rest of the
// pipeline (loadCatalog, SyncCatalogToDB, the gluetun sidecar) expects.
func TestFetchAllProviders_AssemblesMonolithic(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/contents", func(w http.ResponseWriter, _ *http.Request) {
		// README.md must be skipped (not a .json provider file).
		fmt.Fprintf(w, `[
			{"name":"protonvpn.json","download_url":"%s/raw/protonvpn.json"},
			{"name":"mullvad.json","download_url":"%s/raw/mullvad.json"},
			{"name":"README.md","download_url":"%s/raw/README.md"}
		]`, srv.URL, srv.URL, srv.URL)
	})
	mux.HandleFunc("/raw/protonvpn.json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"version":4,"timestamp":111,"servers":[{"hostname":"pa","port_forward":true}]}`)
	})
	mux.HandleFunc("/raw/mullvad.json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"version":4,"timestamp":222,"servers":[{"hostname":"mb"},{"hostname":"mc"}]}`)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	restore := overrideSources(srv.URL+"/contents", srv.URL+"/raw/")
	defer restore()

	s := &ServersRefreshService{}
	payload, err := s.fetchAllProviders(context.Background())
	if err != nil {
		t.Fatalf("fetchAllProviders: %v", err)
	}

	if v, ok := payload["version"]; !ok || v != 1 {
		t.Errorf("top-level version = %v, want 1", payload["version"])
	}
	if _, ok := payload["README"]; ok {
		t.Error("README section should not be present (non-.json file)")
	}

	proton, ok := payload["protonvpn"].(map[string]interface{})
	if !ok {
		t.Fatalf("protonvpn section missing or wrong type: %T", payload["protonvpn"])
	}
	// Per-provider version/timestamp must be preserved inside the section.
	if proton["version"] != float64(4) {
		t.Errorf("protonvpn version = %v, want 4", proton["version"])
	}
	if proton["timestamp"] != float64(111) {
		t.Errorf("protonvpn timestamp = %v, want 111", proton["timestamp"])
	}
	if servers, ok := proton["servers"].([]interface{}); !ok || len(servers) != 1 {
		t.Errorf("protonvpn servers = %v, want 1 entry", proton["servers"])
	}

	mullvad, ok := payload["mullvad"].(map[string]interface{})
	if !ok {
		t.Fatalf("mullvad section missing or wrong type: %T", payload["mullvad"])
	}
	if servers, ok := mullvad["servers"].([]interface{}); !ok || len(servers) != 2 {
		t.Errorf("mullvad servers = %v, want 2 entries", mullvad["servers"])
	}
}

// TestDiscoverProviderSources_Fallback verifies that a failing contents API
// falls back to the hardcoded provider list with space-containing provider
// names URL-encoded in the raw URL.
func TestDiscoverProviderSources_Fallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	restore := overrideSources(srv.URL, "https://raw.example/pkg/servers/")
	defer restore()

	s := &ServersRefreshService{}
	sources := s.discoverProviderSources(context.Background())
	if len(sources) != len(fallbackProviderStems) {
		t.Fatalf("fallback returned %d sources, want %d", len(sources), len(fallbackProviderStems))
	}

	var pia *providerSource
	for i := range sources {
		if sources[i].stem == "private internet access" {
			pia = &sources[i]
			break
		}
	}
	if pia == nil {
		t.Fatal("expected 'private internet access' in fallback sources")
	}
	if !strings.Contains(pia.url, "private%20internet%20access.json") {
		t.Errorf("fallback url not space-encoded: %s", pia.url)
	}
}

func overrideSources(contentsURL, rawBase string) func() {
	prevContents, prevRaw := gluetunServersContentsURL, gluetunServersRawBase
	gluetunServersContentsURL = contentsURL
	gluetunServersRawBase = rawBase
	return func() {
		gluetunServersContentsURL = prevContents
		gluetunServersRawBase = prevRaw
	}
}
