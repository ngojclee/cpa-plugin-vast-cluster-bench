package main

import (
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

// probeAll runs one probe cycle: fetch Vast instances, then probe every
// reachable node in parallel.
func probeAll() {
	p := currentPool()
	if !p.markProbeStart() {
		return
	}
	defer p.markProbeDone()

	instances, errInstances := fetchVastInstances(p.effectiveKey())
	if errInstances != nil {
		hostLog("warn", "vast api failed", map[string]any{"error": errInstances.Error()})
		// Keep probing configured nodes even if the API is down.
		instances = nil
	}

	// Build node list: config nodes win by name; auto-discover the rest.
	names := p.nodeNames()
	configByName := make(map[string]NodeConfig)
	for _, n := range p.cfg.Nodes {
		configByName[n.Name] = n
	}

	// Map instance id -> instance.
	byID := make(map[string]VastInstance)
	for _, inst := range instances {
		byID[inst.IDString()] = inst
	}

	// Union of names: configured + discovered. Use a stable unique name per
	// instance: config name wins; otherwise template name, disambiguated by
	// instance id when two machines share the same template name.
	discovered := make(map[string]VastInstance)
	usedNames := make(map[string]string) // name -> instance id
	for name := range configByName {
		usedNames[name] = configByName[name].ID
	}
	for _, inst := range instances {
		base := instanceName(&inst)
		name := base
		if prevID, ok := usedNames[name]; ok && prevID != inst.IDString() {
			name = base + " #" + inst.IDString()
		}
		usedNames[name] = inst.IDString()
		if _, exists := configByName[name]; !exists {
			discovered[name] = inst
			if _, ok := p.nodes[name]; !ok {
				p.nodes[name] = &NodeState{}
			}
		}
	}

	allNames := names
	for name := range discovered {
		found := false
		for _, n := range allNames {
			if n == name {
				found = true
				break
			}
		}
		if !found {
			allNames = append(allNames, name)
		}
	}

	var wg sync.WaitGroup
	for _, name := range allNames {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			probeNode(p, name, configByName, byID, discovered)
		}(name)
	}
	wg.Wait()
}

func instanceName(inst *VastInstance) string {
	if inst.TemplateName != "" {
		return inst.TemplateName
	}
	return inst.IDString()
}

func probeNode(p *pool, name string, configByName map[string]NodeConfig, byID map[string]VastInstance, discovered map[string]VastInstance) {
	cfg, _ := configByName[name]
	inst, hasVast := discovered[name]
	if !hasVast {
		// Maybe the config node id maps to a fetched instance.
		if cfg.ID != "" {
			if i, ok := byID[cfg.ID]; ok {
				inst = i
				hasVast = true
			}
		}
	}

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

	// Determine SSH endpoint: config > direct > proxy.
	host, port := cfg.SSHHost, cfg.SSHPort
	if host == "" && hasVast {
		if inst.PublicIP != "" && inst.DirectPort > 0 {
			host, port = inst.PublicIP, inst.DirectPort
		} else if inst.SSHHost != "" {
			host, port = inst.SSHHost, inst.SSHPort+1 // Vast proxy port is ssh_port+1
		}
	}

	res, errProbe := sshProbe(host, port, p.cfg.User(), keyPath, keyPEM, p.cfg.Interval())
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

	point := &HistoryPoint{
		Reachable:  true,
		EngineUp:   res.Port != nil,
		Model:      res.Model,
		Engine:     res.Engine,
		TTFTS:      derefF(res.TTFTS),
		DecodeTokS: derefF(res.DecodeTokS),
		KVTokens:   derefF(res.KVTokens),
		KVUsage:    derefF(res.KVUsage),
		Running:    derefF(res.NumRunning),
		Queue:      derefF(res.Queue),
		ProbeTokens: res.ProbeTokens,
		Status:     "ok",
	}
	if !point.EngineUp {
		point.Status = "no_engine"
	}
	if res.ProbeError != "" {
		point.Status = "probe_error"
	}
	p.setNodeResult(name, point, vastMapIf(hasVast, &inst))
	hostLog("info", "probe ok", map[string]any{
		"node": name, "model": res.Model, "engine": res.Engine,
		"tok_s": point.DecodeTokS, "ttft_s": point.TTFTS,
		"running": point.Running, "queue": point.Queue,
	})
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
