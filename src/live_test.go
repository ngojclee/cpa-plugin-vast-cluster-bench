package main

import (
	"os"
	"testing"
)

// TestDecodeLiveBlock parses the exact plugin block present on the CPA host
// (including the nested store: object added by the Plugin Store installer).
func TestDecodeLiveBlock(t *testing.T) {
	raw, err := os.ReadFile("live_cfg.yaml")
	if err != nil {
		t.Skip("no live config fixture: " + err.Error())
	}
	cfg := decodeSettings(raw)
	if cfg.ProbeInterval != "5m" {
		t.Errorf("probe-interval = %q, want 5m", cfg.ProbeInterval)
	}
	t.Logf("nodes=%d interval=%s days=%d tunnel=%s", len(cfg.Nodes), cfg.ProbeInterval, cfg.HistoryDays, cfg.TunnelDir)
}
