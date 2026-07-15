package vpn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Gluetun moved its server data out of the main repo (the old monolithic
// internal/storage/servers.json) into the dedicated qdm12/gluetun-servers repo,
// which stores one JSON file per provider under pkg/servers. We list those files
// via the GitHub contents API and fetch each raw file, then reassemble them into
// the monolithic {"version":N, "<provider>": {"servers":[...]}, ...} structure the
// rest of the pipeline (and the gluetun sidecar's /gluetun/servers.json) expects.
// vars (not consts) so tests can point them at a local httptest server.
var (
	gluetunServersContentsURL = "https://api.github.com/repos/qdm12/gluetun-servers/contents/pkg/servers?ref=main"
	gluetunServersRawBase     = "https://raw.githubusercontent.com/qdm12/gluetun-servers/main/pkg/servers/"
)

// fallbackProviderStems is used only when the GitHub contents API listing fails
// (e.g. rate-limited). Filename stems match the gluetun provider names, spaces
// included. Kept in sync manually with the gluetun-servers repo.
var fallbackProviderStems = []string{
	"airvpn", "cyberghost", "expressvpn", "fastestvpn", "giganews",
	"hidemyass", "ipvanish", "ivpn", "mullvad", "nordvpn", "ovpn",
	"perfect privacy", "privado", "private internet access", "privatevpn",
	"protonvpn", "purevpn", "slickvpn", "surfshark", "torguard",
	"vpn unlimited", "vpnsecure", "vyprvpn", "windscribe",
}

// ServersRefreshService periodically downloads the Gluetun official servers list
// and writes it to the configured servers directory.
//
// ProtonVPN server refresh is handled by the Python vpn_servers_refresh.py binary
// since it requires the Proton SRP auth flow and is not latency-sensitive.
type ServersRefreshService struct {
	serversDir string
	rep        *ReputationEngine
	mu         sync.Mutex
	inProgress bool
	lastOK     *bool
	lastErr    string
	lastAt     time.Time

	initialDone chan struct{}
	once        sync.Once
}

func NewServersRefreshService(serversDir string, rep *ReputationEngine) *ServersRefreshService {
	return &ServersRefreshService{
		serversDir:  serversDir,
		rep:         rep,
		initialDone: make(chan struct{}),
	}
}

// Run is the background refresh loop. It respects ctx cancellation.
func (s *ServersRefreshService) Run(ctx context.Context, autoRefresh bool, period time.Duration) {
	slog.Info("VPN servers refresh service started",
		"auto_refresh", autoRefresh,
		"period", period,
	)

	if autoRefresh {
		// Fire an immediate refresh on startup.
		if err := s.RefreshOfficial(ctx); err != nil {
			slog.Warn("Initial VPN servers refresh failed", "err", err)
		}
	} else {
		// Even if auto-refresh is disabled, ensure we have a catalog if it's
		// missing entirely (e.g. first run).
		if !s.rep.IsCatalogAvailable("servers.json") {
			slog.Info("VPN servers catalog missing; performing one-time download")
			if err := s.RefreshOfficial(ctx); err != nil {
				slog.Warn("One-time VPN servers download failed", "err", err)
			}
		}
		// Signal initial done so provisioning is not blocked.
		s.once.Do(func() { close(s.initialDone) })
		<-ctx.Done()
		return
	}

	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("VPN servers refresh service stopped")
			return
		case <-ticker.C:
			if err := s.RefreshOfficial(ctx); err != nil {
				slog.Warn("Scheduled VPN servers refresh failed", "err", err)
			}
		}
	}
}

// WaitForInitialRefresh blocks until the first successful refresh completes or
// timeout elapses. Returns true if refresh completed in time.
func (s *ServersRefreshService) WaitForInitialRefresh(ctx context.Context, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.initialDone:
		return true
	case <-timer.C:
		slog.Warn("Timed out waiting for initial VPN servers refresh", "timeout", timeout)
		return false
	case <-ctx.Done():
		return false
	}
}

