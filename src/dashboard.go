package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func jsonResponse(status int, v any) ([]byte, error) {
	body, errMarshal := json.Marshal(v)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       body,
	})
}

func htmlResponse(body string) ([]byte, error) {
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       []byte(body),
	})
}

type nodeStatus struct {
	Name       string         `json:"name"`
	Reachable  bool           `json:"reachable"`
	EngineUp   bool           `json:"engine_up"`
	Status     string         `json:"status"`
	Model      string         `json:"model"`
	Engine     string         `json:"engine"`
	TTFTS      float64        `json:"ttft_s"`
	DecodeTokS float64        `json:"decode_tok_s"`
	KVTokens   float64        `json:"kv_cache_tokens"`
	KVUsage    float64        `json:"kv_usage"`
	Running    float64        `json:"running"`
	Queue      float64        `json:"queue"`
	ProbeTokens int           `json:"probe_tokens"`
	PriceH     float64        `json:"price_h"`
	GPUs       []gpuTelemetry `json:"gpus"`
	Vast       map[string]any `json:"vast,omitempty"`
	LastSeen   string         `json:"last_seen"`
}

type clusterStatus struct {
	Version     string       `json:"version"`
	GeneratedAt string       `json:"generated_at"`
	Interval    string       `json:"probe_interval"`
	Nodes       []nodeStatus `json:"nodes"`
}

func buildStatus() clusterStatus {
	p := currentPool()
	p.mu.Lock()
	cfg := p.cfg
	names := make([]string, 0, len(p.nodes))
	for name := range p.nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	out := clusterStatus{
		Version:     pluginVersion,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Interval:    cfg.ProbeInterval,
		Nodes:       make([]nodeStatus, 0, len(names)),
	}
	p.mu.Unlock()

	for _, name := range names {
		st, ok := p.nodeState(name)
		if !ok {
			continue
		}
		entry := nodeStatus{Name: name, Status: "unknown"}
		st.mu.Lock()
		if st.last != nil {
			entry.Reachable = st.last.Reachable
			entry.EngineUp = st.last.EngineUp
			entry.Status = st.last.Status
			entry.Model = st.last.Model
			entry.Engine = st.last.Engine
			entry.TTFTS = st.last.TTFTS
			entry.DecodeTokS = st.last.DecodeTokS
			entry.KVTokens = st.last.KVTokens
			entry.KVUsage = st.last.KVUsage
			entry.Running = st.last.Running
			entry.Queue = st.last.Queue
			entry.ProbeTokens = st.last.ProbeTokens
			entry.PriceH = st.last.PriceH
		}
		if st.vast != nil {
			if v, okv := st.vast["price_h"]; okv {
				if f, okf := v.(float64); okf {
					entry.PriceH = f
				}
			}
			entry.Vast = st.vast
		}
		if !st.lastSeen.IsZero() {
			entry.LastSeen = st.lastSeen.Format(time.RFC3339)
		}
		st.mu.Unlock()
		out.Nodes = append(out.Nodes, entry)
	}
	return out
}
