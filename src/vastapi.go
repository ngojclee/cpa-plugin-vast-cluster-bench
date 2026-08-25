package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// VastInstance is the subset of the Vast.ai API instance object we need.
type VastInstance struct {
	ID            json.Number    `json:"id"`
	ActualStatus  string         `json:"actual_status"`
	CurState      string         `json:"cur_state"`
	IntendedState string         `json:"intended_status"`
	PublicIP      string         `json:"public_ipaddr"`
	SSHHost       string         `json:"ssh_host"`
	SSHPort       int            `json:"ssh_port"`
	DirectPort    int            `json:"direct_port_start"`
	DPHTotal      float64        `json:"dph_total"`
	GPUName       string         `json:"gpu_name"`
	NumGPUs       int            `json:"num_gpus"`
	CPUName       string         `json:"cpu_name"`
	CPURAM        float64        `json:"cpu_ram"`
	GPUVRAM       int            `json:"gpu_ram"`
	GPUTotalVRAM  int            `json:"gpu_totalram"`
	TemplateName  string         `json:"template_name"`
	GPUUtil       float64        `json:"gpu_util"`
	GPUTemp       float64        `json:"gpu_temp"`
	StatusMsg     string         `json:"status_msg"`
	StartDate     int64          `json:"start_date"`
	Duration      float64        `json:"duration"`
}

func (i *VastInstance) IDString() string {
	return i.ID.String()
}

type vastAPIResponse struct {
	Success   bool            `json:"success"`
	Instances []VastInstance  `json:"instances"`
	Error     string          `json:"error"`
	Msg       string          `json:"msg"`
}

// fetchVastInstances queries the Vast.ai instances API through CPA's host
// HTTP transport (respects any proxy configured in CPA).
func fetchVastInstances(apiKey string) ([]VastInstance, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("no Vast API key configured")
	}
	url := "https://console.vast.ai/api/v1/instances/?api_key=" + apiKey
	resp, errHTTP := hostHTTPDo(pluginapi.HTTPRequest{
		Method: "GET",
		URL:    url,
	})
	if errHTTP != nil {
		return nil, fmt.Errorf("vast api http: %w", errHTTP)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("vast api status %d", resp.StatusCode)
	}
	var parsed vastAPIResponse
	if errJSON := json.Unmarshal(resp.Body, &parsed); errJSON != nil {
		return nil, fmt.Errorf("vast api parse: %w", errJSON)
	}
	if !parsed.Success {
		return nil, fmt.Errorf("vast api error: %s %s", parsed.Error, parsed.Msg)
	}
	return parsed.Instances, nil
}

func (i *VastInstance) IsRunning() bool {
	return i.ActualStatus == "running" || i.CurState == "running"
}

func (i *VastInstance) IsOffline() bool {
	s := i.ActualStatus
	return s == "exited" || s == "offline" || s == "stopped" || s == "paused" || s == "error"
}

func (i *VastInstance) Uptime() time.Duration {
	if i.StartDate == 0 {
		return 0
	}
	return time.Since(time.Unix(i.StartDate, 0))
}
