package main

import (
	"testing"
)

const testConfigYAML = `
plugins:
  enabled: true
  configs:
    vast-cluster-bench:
      enabled: true
      probe-interval: 1m
      history-days: 14
      tunnel-dir: /vast-tunnel
      ssh-key-path: ""
      ssh-user: root
      nodes:
        - name: G
          id: "48423380"
          ssh-host: 65.95.12.163
          ssh-port: 31027
        - name: F
          id: "48423230"
          ssh-host: 80.251.216.116
          ssh-port: 10048
`

func TestDecodeSettings(t *testing.T) {
	cfg := decodeSettings([]byte(testConfigYAML))
	if len(cfg.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d: %+v", len(cfg.Nodes), cfg)
	}
	if cfg.ProbeInterval != "1m" {
		t.Errorf("probe-interval = %q, want 1m", cfg.ProbeInterval)
	}
	if cfg.HistoryDays != 14 {
		t.Errorf("history-days = %d, want 14", cfg.HistoryDays)
	}
	if cfg.Nodes[0].Name != "G" || cfg.Nodes[0].SSHPort != 31027 {
		t.Errorf("node G wrong: %+v", cfg.Nodes[0])
	}
	t.Logf("OK: %d nodes, interval=%s, days=%d", len(cfg.Nodes), cfg.ProbeInterval, cfg.HistoryDays)
}
