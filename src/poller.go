package main

import (
	"fmt"
	"sync"
	"time"
)

var (
	pollerOnce sync.Once
	pollerStop chan struct{}
)

// startPoller launches the background probe loop (idempotent).
func startPoller() {
	pollerOnce.Do(func() {
		pollerStop = make(chan struct{})
		go pollerLoop()
		hostLog("info", "poller started", nil)
	})
}

// kickPoller asks the poller to run a probe cycle immediately.
func kickPoller() {
	go func() {
		probeAll() // probeAll does its own markProbeStart/Done guard
	}()
}

// stopPoller stops the background loop (used on plugin shutdown).
func stopPoller() {
	if pollerStop != nil {
		close(pollerStop)
	}
}

func pollerLoop() {
	p := currentPool()
	// First cycle immediately, then on interval.
	p.loadPersistedSettings()
	probeAll()
	ticker := time.NewTicker(p.cfg.Interval())
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			probeAll()
		case <-pollerStop:
			return
		}
	}
}

// probeAll runs one probe cycle:
//  1. fetch Vast instances (status/price/GPU metadata)
//  2. auto-discover nodes from the tunnel's instances.txt (if present)
//  3. merge with configured nodes
//  4. probe every reachable node in parallel
//  5. prune history older than history-days
func probeAll() {
	p := currentPool()
	if !p.markProbeStart() {
		return
	}
	defer p.markProbeDone()

	instances, errInstances := fetchVastInstances(p.effectiveKey())
	if errInstances != nil {
		hostLog("warn", "vast api failed", map[string]any{"error": errInstances.Error()})
		instances = nil
	}

	// Sources of nodes, in priority order (name wins if duplicated):
	// 1. explicit config nodes
	// 2. tunnel instances.txt (auto-discovered)
	// 3. Vast API instances not otherwise covered
	configByName := make(map[string]NodeConfig)
	for _, n := range p.cfg.Nodes {
		configByName[n.Name] = n
	}

	// id -> instance metadata from Vast API.
	byID := make(map[string]VastInstance)
	for _, inst := range instances {
		byID[inst.IDString()] = inst
	}

	// Build final node set.
	all := make(map[string]NodeConfig)
	usedNames := make(map[string]string)  // name -> instance id (disambiguate)
	cfgByID := make(map[string]string)    // config instance id -> name (skip dup)
	for name, cfg := range configByName {
		all[name] = cfg
		usedNames[name] = cfg.ID
		if cfg.ID != "" {
			cfgByID[cfg.ID] = name
		}
	}

	// Tunnel-discovered nodes (they carry live SSH host/port from the tunnel).
	// Skip any whose instance id is already covered by a config node (G/F/I).
	tunnelNodes := p.discoverTunnelNodes()
	tunnelByName := make(map[string]NodeConfig)
	for _, n := range tunnelNodes {
		if _, covered := cfgByID[n.ID]; covered {
			continue
		}
		tunnelByName[n.Name] = n
		if _, exists := all[n.Name]; !exists {
			all[n.Name] = n
			usedNames[n.Name] = n.ID
			if _, ok := p.nodes[n.Name]; !ok {
				p.nodes[n.Name] = &NodeState{}
			}
		}
	}

	// Vast API instances not already known (auto-discover new machines).
	// Multiple instances can share one template name; disambiguate with #ID.
	// Instances whose id is already covered by a config node are skipped so
	// the prettier config name (G/F/I) is used instead of the template name.
	for _, inst := range instances {
		id := inst.IDString()
		if _, covered := cfgByID[id]; covered {
			continue
		}
		base := instanceName(&inst)
		name := base
		if prevID, ok := usedNames[name]; ok && prevID != id {
			name = base + " #" + id
		}
		if _, exists := all[name]; exists {
			continue
		}
		usedNames[name] = id
		// prefer tunnel endpoint if we have one for this id
		if tc, ok := tunnelByName[name]; ok {
			all[name] = tc
		} else {
			cfg := NodeConfig{Name: name, ID: id}
			if inst.PublicIP != "" && inst.DirectPort > 0 {
				cfg.SSHHost, cfg.SSHPort = inst.PublicIP, inst.DirectPort
			} else if inst.SSHHost != "" {
				cfg.SSHHost, cfg.SSHPort = inst.SSHHost, inst.SSHPort+1
			}
			all[name] = cfg
		}
		if _, ok := p.nodes[name]; !ok {
			p.nodes[name] = &NodeState{}
		}
	}

	// Keep node states for names that vanished from all sources so the
	// dashboard still shows them as stale/offline (last known data).

	var wg sync.WaitGroup
	for name, cfg := range all {
		wg.Add(1)
		go func(name string, cfg NodeConfig) {
			defer wg.Done()
			probeNode(p, name, cfg, byID)
		}(name, cfg)
	}
	wg.Wait()

	p.pruneHistory()
}

func instanceName(inst *VastInstance) string {
	if inst.TemplateName != "" {
		return inst.TemplateName
	}
	return inst.IDString()
}

