package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// remoteProbeScript is executed on each Vast node over SSH (python3 -c).
// It scans ALL candidate LLM engine ports, measures each engine's TTFT,
// prefill + decode throughput with real requests, reads KV/cache metrics,
// and samples per-GPU telemetry once. Output: one JSON object on stdout
// prefixed with "PROBE_JSON=".
//
// Multi-engine support: a machine running both vLLM and SGLang (or two
// servers on different ports) yields one entry per engine. Shared host
// telemetry (GPU) is emitted once.
const remoteProbeScript = `
import json, os, re, subprocess, time, urllib.request

def get(url, timeout=5, key=None):
    hdrs = {"User-Agent": "cluster-bench"}
    if key:
        hdrs["Authorization"] = "Bearer " + key
    req = urllib.request.Request(url, headers=hdrs)
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return r.read().decode("utf-8", "replace")

HUB_KEY = os.environ.get("CB_API_KEY", "")

def autodetect_key():
    found = []
    for pid in os.listdir("/proc"):
        if not pid.isdigit():
            continue
        try:
            with open("/proc/%s/cmdline" % pid, "rb") as f:
                raw = f.read().replace(b"\x00", b" ").decode("utf-8", "replace")
        except Exception:
            continue
        m = re.search(r"--api-key\s+([A-Za-z0-9]{16,})", raw)
        if m:
            t = m.group(1)
            if t not in found:
                found.append(t)
    return found

def key_candidates():
    cands = []
    if HUB_KEY:
        cands.append(HUB_KEY)
    for k in autodetect_key():
        if k not in cands:
            cands.append(k)
    cands.append(None)
    return cands

def mval(lines, *pats):
    for line in lines:
        parts = line.rsplit(" ", 1)
        if len(parts) != 2:
            continue
        name = parts[0].split("{")[0]
        for pat in pats:
            if pat in name:
                try:
                    return float(parts[1])
                except Exception:
                    pass
    return None

def probe_engine(p, key):
    try:
        m = get("http://127.0.0.1:%d/v1/models" % p, timeout=4, key=key)
        d = json.loads(m)
        models = d.get("data") or []
        if not models:
            return None
        e = {
            "port": p,
            "model": models[0].get("id") or models[0].get("name"),
            "engine": None,
            "auth": "key" if key else "none",
        }
        try:
            m = get("http://127.0.0.1:%d/metrics" % p, timeout=8, key=key)
            lines = [l for l in m.splitlines() if l and not l.startswith("#")]
            if "sglang" in m:
                e["engine"] = "sglang"
                e["kv_cache_tokens"] = mval(lines, "kv_cache_pool_tokens", "num_gpu_blocks")
                e["kv_usage"] = mval(lines, "token_usage")
                e["queue"] = mval(lines, "num_queue")
                e["num_running"] = mval(lines, "num_running")
                e["cache_hit"] = mval(lines, "cache_hit_rate")
                e["requests_total"] = mval(lines, "num_total_requests", "requests_total")
                e["prompt_tokens_total"] = mval(lines, "prompt_tokens_total")
                e["gen_tokens_total"] = mval(lines, "generation_tokens_total")
            else:
                e["engine"] = "vllm"
                e["kv_cache_tokens"] = mval(lines, "kv_cache_size_tokens")
                e["kv_usage"] = mval(lines, "kv_cache_usage_perc")
                # vLLM 0.27.x chỉ expose kv_cache_usage_perc (không có
                # kv_cache_pool_tokens). Ước lượng tokens từ capacity cấu hình.
                if e.get("kv_cache_tokens") is None and e.get("kv_usage") is not None:
                    cap_ = float(os.environ.get("CB_KV_CAPACITY") or 0)
                    if cap_ > 0:
                        e["kv_cache_tokens"] = round(e["kv_usage"] * cap_)
                e["queue"] = mval(lines, "num_requests_waiting")
                e["num_running"] = mval(lines, "num_requests_running")
                e["requests_total"] = mval(lines, "num_requests_total", "requests_total")
                e["prompt_tokens_total"] = mval(lines, "num_prompt_tokens_total", "prompt_tokens_total")
                e["gen_tokens_total"] = mval(lines, "num_generation_tokens_total", "generation_tokens_total")
        except Exception as ex:
            e["metrics_error"] = str(ex)[:200]
        return e
    except Exception:
        return None

def measure(e):
    p = e["port"]
    CB_KEY = None
    for key in key_candidates():
        try:
            get("http://127.0.0.1:%d/v1/models" % p, timeout=4, key=key)
            CB_KEY = key
            break
        except Exception:
            continue
    hdrs = {"Content-Type": "application/json"}
    if CB_KEY:
        hdrs["Authorization"] = "Bearer " + CB_KEY

    # Prefill speed: one request with a long prompt, max_tokens 1.
    try:
        payload_prefill = {
            "model": e["model"],
            "messages": [{"role": "user", "content": "Hãy viết lại câu sau: " + ("lorem ipsum dolor sit amet consectetur adipiscing elit. " * 60)}],
            "max_tokens": 1,
            "stream": False,
            "temperature": 0.0,
            "chat_template_kwargs": {"enable_thinking": False},
        }
        req = urllib.request.Request(
            "http://127.0.0.1:%d/v1/chat/completions" % p,
            data=json.dumps(payload_prefill).encode(),
            headers=hdrs,
        )
        t0 = time.time()
        with urllib.request.urlopen(req, timeout=60) as resp:
            body = json.loads(resp.read().decode("utf-8", "replace"))
        total_prefill = time.time() - t0
        prompt_tokens = (body.get("usage") or {}).get("prompt_tokens") or 0
        if total_prefill > 0 and prompt_tokens > 0:
            e["prefill_tok_s"] = round(prompt_tokens / total_prefill, 1)
    except Exception as ex:
        e["prefill_error"] = str(ex)[:200]

    # Real streaming request: TTFT + decode tok/s.
    try:
        payload = {
            "model": e["model"],
            "messages": [{"role": "user", "content": "Say the word: probe. Then count 1,2,3."}],
            "max_tokens": 64,
            "stream": True,
            "temperature": 0.0,
            "chat_template_kwargs": {"enable_thinking": False},
            "stream_options": {"include_usage": True},
        }
        req = urllib.request.Request(
            "http://127.0.0.1:%d/v1/chat/completions" % p,
            data=json.dumps(payload).encode(),
            headers=hdrs,
        )
        t0 = time.time()
        ttft = None
        ntok = 0
        usage_tokens = None
        with urllib.request.urlopen(req, timeout=90) as resp:
            for line in resp:
                s = line.decode("utf-8", "replace").strip()
                if not s.startswith("data: "):
                    continue
                if s == "data: [DONE]":
                    break
                try:
                    chunk = json.loads(s[6:])
                except Exception:
                    continue
                if ttft is None and chunk.get("choices"):
                    ttft = time.time() - t0
                if chunk.get("usage"):
                    usage_tokens = chunk["usage"].get("completion_tokens")
                for ch in chunk.get("choices", []):
                    d = ch.get("delta") or {}
                    if d.get("content") or d.get("reasoning_content"):
                        ntok += 1
        total = time.time() - t0
        e["ttft_s"] = round(ttft, 4) if ttft is not None else None
        if usage_tokens:
            ntok = usage_tokens
        e["probe_tokens"] = int(ntok)
        if total > 0 and ttft is not None and total > ttft:
            e["decode_tok_s"] = round(ntok / (total - ttft), 1)
        else:
            e["decode_tok_s"] = round(ntok / total, 1) if total > 0 else None
        e["probe_total_s"] = round(total, 2)
    except Exception as ex:
        e["probe_error"] = str(ex)[:200]

# Scan all candidate ports; each live engine becomes its own entry.
engines = []
for p in (18000, 30000, 8000, 18080, 8001):
    for key in key_candidates():
        e = probe_engine(p, key)
        if e is not None:
            engines.append(e)
            break

for e in engines:
    measure(e)

out = {"engines": engines, "gpus": []}

# GPU telemetry (per-card, shared across engines on this host).
gpus = []
try:
    s = subprocess.run(
        ["nvidia-smi", "--query-gpu=index,name,temperature.gpu,power.draw,memory.used,memory.total,utilization.gpu",
         "--format=csv,noheader,nounits"],
        capture_output=True, text=True, timeout=10,
    )
    for line in s.stdout.strip().splitlines():
        c = [x.strip() for x in line.split(",")]
        if len(c) >= 7:
            gpus.append({
                "idx": int(c[0] or 0),
                "name": c[1],
                "temp_c": float(c[2] or 0),
                "w": float(c[3] or 0),
                "mem_mib": float(c[4] or 0),
                "mem_total_mib": float(c[5] or 0),
                "util_pct": float(c[6] or 0),
            })
except Exception:
    pass
out["gpus"] = gpus

print("PROBE_JSON=" + json.dumps(out))
`

