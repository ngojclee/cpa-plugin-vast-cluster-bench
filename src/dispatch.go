package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type lifecycleRequest struct {
	ConfigYAML []byte `json:"config_yaml"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	Scheduler     bool `json:"scheduler"`
	UsagePlugin   bool `json:"usage_plugin"`
	ManagementAPI bool `json:"management_api"`
}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		if errConfigure := configure(request); errConfigure != nil {
			return nil, errConfigure
		}
		startPoller()
		return okEnvelope(pluginRegistration())
	case pluginabi.MethodManagementRegister:
		return handleManagementRegister()
	case pluginabi.MethodManagementHandle:
		return handleManagement(request)
	case pluginabi.MethodPluginShutdown:
		stopPoller()
		return okEnvelope(map[string]string{"status": "shutdown"})
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func configure(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return errUnmarshal
		}
	}
	cfg := decodeSettings(req.ConfigYAML)
	cfg.AuthDir = hostAuthDir()
	cfg.DataDir = hostPluginDataDir()
	currentPool().reconfigure(cfg)
	hostLog("info", "configured", map[string]any{
		"nodes":    len(cfg.Nodes),
		"interval": cfg.ProbeInterval,
		"api_key":  cfg.VastAPIKey != "",
	})
	return nil
}

func hostAuthDir() string {
	// The plugin reads the host auth-dir the same way go-pool does: from the
	// config file, falling back to the SDK's resolved auth dir if needed.
	if p := currentPool(); p != nil && p.cfg.AuthDir != "" {
		return p.cfg.AuthDir
	}
	return "/root/.cli-proxy-api"
}

// hostPluginDataDir resolves where the plugin may persist its own state
// (settings, history) — OUTSIDE auth-dir so account scans never see it.
func hostPluginDataDir() string {
	if p := os.Getenv("CPA_PLUGIN_DATA_DIR"); p != "" {
		return p
	}
	return "/CLIProxyAPI/plugins/plugin-data"
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "vast-cluster-bench",
			Version:          pluginVersion,
			Author:           "ngojclee",
			GitHubRepository: "https://github.com/ngojclee/cpa-plugin-vast-cluster-bench",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "probe-interval", Type: pluginapi.ConfigFieldTypeString, Description: "Interval between live probes (default 5m)."},
				{Name: "vast-api-key", Type: pluginapi.ConfigFieldTypeString, Description: "Vast.ai API key. Falls back to env VAST_API_KEY."},
				{Name: "tunnel-dir", Type: pluginapi.ConfigFieldTypeString, Description: "Tunnel directory (instances.txt + ssh/id_ed25519). Default /vast-tunnel."},
				{Name: "history-days", Type: pluginapi.ConfigFieldTypeString, Description: "Keep probe history N days, auto-prune (default 7)."},
				{Name: "ssh-key-path", Type: pluginapi.ConfigFieldTypeString, Description: "Path to the SSH private key used to reach Vast nodes (default /vast-ssh/id_ed25519)."},
				{Name: "ssh-user", Type: pluginapi.ConfigFieldTypeString, Description: "SSH user on Vast nodes (default root)."},
				{Name: "nodes", Type: pluginapi.ConfigFieldTypeArray, Description: "Optional explicit node list: name, id, ssh-host, ssh-port, direct-host, direct-port. Empty = auto-discover from Vast API."},
			},
		},
		Capabilities: registrationCapability{
			ManagementAPI: true,
		},
	}
}

const (
	pluginID           = "vast-cluster-bench"
	managementBasePath = "/plugins/" + pluginID
)

func handleManagementRegister() ([]byte, error) {
	return okEnvelope(pluginapi.ManagementRegistrationResponse{
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodGet, Path: managementBasePath + "/settings"},
			{Method: http.MethodPost, Path: managementBasePath + "/settings"},
			{Method: http.MethodPost, Path: managementBasePath + "/probe"},
		},
		Resources: []pluginapi.ResourceRoute{
			{
				Path:        "/status",
				Menu:        "Vast Cluster Bench",
				Description: "Live benchmark and GPU telemetry of the Vast.ai cluster.",
			},
			{
				Path:        "/data",
				Menu:        "",
				Description: "JSON snapshot of the live cluster state (unauthenticated).",
			},
			{
				Path:        "/history",
				Menu:        "",
				Description: "JSON probe history for one node (unauthenticated).",
			},
		},
	})
}

func normalizeManagementPath(path string) (string, bool) {
	isResource := false
	if idx := strings.Index(path, "/v0/resource/plugins/"+pluginID); idx >= 0 {
		path = path[idx+len("/v0/resource/plugins/"+pluginID):]
		isResource = true
	} else if idx := strings.Index(path, "/v0/management/plugins/"+pluginID); idx >= 0 {
		path = path[idx+len("/v0/management/plugins/"+pluginID):]
	} else if strings.HasPrefix(path, managementBasePath) {
		path = strings.TrimPrefix(path, managementBasePath)
	}
	if path == "" {
		path = "/"
	}
	return path, isResource
}

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
		return nil, errUnmarshal
	}
	path, isResource := normalizeManagementPath(req.Path)

	if isResource {
		// Resource routes are not management-authenticated: serve the
		// dashboard shell, the live JSON snapshot, and per-node history.
		if req.Method == http.MethodGet && (path == "/status" || path == "/") {
			return htmlResponse(statusPageHTML)
		}
		if req.Method == http.MethodGet && path == "/data" {
			return jsonResponse(http.StatusOK, buildStatus())
		}
		if req.Method == http.MethodGet && path == "/history" {
			return handleHistory(req)
		}
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "not found"})
	}

	switch {
	case req.Method == http.MethodGet && path == "/status":
		return jsonResponse(http.StatusOK, buildStatus())
	case req.Method == http.MethodGet && path == "/history":
		return handleHistory(req)
	case req.Method == http.MethodPost && path == "/probe":
		kickPoller()
		return jsonResponse(http.StatusAccepted, map[string]string{"status": "probe scheduled"})
	case req.Method == http.MethodGet && path == "/settings":
		return jsonResponse(http.StatusOK, buildSettings())
	case req.Method == http.MethodPost && path == "/settings":
		return handleSettingsUpdate(req)
	default:
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func handleHistory(req pluginapi.ManagementRequest) ([]byte, error) {
	nodeName := req.Query.Get("node")
	if nodeName == "" {
		nodeName = req.Query.Get("name")
	}
	if nodeName == "" {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "node is required"})
	}
	points := currentPool().historyFor(nodeName)
	return jsonResponse(http.StatusOK, map[string]any{
		"node":    nodeName,
		"points":  points,
		"version": pluginVersion,
	})
}

func handleSettingsUpdate(req pluginapi.ManagementRequest) ([]byte, error) {
	var body settingsUpdateBody
	if errUnmarshal := json.Unmarshal(req.Body, &body); errUnmarshal != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
	}
	if errUpdate := currentPool().updateSettings(body); errUpdate != nil {
		return jsonResponse(http.StatusInternalServerError, map[string]string{"error": errUpdate.Error()})
	}
	kickPoller()
	return jsonResponse(http.StatusOK, map[string]string{"status": "saved"})
}

var errUnknownSettings = errors.New("unknown settings")

type settingsUpdateBody struct {
	VastAPIKey string `json:"vast_api_key"`
	SSHKey     string `json:"ssh_key"`
	Clear      bool   `json:"clear"`
}