func probeNode(p *pool, name string, cfg NodeConfig, byID map[string]VastInstance) {
	inst, hasVast := byID[cfg.ID]

	// Offline / exited instances: record status only, no probe.
	if hasVast && !inst.IsRunning() {
		status := inst.ActualStatus
		if status == "" {
			status = "offline"
		}
		p.setNodeResult(name, &HistoryPoint{Reachable: false, EngineUp: false, Status: status}, vastMap(&inst))
		hostLog("info", "node offline", map[string]any{"node": name, "status": status})
		return
	}

	keyPath, keyPEM := p.effectiveSSHKey()

	// Determine SSH endpoint: tunnel/config host/port > direct > proxy.
	host, port := cfg.SSHHost, cfg.SSHPort
	if host == "" && hasVast {
		if inst.PublicIP != "" && inst.DirectPort > 0 {
			host, port = inst.PublicIP, inst.DirectPort
		} else if inst.SSHHost != "" {
			host, port = inst.SSHHost, inst.SSHPort+1
		}
	}

	res, errProbe := sshProbe(host, port, p.cfg.User(), keyPath, keyPEM, p.cfg.Interval(), cfg.KVCapacity)
	if errProbe != nil {
		p.setNodeResult(name, &HistoryPoint{
			Reachable: false,
			EngineUp:  false,
			Status:    "ssh_error",
			Model:     errProbe.Error(),
		}, vastMapIf(hasVast, &inst))
		hostLog("warn", "probe failed", map[string]any{"node": name, "error": errProbe.Error()})
		return
	}

	// Multi-engine: one node row per live engine. If only one engine exists,
	// keep the configured node name (G/F/I); extra engines get ":port" suffix.
	engines := res.Engines
	if len(engines) == 0 {
		p.setNodeResult(name, &HistoryPoint{
			Reachable: true,
			EngineUp:  false,
			Status:    "no_engine",
			Model:     "engine không tìm thấy trên các port 18000/30000/8000/18080/8001",
		}, vastMapIf(hasVast, &inst))
		hostLog("warn", "no engine found", map[string]any{"node": name})
		return
	}

	vast := vastMapIf(hasVast, &inst)
	for i := range engines {
		e := &engines[i]
		engineName := name
		if i > 0 || len(engines) > 1 {
			engineName = fmt.Sprintf("%s:%d", name, e.Port)
		}
		point := &HistoryPoint{
			Reachable:   true,
			EngineUp:    true,
			Model:       e.Model,
			Engine:      e.Engine,
			TTFTS:       derefF(e.TTFTS),
			DecodeTokS:  derefF(e.DecodeTokS),
			PrefillTokS: derefF(e.PrefillTokS),
			KVTokens:    derefF(e.KVTokens),
			KVUsage:     derefF(e.KVUsage),
			Running:     derefF(e.NumRunning),
			Queue:       derefF(e.Queue),
			CacheHit:    derefF(e.CacheHit),
			RequestsTotal:     derefF(e.RequestsTotal),
			PromptTokensTotal: derefF(e.PromptTokensTotal),
			GenTokensTotal:    derefF(e.GenTokensTotal),
			ProbeTokens: e.ProbeTokens,
			Status:      "ok",
		}
		if !point.EngineUp {
			point.Status = "no_engine"
		}
		if e.ProbeError != "" {
			point.Status = "probe_error"
		}
		if _, ok := p.nodes[engineName]; !ok {
			p.nodes[engineName] = &NodeState{}
		}
		p.setNodeResult(engineName, point, vast)
		hostLog("info", "probe ok", map[string]any{
			"node": engineName, "model": e.Model, "engine": e.Engine, "port": e.Port,
			"tok_s": point.DecodeTokS, "prefill_s": point.PrefillTokS, "ttft_s": point.TTFTS,
			"running": point.Running, "queue": point.Queue,
		})
	}
}

func vastMap(inst *VastInstance) map[string]any {
	return vastMapIf(true, inst)
}

func vastMapIf(ok bool, inst *VastInstance) map[string]any {
	if !ok || inst == nil {
		return nil
	}
	return map[string]any{
		"id":            inst.IDString(),
		"status":        inst.ActualStatus,
		"public_ip":     inst.PublicIP,
		"ssh_host":      inst.SSHHost,
		"ssh_port":      inst.SSHPort,
		"direct_port":   inst.DirectPort,
		"price_h":       inst.DPHTotal,
		"gpu_name":      inst.GPUName,
		"num_gpus":      inst.NumGPUs,
		"gpu_ram":       inst.GPUVRAM,
		"gpu_totalram":  inst.GPUTotalVRAM,
		"cpu_name":      inst.CPUName,
		"cpu_ram":       inst.CPURAM,
		"template":      inst.TemplateName,
		"image":         inst.ImageUUID,
		"onstart":       inst.Onstart,
		"cpu_util":      inst.CPUUtil,
		"mem_usage":     inst.MemUsage,
		"disk_util":     inst.DiskUtil,
		"disk_space":    inst.DiskSpace,
		"disk_usage":    inst.DiskUsage,
		"status_msg":    inst.StatusMsg,
		"uptime_s":      int64(inst.Uptime().Seconds()),
		"gpu_util":      inst.GPUUtil,
		"gpu_temp":      inst.GPUTemp,
	}
}

func derefF(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
