package main

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// NodeConfig describes one Vast.ai instance to benchmark.
type NodeConfig struct {
	Name       string `yaml:"name" json:"name"`
	ID         string `yaml:"id" json:"id"`
	SSHHost    string `yaml:"ssh-host" json:"ssh_host"`
	SSHPort    int    `yaml:"ssh-port" json:"ssh_port"`
	DirectHost string `yaml:"direct-host" json:"direct_host"`
	DirectPort int    `yaml:"direct-port" json:"direct_port"`
	LocalPort  int    `yaml:"-" json:"-"`
}

// Settings holds all plugin configuration.
type Settings struct {
	ProbeInterval string        `yaml:"probe-interval" json:"probe_interval"`
	VastAPIKey    string        `yaml:"vast-api-key" json:"vast_api_key"`
	SSHKeyPath    string        `yaml:"ssh-key-path" json:"ssh_key_path"`
	SSHUser       string        `yaml:"ssh-user" json:"ssh_user"`
	TunnelDir     string        `yaml:"tunnel-dir" json:"tunnel_dir"`
	HistoryDays   int           `yaml:"history-days" json:"history_days"`
	Nodes         []NodeConfig  `yaml:"nodes" json:"nodes"`
	AuthDir       string        `yaml:"-" json:"-"`

	interval time.Duration
}

func (s *Settings) Interval() time.Duration {
	if s.interval > 0 {
		return s.interval
	}
	return 5 * time.Minute
}

func (s *Settings) Key() string {
	if s.VastAPIKey != "" {
		return s.VastAPIKey
	}
	return os.Getenv("VAST_API_KEY")
}

// TunnelDirResolved returns the tunnel base directory used to auto-discover
// instances.txt and the SSH key. Env VAST_TUNNEL_DIR wins over config.
func (s *Settings) TunnelDirResolved() string {
	if p := os.Getenv("VAST_TUNNEL_DIR"); p != "" {
		return p
	}
	if s.TunnelDir != "" {
		return s.TunnelDir
	}
	return "/vast-tunnel"
}

// SSHKey resolves the SSH private key path. Priority:
//  1. tunnel-dir/ssh/id_ed25519 (auto-detected, matches vast-tunnel)
//  2. env SSH_KEY_PATH / VAST_SSH_KEY_PATH
//  3. config ssh-key-path
func (s *Settings) SSHKey() string {
	if s.SSHKeyPath != "" {
		return s.SSHKeyPath
	}
	if p := os.Getenv("SSH_KEY_PATH"); p != "" {
		return p
	}
	if p := os.Getenv("VAST_SSH_KEY_PATH"); p != "" {
		return p
	}
	auto := filepath.Join(s.TunnelDirResolved(), "ssh", "id_ed25519")
	if fi, err := os.Stat(auto); err == nil && !fi.IsDir() {
		return auto
	}
	return "/vast-ssh/id_ed25519"
}

func (s *Settings) User() string {
	if s.SSHUser != "" {
		return s.SSHUser
	}
	return "root"
}

// decodeSettings extracts the plugin config block from CPA's config.yaml.
// The full CPA config YAML is passed in the lifecycle request; we only read
// plugins.configs.vast-cluster-bench.
func decodeSettings(configYAML []byte) Settings {
	out := Settings{
		ProbeInterval: "5m",
		SSHUser:       "root",
		HistoryDays:   7,
	}
	if len(configYAML) == 0 {
		return out
	}
	var root struct {
		Plugins *struct {
			Configs map[string]Settings `yaml:"configs"`
		} `yaml:"plugins"`
	}
	if errYAML := yaml.Unmarshal(configYAML, &root); errYAML != nil {
		hostLog("warn", "config parse failed", map[string]any{"error": errYAML.Error()})
		return out
	}
	if root.Plugins != nil {
		if cfg, ok := root.Plugins.Configs["vast-cluster-bench"]; ok {
			// Merge instead of replace: YAML config only overrides fields it
			// actually sets, so defaults survive when keys are omitted.
			out = cfg
			if cfg.ProbeInterval == "" {
				out.ProbeInterval = "5m"
			}
			if cfg.HistoryDays <= 0 {
				out.HistoryDays = 7
			}
			if cfg.SSHUser == "" {
				out.SSHUser = "root"
			}
			if cfg.TunnelDir != "" {
				out.TunnelDir = cfg.TunnelDir
			}
		}
	}
	// Robustness: if the lifecycle config_yaml did not carry our block (some
	// CPA versions send a trimmed payload), read the mounted config file
	// directly (same file the host uses: /CLIProxyAPI/config.yaml).
	if len(out.Nodes) == 0 || out.ProbeInterval == "" {
		if cfg, ok := decodeConfigFile(); ok {
			if len(cfg.Nodes) > 0 {
				out.Nodes = cfg.Nodes
			}
			if cfg.ProbeInterval != "" {
				out.ProbeInterval = cfg.ProbeInterval
			}
			if cfg.HistoryDays > 0 {
				out.HistoryDays = cfg.HistoryDays
			}
			if cfg.SSHUser != "" {
				out.SSHUser = cfg.SSHUser
			}
			if cfg.TunnelDir != "" {
				out.TunnelDir = cfg.TunnelDir
			}
		}
	}
	if out.ProbeInterval != "" {
		if d, errParse := time.ParseDuration(out.ProbeInterval); errParse == nil {
			out.interval = d
		}
	}
	if out.HistoryDays <= 0 {
		out.HistoryDays = 7
	}
	return out
}

// decodeConfigFile reads the CPA config file from the container mount and
// extracts the plugin block. Paths tried: env CPA_CONFIG_PATH, the standard
// mount /CLIProxyAPI/config.yaml, and the cpa-config-path convention.
func decodeConfigFile() (Settings, bool) {
	candidates := []string{
		os.Getenv("CPA_CONFIG_PATH"),
		"/CLIProxyAPI/config.yaml",
		"/cpa-config.yaml",
	}
	for _, path := range candidates {
		if path == "" {
			continue
		}
		raw, errRead := os.ReadFile(path)
		if errRead != nil {
			continue
		}
		var root struct {
			Plugins *struct {
				Configs map[string]Settings `yaml:"configs"`
			} `yaml:"plugins"`
		}
		if errYAML := yaml.Unmarshal(raw, &root); errYAML != nil {
			continue
		}
		if root.Plugins != nil {
			if cfg, ok := root.Plugins.Configs["vast-cluster-bench"]; ok {
				hostLog("info", "config read from file", map[string]any{"path": path, "nodes": len(cfg.Nodes)})
				return cfg, true
			}
		}
	}
	return Settings{}, false
}
