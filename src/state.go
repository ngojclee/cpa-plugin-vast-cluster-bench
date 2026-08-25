package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// HistoryPoint is one stored probe reading for a node.
type HistoryPoint struct {
	TS           int64   `json:"ts"`
	Reachable    bool    `json:"reachable"`
	EngineUp     bool    `json:"engine_up"`
	Model        string  `json:"model"`
	Engine       string  `json:"engine"`
	TTFTS        float64 `json:"ttft_s,omitempty"`
	DecodeTokS   float64 `json:"decode_tok_s,omitempty"`
	PrefillTokS  float64 `json:"prefill_tok_s,omitempty"`
	KVTokens     float64 `json:"kv_cache_tokens,omitempty"`
	KVUsage      float64 `json:"kv_usage,omitempty"`
	Running      float64 `json:"running,omitempty"`
	Queue        float64 `json:"queue,omitempty"`
	CacheHit     float64 `json:"cache_hit,omitempty"`
	RequestsTotal float64 `json:"requests_total,omitempty"`
	PromptTokensTotal float64 `json:"prompt_tokens_total,omitempty"`
	GenTokensTotal float64 `json:"gen_tokens_total,omitempty"`
	ProbeTokens  int     `json:"probe_tokens,omitempty"`
	Status       string  `json:"status"`
	PriceH       float64 `json:"price_h,omitempty"`
}

// NodeState holds the latest reading plus a bounded history ring per node.
type NodeState struct {
	mu       sync.Mutex
	last     *HistoryPoint
	history  []HistoryPoint
	vast     map[string]any
	lastSeen time.Time
}

type pool struct {
	mu         sync.Mutex
	cfg        Settings
	nodes      map[string]*NodeState
	uiSettings map[string]string
	lastProbe  time.Time
	probeBusy  bool
}

var current *pool

func currentPool() *pool {
	if current == nil {
		current = &pool{
			nodes:      make(map[string]*NodeState),
			uiSettings: make(map[string]string),
		}
	}
	return current
}

func (p *pool) reconfigure(cfg Settings) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = cfg
	if len(cfg.Nodes) == 0 {
		// Auto-discovery happens on the next probe cycle; keep any manual
		// entries the user added through the settings page.
		return
	}
	// Keep only configured nodes; remove stale ones.
	keep := make(map[string]bool)
	for _, n := range cfg.Nodes {
		keep[n.Name] = true
		if _, ok := p.nodes[n.Name]; !ok {
			p.nodes[n.Name] = &NodeState{}
		}
	}
	for name := range p.nodes {
		if !keep[name] {
			delete(p.nodes, name)
		}
	}
}

func (p *pool) nodeNames() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.nodes))
	for name := range p.nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (p *pool) setNodeResult(name string, point *HistoryPoint, vast map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.nodes[name]
	if !ok {
		st = &NodeState{}
		p.nodes[name] = st
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	if point != nil {
		point.TS = now.Unix()
		st.last = point
		st.history = append(st.history, *point)
		// Keep ~7 days of 5m probes (2016 points).
		if len(st.history) > 2016 {
			st.history = st.history[len(st.history)-2016:]
		}
	}
	if vast != nil {
		st.vast = vast
	}
	st.lastSeen = now
}

func (p *pool) nodeState(name string) (*NodeState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.nodes[name]
	return st, ok
}

func (p *pool) historyFor(name string) []HistoryPoint {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.nodes[name]
	if !ok {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]HistoryPoint, len(st.history))
	copy(out, st.history)
	return out
}

// pruneHistory removes probe readings older than history-days (default 7).
// Called at the end of every probe cycle; logs how many points were dropped
// so retention is observable in CPA logs.
func (p *pool) pruneHistory() {
	p.mu.Lock()
	days := p.cfg.HistoryDays
	if days <= 0 {
		days = 7
	}
	p.mu.Unlock()

	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	total := 0

	p.mu.Lock()
	for name, st := range p.nodes {
		st.mu.Lock()
		if len(st.history) == 0 {
			st.mu.Unlock()
			continue
		}
		kept := st.history[:0]
		dropped := 0
		for _, pt := range st.history {
			if pt.TS > 0 && pt.TS < cutoff {
				dropped++
				continue
			}
			kept = append(kept, pt)
		}
		st.history = kept
		total += dropped
		st.mu.Unlock()
		if dropped > 0 {
			hostLog("info", "history pruned", map[string]any{"node": name, "dropped": dropped, "days": days})
		}
	}
	p.mu.Unlock()

	if total > 0 {
		hostLog("info", "history retention applied", map[string]any{"total_dropped": total, "days": days})
	}
}

