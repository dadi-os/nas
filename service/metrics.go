package main

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cpuStatus struct {
	Name        string  `json:"name"`
	UsedPercent float64 `json:"used_percent"`
}

type memoryStatus struct {
	Name        string  `json:"name,omitempty"`
	UsedBytes   uint64  `json:"used_bytes"`
	TotalBytes  uint64  `json:"total_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

type gpuStatus struct {
	Name        string   `json:"name"`
	UsedPercent *float64 `json:"used_percent,omitempty"`
}

type metricsSnapshot struct {
	CPU    *cpuStatus    `json:"cpu,omitempty"`
	Memory *memoryStatus `json:"memory,omitempty"`
	GPU    []gpuStatus   `json:"gpu,omitempty"`
}

var (
	metricsMu     sync.RWMutex
	cachedCPU     *cpuStatus
	cachedMem     *memoryStatus
	cachedGPUs    []gpuStatus
	lastCPUIdle   uint64
	lastCPUTotal  uint64
	haveCPUSample bool
)

func startMetricsSampler() {
	refreshMetrics()
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for range t.C {
			refreshMetrics()
		}
	}()
}

func currentMetrics() metricsSnapshot {
	metricsMu.RLock()
	defer metricsMu.RUnlock()
	out := metricsSnapshot{}
	if cachedCPU != nil {
		cp := *cachedCPU
		out.CPU = &cp
	}
	if cachedMem != nil {
		m := *cachedMem
		out.Memory = &m
	}
	if len(cachedGPUs) > 0 {
		out.GPU = append([]gpuStatus(nil), cachedGPUs...)
	}
	return out
}

func refreshMetrics() {
	cpu := sampleCPU()
	mem := sampleMemory()
	gpus := sampleGPUs()

	metricsMu.Lock()
	defer metricsMu.Unlock()
	cachedCPU = cpu
	cachedMem = mem
	cachedGPUs = gpus
}

func sampleCPU() *cpuStatus {
	name := readCPUName()
	idle, total, ok := readCPUTimes()
	if !ok {
		if name == "" {
			return nil
		}
		return &cpuStatus{Name: name, UsedPercent: 0}
	}

	pct := 0.0
	if haveCPUSample && total > lastCPUTotal {
		idleDelta := idle - lastCPUIdle
		totalDelta := total - lastCPUTotal
		if totalDelta > 0 {
			busy := 1.0 - float64(idleDelta)/float64(totalDelta)
			if busy < 0 {
				busy = 0
			}
			if busy > 1 {
				busy = 1
			}
			pct = busy * 100
		}
	}
	lastCPUIdle = idle
	lastCPUTotal = total
	haveCPUSample = true

	if name == "" {
		name = "CPU"
	}
	return &cpuStatus{Name: name, UsedPercent: pct}
}

func readCPUName() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()

	var model, hardware, implementer string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		key, val, cut := strings.Cut(line, ":")
		if !cut {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "model name":
			if model == "" {
				model = val
			}
		case "Hardware":
			hardware = val
		case "CPU implementer":
			implementer = val
		case "Model":
			if model == "" {
				model = val
			}
		}
	}
	if model != "" {
		return model
	}
	if hardware != "" {
		return hardware
	}
	if brand := cpuBrandFromImplementer(implementer); brand != "" {
		return brand
	}
	return ""
}

func cpuBrandFromImplementer(implementer string) string {
	switch strings.ToLower(strings.TrimSpace(implementer)) {
	case "0x61":
		return "Apple Silicon"
	case "0x41":
		return "ARM"
	case "0x42":
		return "Broadcom"
	case "0x43":
		return "Cavium"
	case "0x51":
		return "Qualcomm"
	case "0x53":
		return "Samsung"
	case "0x69":
		return "Intel"
	default:
		if implementer == "" {
			return ""
		}
		return "CPU " + implementer
	}
}

func readCPUTimes() (idle, total uint64, ok bool) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0, false
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	var sum uint64
	for i := 1; i < len(fields); i++ {
		n, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return 0, 0, false
		}
		sum += n
		if i == 4 {
			idle = n
		}
	}
	return idle, sum, true
}

func sampleMemory() *memoryStatus {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil
	}
	defer f.Close()

	var total, available uint64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		bytes := kb * 1024
		switch fields[0] {
		case "MemTotal:":
			total = bytes
		case "MemAvailable:":
			available = bytes
		}
	}
	if total == 0 {
		return nil
	}
	used := total - available
	if available > total {
		used = 0
	}
	pct := float64(used) / float64(total) * 100
	return &memoryStatus{
		Name:        "System memory",
		UsedBytes:   used,
		TotalBytes:  total,
		UsedPercent: pct,
	}
}

func sampleGPUs() []gpuStatus {
	cmd := exec.Command(
		"nvidia-smi",
		"--query-gpu=name,utilization.gpu",
		"--format=csv,noheader,nounits",
	)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var gpus []gpuStatus
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		g := gpuStatus{Name: name}
		if len(parts) == 2 {
			raw := strings.TrimSpace(parts[1])
			if raw != "" && !strings.EqualFold(raw, "[N/A]") {
				if pct, err := strconv.ParseFloat(raw, 64); err == nil {
					g.UsedPercent = &pct
				}
			}
		}
		gpus = append(gpus, g)
	}
	return gpus
}
