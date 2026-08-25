package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// tunnelInstance is one parsed row from vast-tunnel's instances.txt.
// Format: LOCAL_PORT SSH_HOST SSH_PORT INSTANCE_ID PRICE [MODEL]
type tunnelInstance struct {
	LocalPort  int
	Host       string
	SSHPort    int
	InstanceID string
	Price      float64
	Model      string
}

// instancesTxtPath returns the tunnel-managed instances.txt path
// (auto-discovered from tunnel-dir), or "" when unavailable.
func (p *pool) instancesTxtPath() string {
	dir := p.cfg.TunnelDirResolved()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "instances.txt")
}

// discoverTunnelNodes reads instances.txt written by vast-tunnel /
// vast-gateway and returns NodeConfig entries for every tunnel row.
// This is the "auto-detect via our tunnel" mechanism: any machine the
// tunnel stack manages automatically shows up here, no manual config.
func (p *pool) discoverTunnelNodes() []NodeConfig {
	path := p.instancesTxtPath()
	if path == "" {
		return nil
	}
	f, errOpen := os.Open(path)
	if errOpen != nil {
		hostLog("debug", "instances.txt unavailable", map[string]any{"path": path, "error": errOpen.Error()})
		return nil
	}
	defer f.Close()

	var out []NodeConfig
	seen := make(map[string]bool)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		rec := tunnelInstance{}
		// New stable format: LOCAL_PORT SSH_HOST SSH_PORT INSTANCE_ID [PRICE]
		if lp, err := strconv.Atoi(parts[0]); err == nil && lp > 0 {
			rec.LocalPort = lp
			rec.Host = parts[1]
			rec.SSHPort, _ = strconv.Atoi(parts[2])
			rec.InstanceID = parts[3]
			if len(parts) >= 5 {
				rec.Price, _ = strconv.ParseFloat(parts[4], 64)
			}
			if len(parts) >= 6 {
				rec.Model = parts[5]
			}
		} else {
			// Legacy: SSH_HOST SSH_PORT INSTANCE_ID [PRICE]
			rec.Host = parts[0]
			rec.SSHPort, _ = strconv.Atoi(parts[1])
			rec.InstanceID = parts[2]
			if len(parts) >= 4 {
				rec.Price, _ = strconv.ParseFloat(parts[3], 64)
			}
		}
		if rec.Host == "" || rec.SSHPort <= 0 {
			continue
		}
		key := rec.InstanceID
		if key == "" {
			key = rec.Host
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, NodeConfig{
			Name:      nodeNameForID(rec.InstanceID, rec.Host),
			ID:        rec.InstanceID,
			SSHHost:   rec.Host,
			SSHPort:   rec.SSHPort,
			LocalPort: rec.LocalPort,
			Model:     rec.Model,
		})
	}
	if len(out) > 0 {
		hostLog("info", "tunnel auto-discovered nodes", map[string]any{"count": len(out), "path": path})
	}
	return out
}

// nodeNameForID picks a readable name for an auto-discovered node.
// The Vast template name is prettier; we apply it during instance merge,
// so here we fall back to a stable id-ish label.
func nodeNameForID(id, host string) string {
	if id != "" {
		return "vast-" + id
	}
	return "vast-" + host
}