func (p *pool) markProbeStart() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.probeBusy {
		return false
	}
	p.probeBusy = true
	p.lastProbe = time.Now()
	return true
}

func (p *pool) markProbeDone() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.probeBusy = false
}

// stateDir returns the plugin's persisted state directory (settings, history).
func (p *pool) stateDir() string {
	base := p.cfg.AuthDir
	if base == "" {
		base = "/root/.cli-proxy-api"
	}
	return filepath.Join(base, "plugins-data", pluginID)
}

func settingsFilePath(stateDir string) string {
	return filepath.Join(stateDir, "settings.json")
}

type persistedSettings struct {
	VastAPIKey string `json:"vast_api_key,omitempty"`
	SSHKey     string `json:"ssh_key,omitempty"`
}

func (p *pool) updateSettings(body settingsUpdateBody) error {
	dir := p.stateDir()
	if errMkdir := os.MkdirAll(dir, 0o700); errMkdir != nil {
		return errMkdir
	}
	ps := persistedSettings{}
	if raw, errRead := os.ReadFile(settingsFilePath(dir)); errRead == nil {
		_ = json.Unmarshal(raw, &ps)
	}
	if body.Clear {
		// Explicit "use env / path" — drop any stored keys.
		ps.VastAPIKey = ""
		ps.SSHKey = ""
	} else {
		// Empty field = leave unchanged; non-empty = replace.
		if body.VastAPIKey != "" {
			ps.VastAPIKey = body.VastAPIKey
		}
		if body.SSHKey != "" {
			ps.SSHKey = body.SSHKey
		}
	}
	raw, errMarshal := json.MarshalIndent(ps, "", "  ")
	if errMarshal != nil {
		return errMarshal
	}
	tmp := settingsFilePath(dir) + ".tmp"
	if errWrite := os.WriteFile(tmp, raw, 0o600); errWrite != nil {
		return errWrite
	}
	if errRename := os.Rename(tmp, settingsFilePath(dir)); errRename != nil {
		return errRename
	}
	p.mu.Lock()
	if ps.VastAPIKey != "" {
		p.uiSettings["vast_api_key"] = ps.VastAPIKey
	} else {
		delete(p.uiSettings, "vast_api_key")
	}
	if ps.SSHKey != "" {
		p.uiSettings["ssh_key"] = ps.SSHKey
	} else {
		delete(p.uiSettings, "ssh_key")
	}
	p.mu.Unlock()
	hostLog("info", "settings updated", map[string]any{"cleared": body.Clear})
	return nil
}

func (p *pool) loadPersistedSettings() {
	dir := p.stateDir()
	raw, errRead := os.ReadFile(settingsFilePath(dir))
	if errRead != nil {
		return
	}
	var ps persistedSettings
	if errUnmarshal := json.Unmarshal(raw, &ps); errUnmarshal != nil {
		return
	}
	p.mu.Lock()
	if ps.VastAPIKey != "" {
		p.uiSettings["vast_api_key"] = ps.VastAPIKey
	}
	if ps.SSHKey != "" {
		p.uiSettings["ssh_key"] = ps.SSHKey
	}
	p.mu.Unlock()
}

func (p *pool) effectiveKey() string {
	p.mu.Lock()
	ui := p.uiSettings["vast_api_key"]
	p.mu.Unlock()
	if ui != "" {
		return ui
	}
	return p.cfg.Key()
}

func (p *pool) effectiveSSHKey() (string, string) {
	p.mu.Lock()
	ui := p.uiSettings["ssh_key"]
	p.mu.Unlock()
	if ui != "" {
		return "", ui // PEM content inline
	}
	return p.cfg.SSHKey(), ""
}

// buildSettings returns the settings view for the management API (no secrets).
func buildSettings() map[string]any {
	p := currentPool()
	p.mu.Lock()
	defer p.mu.Unlock()
	uiHasKey := p.uiSettings["vast_api_key"] != ""
	uiHasSSH := p.uiSettings["ssh_key"] != ""
	return map[string]any{
		"version":              pluginVersion,
		"probe_interval":       p.cfg.ProbeInterval,
		"history_days":         p.cfg.HistoryDays,
		"tunnel_dir":           p.cfg.TunnelDirResolved(),
		"ssh_key_path":         p.cfg.SSHKey(),
		"ssh_user":             p.cfg.User(),
		"vast_api_key_env":     os.Getenv("VAST_API_KEY") != "",
		"vast_api_key_ui":      uiHasKey,
		"ssh_key_ui":           uiHasSSH,
		"nodes_configured":     len(p.cfg.Nodes),
		"last_probe":           p.lastProbe.Format(time.RFC3339),
		"probe_busy":           p.probeBusy,
	}
}
