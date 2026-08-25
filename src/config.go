package main

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// NodeConfig describes one Vast.ai instance to benchmark.
type NodeConfig struct {
	Name        string `yaml:"name" json:"name"`
	ID          string `yaml:"id" json:"id"`
	SSHHost     string `yaml:"ssh-host" json:"ssh_host"`
	SSHPort     int    `yaml:"ssh-port" json:"ssh_port"`
	DirectHost  string `yaml:"direct-host" json:"direct_host"`
	DirectPort  int    `yaml:"direct-port" json:"direct_port"`
}

// Settings holds all plugin configuration.
type Settings struct {
	ProbeInterval string        `yaml:"probe-interval" json:"probe_interval"`
	VastAPIKey    string        `yaml:"vast-api-key" json:"vast_api_key"`
	SSHKeyPath    string        `yaml:"ssh-key-path" json:"ssh_key_path"`
	SSHUser       string        `yaml:"ssh-user" json:"ssh_user"`
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
			out = cfg
		}
	}
	if out.ProbeInterval != "" {
		if d, errParse := time.ParseDuration(out.ProbeInterval); errParse == nil {
			out.interval = d
		}
	}
	return out
}