// RefreshOfficial downloads the official Gluetun servers list, writes
// servers-official.json, and merges it into servers.json.
func (s *ServersRefreshService) RefreshOfficial(ctx context.Context) error {
	s.mu.Lock()
	if s.inProgress {
		s.mu.Unlock()
		return fmt.Errorf("refresh already in progress")
	}
	s.inProgress = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.inProgress = false
		s.mu.Unlock()
	}()

	slog.Info("Refreshing VPN servers from official Gluetun source")

	payload, err := s.fetchAllProviders(ctx)
	if err != nil {
		s.recordResult(err)
		return err
	}

	dir := s.resolveDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.recordResult(err)
		return err
	}

	officialPath := filepath.Join(dir, "servers-official.json")
	mergedPath := filepath.Join(dir, "servers.json")

	if err := atomicWriteJSON(officialPath, payload); err != nil {
		s.recordResult(err)
		return err
	}

	// Filter protonvpn servers to only include those with port_forward: true.
	if pData, ok := payload["protonvpn"].(map[string]interface{}); ok {
		if servers, ok := pData["servers"].([]interface{}); ok {
			var filtered []interface{}
			for _, s := range servers {
				if server, ok := s.(map[string]interface{}); ok {
					if pf, ok := server["port_forward"].(bool); ok && pf {
						filtered = append(filtered, s)
					}
				}
			}
			pData["servers"] = filtered
			slog.Info("Filtered ProtonVPN official servers", "total", len(servers), "remaining", len(filtered))
		}
	}

	// Update mode: merge into existing servers.json.
	existing := loadExistingJSON(mergedPath)
	if ver, ok := payload["version"]; ok {
		existing["version"] = ver
	}
	for key, val := range payload {
		if key == "version" {
			continue
		}
		// If servers.json already has a protonvpn section from a dedicated
		// Proton refresh (via the Python binary), preserve it rather than
		// overwriting with the potentially stale official list.
		if key == "protonvpn" {
			if _, exists := existing["protonvpn"]; exists {
				continue
			}
		}
		existing[key] = val
	}
	if err := atomicWriteJSON(mergedPath, existing); err != nil {
		s.recordResult(err)
		return err
	}

	slog.Info("VPN servers.json updated",
		"official_file", officialPath,
		"merged_file", mergedPath,
	)

	// Import catalog into DB so the VPN page has rows immediately.
	if s.rep != nil {
		// Invalidate the in-memory catalog cache so the updated file is re-read.
		s.rep.catalog.mu.Lock()
		delete(s.rep.catalog.catalogs, s.rep.catalog.serversJSONPath("servers.json"))
		s.rep.catalog.mu.Unlock()

		if err := s.rep.SyncCatalogToDB(ctx); err != nil {
			slog.Warn("SyncCatalogToDB failed after refresh", "err", err)
		}
	}

	// Sync into the shared Docker volume.
	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := SyncServersToVolume(syncCtx, mergedPath); err != nil {
		slog.Warn("Failed to sync servers.json to Docker volume", "err", err)
	}

	s.recordResult(nil)
	// Signal that the first successful refresh has completed.
	s.once.Do(func() { close(s.initialDone) })
	return nil
}

// Status returns a snapshot of the refresh service state.
func (s *ServersRefreshService) Status() map[string]interface{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := map[string]interface{}{
		"in_progress":  s.inProgress,
		"official_url": gluetunServersContentsURL,
	}
	if !s.lastAt.IsZero() {
		m["last_at"] = s.lastAt
		m["last_ok"] = s.lastOK != nil && *s.lastOK
		m["last_error"] = s.lastErr
	}
	return m
}

func (s *ServersRefreshService) recordResult(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastAt = time.Now().UTC()
	ok := err == nil
	s.lastOK = &ok
	if err != nil {
		s.lastErr = err.Error()
	} else {
		s.lastErr = ""
	}
}

func (s *ServersRefreshService) resolveDir() string {
	if s.serversDir != "" {
		return s.serversDir
	}
	if v := os.Getenv("GLUETUN_SERVERS_JSON_PATH"); v != "" {
		if filepath.Ext(v) == ".json" {
			return filepath.Dir(v)
		}
		return v
	}
	return "."
}