// engineProbe is one detected LLM engine on a node (per port).
type engineProbe struct {
	Port              int      `json:"port"`
	Model             string   `json:"model"`
	Engine            string   `json:"engine"`
	Auth              string   `json:"auth"`
	KVTokens          *float64 `json:"kv_cache_tokens"`
	KVUsage           *float64 `json:"kv_usage"`
	Queue             *float64 `json:"queue"`
	NumRunning        *float64 `json:"num_running"`
	MetricsError      string   `json:"metrics_error"`
	TTFTS             *float64 `json:"ttft_s"`
	ProbeTokens       int      `json:"probe_tokens"`
	DecodeTokS        *float64 `json:"decode_tok_s"`
	PrefillTokS       *float64 `json:"prefill_tok_s"`
	ProbeTotalS       *float64 `json:"probe_total_s"`
	CacheHit          *float64 `json:"cache_hit"`
	RequestsTotal     *float64 `json:"requests_total"`
	PromptTokensTotal *float64 `json:"prompt_tokens_total"`
	GenTokensTotal    *float64 `json:"gen_tokens_total"`
	ProbeError        string   `json:"probe_error"`
}

// probeResult is the parsed remote probe output (multi-engine).
type probeResult struct {
	Engines []engineProbe  `json:"engines"`
	GPUs    []gpuTelemetry `json:"gpus"`
}