// providerSource is a single provider's JSON file to fetch from the
// gluetun-servers repo. stem is the gluetun provider name (filename without the
// .json suffix, spaces preserved, e.g. "private internet access").
type providerSource struct {
	stem string
	url  string
}

// fetchAllProviders discovers every provider file in the gluetun-servers repo,
// downloads them concurrently, and assembles them into the monolithic
// servers.json structure keyed by provider name. Individual provider failures
// are logged and skipped; only a total failure (zero providers fetched) errors.
func (s *ServersRefreshService) fetchAllProviders(ctx context.Context) (map[string]interface{}, error) {
	sources := s.discoverProviderSources(ctx)
	if len(sources) == 0 {
		return nil, fmt.Errorf("no Gluetun provider sources to fetch")
	}

	type result struct {
		stem string
		obj  map[string]interface{}
		err  error
	}

	const workers = 6
	jobs := make(chan providerSource)
	results := make(chan result)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for src := range jobs {
				obj, err := s.download(ctx, src.url)
				results <- result{stem: src.stem, obj: obj, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, src := range sources {
			select {
			case <-ctx.Done():
				return
			case jobs <- src:
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()

	// Reassemble into the monolithic shape the pipeline expects: each provider's
	// file object ({"version":N,"timestamp":..,"servers":[..]}) becomes a section
	// keyed by provider name, preserving its own version/timestamp.
	payload := map[string]interface{}{"version": 1}
	fetched := 0
	for r := range results {
		if r.err != nil {
			slog.Warn("Failed to fetch Gluetun provider servers", "provider", r.stem, "err", r.err)
			continue
		}
		payload[r.stem] = r.obj
		fetched++
	}

	if fetched == 0 {
		return nil, fmt.Errorf("failed to fetch any Gluetun provider servers")
	}
	slog.Info("Fetched Gluetun provider servers", "providers", fetched, "attempted", len(sources))
	return payload, nil
}

// discoverProviderSources lists provider JSON files via the GitHub contents API,
// falling back to a hardcoded list of raw URLs if the API is unavailable.
func (s *ServersRefreshService) discoverProviderSources(ctx context.Context) []providerSource {
	sources, err := s.listViaContentsAPI(ctx)
	if err != nil {
		slog.Warn("Gluetun servers contents API listing failed; using hardcoded provider list", "err", err)
		return fallbackProviderSources()
	}
	if len(sources) == 0 {
		slog.Warn("Gluetun servers contents API returned no providers; using hardcoded provider list")
		return fallbackProviderSources()
	}
	return sources
}

func fallbackProviderSources() []providerSource {
	out := make([]providerSource, 0, len(fallbackProviderStems))
	for _, stem := range fallbackProviderStems {
		out = append(out, providerSource{
			stem: stem,
			url:  gluetunServersRawBase + url.PathEscape(stem) + ".json",
		})
	}
	return out
}

func (s *ServersRefreshService) listViaContentsAPI(ctx context.Context) ([]providerSource, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gluetunServersContentsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "acestream-orchestrator/1.0")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from contents API", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var entries []struct {
		Name        string `json:"name"`
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parsing contents API response: %w", err)
	}

	var out []providerSource
	for _, e := range entries {
		if !strings.HasSuffix(e.Name, ".json") {
			continue
		}
		stem := strings.TrimSuffix(e.Name, ".json")
		u := e.DownloadURL
		if u == "" {
			u = gluetunServersRawBase + url.PathEscape(stem) + ".json"
		}
		out = append(out, providerSource{stem: stem, url: u})
	}
	return out, nil
}

func (s *ServersRefreshService) download(ctx context.Context, url string) (map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "acestream-orchestrator/1.0")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parsing Gluetun servers JSON: %w", err)
	}
	return payload, nil
}

func atomicWriteJSON(path string, payload map[string]interface{}) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadExistingJSON(path string) map[string]interface{} {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]interface{}{"version": 1}
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]interface{}{"version": 1}
	}
	return m
}