type gpuTelemetry struct {
	Idx         int     `json:"idx"`
	Name        string  `json:"name"`
	TempC       float64 `json:"temp_c"`
	W           float64 `json:"w"`
	MemMiB      float64 `json:"mem_mib"`
	MemTotalMiB float64 `json:"mem_total_mib"`
	UtilPct     float64 `json:"util_pct"`
}

// sshProbe connects to one node over SSH and runs the remote probe script.
// It tries direct host:port first, then the proxy host:port (both from the
// Vast API / config). keyPath == "" means the PEM is passed inline as keyPEM.
func sshProbe(host string, port int, user, keyPath, keyPEM string, timeout time.Duration, kvCapacity int64) (*probeResult, error) {
	if host == "" || port == 0 {
		return nil, fmt.Errorf("no ssh endpoint for node")
	}
	var auth ssh.AuthMethod
	if keyPEM != "" {
		signer, errSigner := ssh.ParsePrivateKey([]byte(keyPEM))
		if errSigner != nil {
			return nil, fmt.Errorf("parse inline key: %w", errSigner)
		}
		auth = ssh.PublicKeys(signer)
	} else {
		raw, errRead := os.ReadFile(keyPath)
		if errRead != nil {
			return nil, fmt.Errorf("read ssh key %s: %w", keyPath, errRead)
		}
		signer, errSigner := ssh.ParsePrivateKey(raw)
		if errSigner != nil {
			return nil, fmt.Errorf("parse ssh key %s: %w", keyPath, errSigner)
		}
		auth = ssh.PublicKeys(signer)
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Vast nodes rotate; key auth is the trust anchor.
		Timeout:         15 * time.Second,
	}
	client, errDial := ssh.Dial("tcp", fmt.Sprintf("%s:%d", host, port), cfg)
	if errDial != nil {
		return nil, errDial
	}
	defer client.Close()

	session, errNew := client.NewSession()
	if errNew != nil {
		return nil, errNew
	}
	defer session.Close()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	envPrefix := ""
	if kvCapacity > 0 {
		envPrefix = fmt.Sprintf("CB_KV_CAPACITY=%d ", kvCapacity)
	}
	if errRun := session.Run(envPrefix + "python3 -c " + shellQuote(remoteProbeScript)); errRun != nil {
		return nil, fmt.Errorf("ssh run: %w (stderr: %s)", errRun, truncate(stderr.String(), 300))
	}

	// Parse the last PROBE_JSON= line.
	last := ""
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "PROBE_JSON=") {
			last = strings.TrimPrefix(line, "PROBE_JSON=")
		}
	}
	if last == "" {
		return nil, fmt.Errorf("no PROBE_JSON in output (stderr: %s)", truncate(stderr.String(), 300))
	}
	var res probeResult
	if errJSON := json.Unmarshal([]byte(last), &res); errJSON != nil {
		return nil, fmt.Errorf("parse probe json: %w", errJSON)
	}
	return &res, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
