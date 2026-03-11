// Termux System Monitor – Web Dashboard
// Mirrors Python web_status.py approach exactly.
// Zero external deps. Reads /proc directly. Single binary.
// Build:  go build -o monitor .
// Cross:  GOOS=linux GOARCH=arm64 go build -o monitor .
// Run:    ./monitor --port 8080
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ══════════════════════════════════════════════════════════════════════════
// Helpers  (same as Python's _cmd, _fb, etc.)
// ══════════════════════════════════════════════════════════════════════════

// readProc reads a /proc (or /sys) virtual file. Returns "" on any error.
func readProc(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// fmtBytes  →  Python _fb()
func fmtBytes(b float64) string {
	for _, u := range []string{"B", "KB", "MB", "GB", "TB"} {
		if b < 1024 {
			return fmt.Sprintf("%.1f %s", b, u)
		}
		b /= 1024
	}
	return fmt.Sprintf("%.1f PB", b)
}

// fmtRuntime  →  Python runtime formatting
func fmtRuntime(sec float64) string {
	if sec >= 86400 {
		d := int(sec / 86400)
		h := int(math.Mod(sec, 86400) / 3600)
		return fmt.Sprintf("%dd %dh", d, h)
	}
	if sec >= 3600 {
		h := int(sec / 3600)
		m := int(math.Mod(sec, 3600) / 60)
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if sec >= 60 {
		m := int(sec / 60)
		s := int(math.Mod(sec, 60))
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", int(sec))
}

// cmdRun  →  Python _cmd().  Has 3-second timeout like Python.
func cmdRun(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = nil // suppress stderr
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// cmdJSON  →  Python _cmd(as_json=True)
func cmdJSON(name string, args ...string) map[string]interface{} {
	s := cmdRun(name, args...)
	if s == "" {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

func atoi(s string) int        { v, _ := strconv.Atoi(strings.TrimSpace(s)); return v }
func atof(s string) float64    { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return v }
func round1(v float64) float64 { return math.Round(v*10) / 10 }

var rePid = regexp.MustCompile(`^\d+$`)

// ══════════════════════════════════════════════════════════════════════════
// Data Types
// ══════════════════════════════════════════════════════════════════════════

type CPUData struct {
	Percent float64   `json:"percent"`
	PerCore []float64 `json:"per_core"`
	Count   int       `json:"count"`
	Model   string    `json:"model"`
	Freqs   []float64 `json:"freqs"`
	FreqCur float64   `json:"freq_cur"`
	FreqMax float64   `json:"freq_max"`
}

type MemData struct {
	Percent     float64 `json:"percent"`
	Total       int64   `json:"total"`
	Used        int64   `json:"used"`
	Available   int64   `json:"available"`
	Buffers     int64   `json:"buffers"`
	Cached      int64   `json:"cached"`
	SwapTotal   int64   `json:"swap_total"`
	SwapUsed    int64   `json:"swap_used"`
	SwapPercent float64 `json:"swap_percent"`
	TotalF      string  `json:"total_f"`
	UsedF       string  `json:"used_f"`
	AvailF      string  `json:"avail_f"`
	BufF        string  `json:"buf_f"`
	CacheF      string  `json:"cache_f"`
	SwTotalF    string  `json:"sw_total_f"`
	SwUsedF     string  `json:"sw_used_f"`
}

type StorData struct {
	Mount   string  `json:"mount"`
	Percent float64 `json:"percent"`
	Total   int64   `json:"total"`
	Used    int64   `json:"used"`
	Free    int64   `json:"free"`
	TotalF  string  `json:"total_f"`
	UsedF   string  `json:"used_f"`
	FreeF   string  `json:"free_f"`
}

type BatData struct {
	Percentage    float64 `json:"percentage"`
	Status        string  `json:"status"`
	Health        string  `json:"health"`
	Temperature   float64 `json:"temperature"`
	Plugged       string  `json:"plugged"`
	Current       float64 `json:"current"`
	TimeRemaining string  `json:"time_remaining"`
}

type NetData struct {
	IP  string  `json:"ip"`
	IP6 string  `json:"ip6"`
	BS  int64   `json:"bs"`
	BR  int64   `json:"br"`
	PS  int64   `json:"ps"`
	PR  int64   `json:"pr"`
	EI  int64   `json:"ei"`
	EO  int64   `json:"eo"`
	DI  int64   `json:"di"`
	DO  int64   `json:"do"`
	SU  float64 `json:"su"`
	SD  float64 `json:"sd"`
	BSF string  `json:"bs_f"`
	BRF string  `json:"br_f"`
	SUF string  `json:"su_f"`
	SDF string  `json:"sd_f"`
}

type DevData struct {
	Model        string `json:"model"`
	Android      string `json:"android"`
	Manufacturer string `json:"manufacturer"`
	Arch         string `json:"arch"`
	SDK          string `json:"sdk"`
	Kernel       string `json:"kernel"`
}

type ProcInfo struct {
	PID  int     `json:"pid"`
	Name string  `json:"name"`
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
	St   string  `json:"st"`
}

type ProcSearch struct {
	PID     int     `json:"pid"`
	Name    string  `json:"name"`
	Cmd     string  `json:"cmd"`
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
	St      string  `json:"st"`
	User    string  `json:"user"`
	Runtime string  `json:"runtime"`
}

// Snapshot  →  Python self.data dict
type Snapshot struct {
	CPU       CPUData    `json:"cpu"`
	Memory    MemData    `json:"memory"`
	Storage   StorData   `json:"storage"`
	Battery   BatData    `json:"battery"`
	Network   NetData    `json:"network"`
	Processes []ProcInfo `json:"processes"`
	Device    DevData    `json:"device"`
	Time      string     `json:"time"`
	Date      string     `json:"date"`
	Uptime    string     `json:"uptime"`
}

// ══════════════════════════════════════════════════════════════════════════
// Monitor  →  Python SystemMonitor class
// ══════════════════════════════════════════════════════════════════════════

type cpuTick struct{ idle, total int64 }

type Monitor struct {
	mu   sync.RWMutex
	data Snapshot

	// error log  →  Python self.errors
	errors []string

	// CPU tracking  →  Python self._prev_cpu_times
	prevCPU []cpuTick

	// Network tracking  →  Python self._last_net
	lastNetS int64
	lastNetR int64
	lastNetT float64

	// Device collected once  →  Python only collects when empty
	deviceDone bool

	// Battery capacity estimate
	batCap float64

	// Per-process CPU tracking (utime+stime deltas)
	prevProcTicks map[int]int64
	prevTotalTick int64
	procCPUCache  map[int]float64 // latest CPU% per PID, for allProcesses()
}

func NewMonitor() *Monitor {
	m := &Monitor{
		batCap:        4000,
		lastNetT:      float64(time.Now().UnixMilli()) / 1000,
		prevProcTicks: make(map[int]int64),
		procCPUCache:  make(map[int]float64),
		data: Snapshot{
			Processes: []ProcInfo{},
		},
	}
	// Warm up /proc/stat  →  Python: self._read_proc_stat(); time.sleep(0.5)
	m.prevCPU = m.readProcStat()
	time.Sleep(500 * time.Millisecond)
	go m.loop()
	return m
}

// logErr  →  Python _log_err()
func (m *Monitor) logErr(where, msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := time.Now().Format("15:04:05")
	entry := fmt.Sprintf("[%s] %s: %s", ts, where, msg)
	m.errors = append(m.errors, entry)
	if len(m.errors) > 50 {
		m.errors = m.errors[len(m.errors)-50:]
	}
}

func (m *Monitor) loop() {
	m.collect()
	tick := time.NewTicker(1 * time.Second)
	for range tick.C {
		m.collect()
	}
}

// safeCollect  →  Python _safe_collect().
// Returns (result, true) on success, (zero, false) on panic/error.
func safeCollect[T any](m *Monitor, name string, fn func() (T, error)) (result T, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			m.logErr(name, fmt.Sprintf("PANIC: %v", r))
			ok = false
		}
	}()
	val, err := fn()
	if err != nil {
		m.logErr(name, err.Error())
		return result, false
	}
	return val, true
}

// collect  →  Python _loop() body.
// KEY: each collector is independent; failure preserves old data (like Python).
func (m *Monitor) collect() {
	// CPU
	if cpu, ok := safeCollect(m, "cpu", m.collectCPU); ok {
		m.mu.Lock()
		m.data.CPU = cpu
		m.mu.Unlock()
	}

	// Memory
	if mem, ok := safeCollect(m, "memory", m.collectMem); ok {
		m.mu.Lock()
		m.data.Memory = mem
		m.mu.Unlock()
	}

	// Storage
	if stor, ok := safeCollect(m, "storage", m.collectStor); ok {
		m.mu.Lock()
		m.data.Storage = stor
		m.mu.Unlock()
	}

	// Battery (might be slow — has 3s timeout via cmdRun)
	if bat, ok := safeCollect(m, "battery", m.collectBat); ok {
		m.mu.Lock()
		m.data.Battery = bat
		m.mu.Unlock()
	}

	// Network
	if netD, ok := safeCollect(m, "network", m.collectNet); ok {
		m.mu.Lock()
		m.data.Network = netD
		m.mu.Unlock()
	}

	// Processes
	if procs, ok := safeCollect(m, "processes", m.collectProcs); ok {
		m.mu.Lock()
		m.data.Processes = procs
		m.mu.Unlock()
	}

	// Device (once)
	m.mu.RLock()
	done := m.deviceDone
	m.mu.RUnlock()
	if !done {
		if dev, ok := safeCollect(m, "device", m.collectDev); ok {
			m.mu.Lock()
			m.data.Device = dev
			m.deviceDone = true
			m.mu.Unlock()
		}
	}
}

// Snap  →  Python snap()
func (m *Monitor) Snap() Snapshot {
	// Deep copy via JSON (like Python: json.loads(json.dumps(self.data)))
	m.mu.RLock()
	b, _ := json.Marshal(m.data)
	m.mu.RUnlock()

	var s Snapshot
	json.Unmarshal(b, &s)

	now := time.Now()
	s.Time = now.Format("15:04:05")
	s.Date = now.Format("2006-01-02")
	s.Uptime = m.getUptime()
	if s.Processes == nil {
		s.Processes = []ProcInfo{}
	}
	if s.CPU.PerCore == nil {
		s.CPU.PerCore = []float64{}
	}
	if s.CPU.Freqs == nil {
		s.CPU.Freqs = []float64{}
	}
	return s
}

func (m *Monitor) Errors() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, len(m.errors))
	copy(out, m.errors)
	n := len(out)
	if n > 30 {
		return out[n-30:]
	}
	return out
}

// ══════════════════════════════════════════════════════════════════════════
// CPU  →  Python _cpu(), _read_proc_stat(), _calc_cpu_percent()
// ══════════════════════════════════════════════════════════════════════════

func (m *Monitor) readProcStat() []cpuTick {
	raw := readProc("/proc/stat")
	if raw == "" {
		return nil
	}
	var ticks []cpuTick
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		var idle, total int64
		for i, f := range fields[1:8] {
			v, _ := strconv.ParseInt(f, 10, 64)
			if i == 3 || i == 4 { // idle + iowait
				idle += v
			}
			total += v
		}
		ticks = append(ticks, cpuTick{idle, total})
	}
	return ticks
}

func (m *Monitor) calcCPUPct() (float64, []float64) {
	cur := m.readProcStat()
	prev := m.prevCPU
	m.prevCPU = cur
	if len(cur) == 0 || len(prev) == 0 {
		return 0, []float64{}
	}
	pct := func(o, n cpuTick) float64 {
		di := n.idle - o.idle
		dt := n.total - o.total
		if dt == 0 {
			return 0
		}
		v := (1 - float64(di)/float64(dt)) * 100
		return math.Max(0, math.Min(100, v))
	}
	overall := round1(pct(prev[0], cur[0]))
	minLen := len(prev)
	if len(cur) < minLen {
		minLen = len(cur)
	}
	per := make([]float64, 0, minLen-1)
	for i := 1; i < minLen; i++ {
		per = append(per, round1(pct(prev[i], cur[i])))
	}
	return overall, per
}

func (m *Monitor) collectCPU() (CPUData, error) {
	pct, per := m.calcCPUPct()
	count := runtime.NumCPU()

	// Model from /proc/cpuinfo  →  same as Python
	model := "Unknown"
	cpuinfo := readProc("/proc/cpuinfo")
	if cpuinfo != "" {
		for _, ln := range strings.Split(cpuinfo, "\n") {
			if strings.Contains(ln, "Hardware") || strings.Contains(ln, "model name") {
				parts := strings.SplitN(ln, ":", 2)
				if len(parts) == 2 {
					model = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}

	// Per-core frequencies from sysfs  →  same as Python
	var freqs []float64
	for i := 0; i < count; i++ {
		raw := readProc(fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/scaling_cur_freq", i))
		if raw != "" {
			freqs = append(freqs, float64(atoi(raw))/1000)
		}
	}
	var freqCur, freqMax float64
	if len(freqs) > 0 {
		var sum float64
		for _, f := range freqs {
			sum += f
			if f > freqMax {
				freqMax = f
			}
		}
		freqCur = sum / float64(len(freqs))
	}
	if freqs == nil {
		freqs = []float64{}
	}

	return CPUData{
		Percent: pct, PerCore: per, Count: count, Model: model,
		Freqs: freqs, FreqCur: math.Round(freqCur), FreqMax: math.Round(freqMax),
	}, nil
}

// ══════════════════════════════════════════════════════════════════════════
// Memory  →  Python _mem_from_proc() (primary), identical to psutil internals
// ══════════════════════════════════════════════════════════════════════════

func (m *Monitor) collectMem() (MemData, error) {
	raw := readProc("/proc/meminfo")
	if raw == "" {
		return MemData{
			TotalF: "N/A", UsedF: "N/A", AvailF: "N/A",
			BufF: "0.0 B", CacheF: "0.0 B", SwTotalF: "0.0 B", SwUsedF: "0.0 B",
		}, fmt.Errorf("cannot read /proc/meminfo")
	}

	// Parse like Python: parts = ln.split(); info[parts[0].rstrip(":")] = int(parts[1]) * 1024
	info := map[string]int64{}
	for _, ln := range strings.Split(raw, "\n") {
		fields := strings.Fields(ln)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		v, _ := strconv.ParseInt(fields[1], 10, 64)
		info[key] = v * 1024 // kB → bytes
	}

	total := info["MemTotal"]
	avail := info["MemAvailable"]
	if avail == 0 {
		avail = info["MemFree"]
	}
	used := total - avail
	var pct float64
	if total > 0 {
		pct = round1(float64(used) / float64(total) * 100)
	}

	swT := info["SwapTotal"]
	swF := info["SwapFree"]
	swU := swT - swF
	var swP float64
	if swT > 0 {
		swP = round1(float64(swU) / float64(swT) * 100)
	}

	return MemData{
		Percent: pct, Total: total, Used: used, Available: avail,
		Buffers: info["Buffers"], Cached: info["Cached"],
		SwapTotal: swT, SwapUsed: swU, SwapPercent: swP,
		TotalF: fmtBytes(float64(total)), UsedF: fmtBytes(float64(used)),
		AvailF: fmtBytes(float64(avail)),
		BufF:   fmtBytes(float64(info["Buffers"])), CacheF: fmtBytes(float64(info["Cached"])),
		SwTotalF: fmtBytes(float64(swT)), SwUsedF: fmtBytes(float64(swU)),
	}, nil
}

// ══════════════════════════════════════════════════════════════════════════
// Storage  →  Python _stor() using psutil.disk_usage() which calls statvfs
// ══════════════════════════════════════════════════════════════════════════

func (m *Monitor) collectStor() (StorData, error) {
	// Method 1: syscall.Statfs  →  same as psutil.disk_usage()
	for _, mp := range []string{"/data", "/storage/emulated/0", "/storage/emulated", "/"} {
		total, used, free, err := diskUsage(mp)
		if err != nil {
			continue
		}
		if total > 0 {
			pct := round1(float64(used) / float64(total) * 100)
			return StorData{
				Mount: mp, Percent: pct, Total: total, Used: used, Free: free,
				TotalF: fmtBytes(float64(total)), UsedF: fmtBytes(float64(used)),
				FreeF: fmtBytes(float64(free)),
			}, nil
		}
	}

	// Method 2: df command (Linux fallback)
	if runtime.GOOS == "linux" {
		for _, mp := range []string{"/data", "/storage/emulated", "/"} {
			out := cmdRun("df", mp)
			if out == "" {
				continue
			}
			lines := strings.Split(out, "\n")
			if len(lines) < 2 {
				continue
			}
			var dataStr string
			for _, ln := range lines[1:] {
				dataStr += " " + strings.TrimSpace(ln)
			}
			fields := strings.Fields(dataStr)
			if len(fields) < 4 {
				continue
			}
			total, e1 := strconv.ParseInt(fields[1], 10, 64)
			used, e2 := strconv.ParseInt(fields[2], 10, 64)
			free, e3 := strconv.ParseInt(fields[3], 10, 64)
			if e1 != nil || e2 != nil || e3 != nil {
				continue
			}
			total *= 1024
			used *= 1024
			free *= 1024
			if total > 0 {
				pct := round1(float64(used) / float64(total) * 100)
				return StorData{
					Mount: mp, Percent: pct, Total: total, Used: used, Free: free,
					TotalF: fmtBytes(float64(total)), UsedF: fmtBytes(float64(used)),
					FreeF: fmtBytes(float64(free)),
				}, nil
			}
		}
	}

	// Method 3: Windows wmic
	if runtime.GOOS == "windows" {
		out := cmdRun("wmic", "logicaldisk", "where", "DeviceID='C:'", "get", "Size,FreeSpace", "/format:csv")
		for _, ln := range strings.Split(out, "\n") {
			if !strings.Contains(ln, ",") {
				continue
			}
			parts := strings.Split(strings.TrimSpace(ln), ",")
			if len(parts) < 3 {
				continue
			}
			free, _ := strconv.ParseInt(parts[1], 10, 64)
			total, _ := strconv.ParseInt(parts[2], 10, 64)
			if total > 0 {
				used := total - free
				pct := round1(float64(used) / float64(total) * 100)
				return StorData{
					Mount: "C:", Percent: pct, Total: total, Used: used, Free: free,
					TotalF: fmtBytes(float64(total)), UsedF: fmtBytes(float64(used)),
					FreeF: fmtBytes(float64(free)),
				}, nil
			}
		}
	}

	return StorData{Mount: "/", TotalF: "N/A", UsedF: "N/A", FreeF: "N/A"}, fmt.Errorf("no storage found")
}

// ══════════════════════════════════════════════════════════════════════════
// Battery  →  Python _bat()
// ══════════════════════════════════════════════════════════════════════════

func (m *Monitor) collectBat() (BatData, error) {
	// termux-battery-status (has 3s timeout from cmdRun)
	d := cmdJSON("termux-battery-status")
	if d != nil {
		pctF, _ := d["percentage"].(float64)
		st, _ := d["status"].(string)
		cur, _ := d["current"].(float64)
		health, _ := d["health"].(string)
		temp, _ := d["temperature"].(float64)
		plug, _ := d["plugged"].(string)
		if st == "" {
			st = "Unknown"
		}
		if health == "" {
			health = "Unknown"
		}
		if plug == "" {
			plug = "Unknown"
		}
		tr := "N/A"
		if cur != 0 {
			cap := m.batCap
			stUp := strings.ToUpper(st)
			if strings.Contains(stUp, "CHARGING") && cur > 0 {
				h := (cap * (100 - pctF) / 100) / (cur / 1000)
				tr = fmt.Sprintf("%dh %dm", int(h), int(math.Mod(h, 1)*60))
			} else if strings.Contains(stUp, "DISCHARGING") && cur < 0 {
				h := (cap * pctF / 100) / (math.Abs(cur) / 1000)
				tr = fmt.Sprintf("%dh %dm", int(h), int(math.Mod(h, 1)*60))
			}
		}
		return BatData{
			Percentage: pctF, Status: st, Health: health,
			Temperature: temp, Plugged: plug, Current: cur, TimeRemaining: tr,
		}, nil
	}

	// sysfs fallback  →  Python psutil.sensors_battery() (reads same files)
	capStr := readProc("/sys/class/power_supply/battery/capacity")
	if capStr != "" {
		status := readProc("/sys/class/power_supply/battery/status")
		if status == "" {
			status = "Unknown"
		}
		healthStr := readProc("/sys/class/power_supply/battery/health")
		if healthStr == "" {
			healthStr = "N/A"
		}
		tempStr := readProc("/sys/class/power_supply/battery/temp")
		curStr := readProc("/sys/class/power_supply/battery/current_now")
		var temp float64
		if tempStr != "" {
			temp = float64(atoi(tempStr)) / 10
		}
		plug := "Not plugged"
		if strings.Contains(strings.ToUpper(status), "CHARG") {
			plug = "AC"
		}
		return BatData{
			Percentage: float64(atoi(capStr)), Status: status, Health: healthStr,
			Temperature: temp, Plugged: plug, Current: float64(atoi(curStr)),
			TimeRemaining: "N/A",
		}, nil
	}

	return BatData{Status: "N/A", Health: "N/A", Plugged: "N/A", TimeRemaining: "N/A"}, nil
}

// ══════════════════════════════════════════════════════════════════════════
// Network  →  Python _net()  (psutil.net_io_counters reads /proc/net/dev)
// ══════════════════════════════════════════════════════════════════════════

func (m *Monitor) collectNet() (NetData, error) {
	var bs, br, ps, pr, ei, eo, di, do_ int64
	raw := readProc("/proc/net/dev")
	if raw != "" {
		for _, ln := range strings.Split(raw, "\n") {
			ln = strings.TrimSpace(ln)
			if strings.Contains(ln, "|") || ln == "" {
				continue
			}
			idx := strings.Index(ln, ":")
			if idx < 0 {
				continue
			}
			fields := strings.Fields(ln[idx+1:])
			if len(fields) < 12 {
				continue
			}
			v := func(i int) int64 { n, _ := strconv.ParseInt(fields[i], 10, 64); return n }
			br += v(0)
			pr += v(1)
			ei += v(2)
			di += v(3)
			bs += v(8)
			ps += v(9)
			eo += v(10)
			do_ += v(11)
		}
	}

	now := float64(time.Now().UnixMilli()) / 1000
	dt := now - m.lastNetT
	if dt < 0.01 {
		dt = 0.01
	}
	su := math.Max(0, float64(bs-m.lastNetS)/dt)
	sd := math.Max(0, float64(br-m.lastNetR)/dt)
	m.lastNetS = bs
	m.lastNetR = br
	m.lastNetT = now

	// IP addresses  →  Python psutil.net_if_addrs()
	ip4, ip6 := "N/A", "N/A"
	ifaces, _ := net.Interfaces()
	preferred := []string{"wlan0", "Wi-Fi", "eth0", "en0", "rmnet0"}
	for _, pref := range preferred {
		for _, iface := range ifaces {
			if iface.Name != pref {
				continue
			}
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				var ipAddr net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ipAddr = v.IP
				case *net.IPAddr:
					ipAddr = v.IP
				}
				if ipAddr == nil || ipAddr.IsLoopback() {
					continue
				}
				if ipAddr.To4() != nil {
					ip4 = ipAddr.String()
				} else {
					ip6 = ipAddr.String()
				}
			}
			if ip4 != "N/A" {
				break
			}
		}
		if ip4 != "N/A" {
			break
		}
	}
	if ip4 == "N/A" {
		for _, iface := range ifaces {
			addrs, _ := iface.Addrs()
			for _, addr := range addrs {
				if ipn, ok := addr.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
					ip4 = ipn.IP.String()
					break
				}
			}
			if ip4 != "N/A" {
				break
			}
		}
	}

	return NetData{
		IP: ip4, IP6: ip6, BS: bs, BR: br, PS: ps, PR: pr, EI: ei, EO: eo, DI: di, DO: do_,
		SU: su, SD: sd,
		BSF: fmtBytes(float64(bs)), BRF: fmtBytes(float64(br)),
		SUF: fmtBytes(su) + "/s", SDF: fmtBytes(sd) + "/s",
	}, nil
}

// ══════════════════════════════════════════════════════════════════════════
// Device  →  Python _dev()
// ══════════════════════════════════════════════════════════════════════════

func (m *Monitor) collectDev() (DevData, error) {
	model := cmdRun("getprop", "ro.product.model")
	andr := cmdRun("getprop", "ro.build.version.release")
	mfr := cmdRun("getprop", "ro.product.manufacturer")
	arch := cmdRun("getprop", "ro.product.cpu.abi")
	sdk := cmdRun("getprop", "ro.build.version.sdk")

	if model == "" {
		host, _ := os.Hostname()
		model = host
		mfr = runtime.GOOS
		arch = runtime.GOARCH
	}

	kernel := "N/A"
	ver := readProc("/proc/version")
	if ver != "" {
		if idx := strings.Index(ver, "Linux version "); idx >= 0 {
			rest := ver[idx+14:]
			if sp := strings.IndexByte(rest, ' '); sp > 0 {
				kernel = rest[:sp]
			}
		}
	}

	return DevData{Model: model, Android: andr, Manufacturer: mfr, Arch: arch, SDK: sdk, Kernel: kernel}, nil
}

// ══════════════════════════════════════════════════════════════════════════
// Processes  →  Python _procs()
// ══════════════════════════════════════════════════════════════════════════

var statusMap = map[byte]string{
	'R': "running", 'S': "sleeping", 'D': "disk-sleep",
	'Z': "zombie", 'T': "stopped",
}

// parseProcStat parses /proc/[pid]/stat, handling comm fields with spaces/parens.
// Returns (name, restFields) or ("", nil) on failure.
func parseProcStat(content string) (string, []string) {
	// Find name between first '(' and last ')'
	lp := strings.IndexByte(content, '(')
	rp := strings.LastIndexByte(content, ')')
	if lp < 0 || rp < 0 || rp <= lp || rp+2 >= len(content) {
		return "", nil
	}
	name := content[lp+1 : rp]
	rest := content[rp+2:] // skip ") "
	fields := strings.Fields(rest)
	return name, fields
}

// readTotalTick reads the aggregate CPU line from /proc/stat.
func readTotalTick() int64 {
	raw := readProc("/proc/stat")
	if raw == "" {
		return 0
	}
	line := strings.SplitN(raw, "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 8 || fields[0] != "cpu" {
		return 0
	}
	var total int64
	for _, f := range fields[1:8] {
		v, _ := strconv.ParseInt(f, 10, 64)
		total += v
	}
	return total
}

func (m *Monitor) collectProcs() ([]ProcInfo, error) {
	out := make([]ProcInfo, 0, 50)
	if runtime.GOOS != "linux" {
		return out, nil
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out, fmt.Errorf("ReadDir /proc: %v", err)
	}

	// Total memory  →  for memory % calculation
	var totalMem int64
	memRaw := readProc("/proc/meminfo")
	for _, ln := range strings.Split(memRaw, "\n") {
		fields := strings.Fields(ln)
		if len(fields) >= 2 && strings.TrimSuffix(fields[0], ":") == "MemTotal" {
			v, _ := strconv.ParseInt(fields[1], 10, 64)
			totalMem = v * 1024
			break
		}
	}

	// Total CPU ticks for CPU% delta
	curTotal := readTotalTick()
	dtTotal := curTotal - m.prevTotalTick
	numCPU := runtime.NumCPU()

	newTicks := make(map[int]int64, 200)
	newCPUMap := make(map[int]float64, 200)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !rePid.MatchString(e.Name()) {
			continue
		}
		pid := atoi(e.Name())
		content := readProc(fmt.Sprintf("/proc/%d/stat", pid))
		if content == "" {
			continue
		}

		name, fields := parseProcStat(content)
		if fields == nil || len(fields) < 22 {
			continue
		}

		// Status  →  fields[0] after (name)
		st := "unknown"
		if len(fields[0]) > 0 {
			if s, ok := statusMap[fields[0][0]]; ok {
				st = s
			}
		}

		// Memory %:  RSS = fields[21] in pages (4096 bytes each)
		rss, _ := strconv.ParseInt(fields[21], 10, 64)
		var memPct float64
		if totalMem > 0 {
			memPct = round1(float64(rss*4096) / float64(totalMem) * 100)
		}

		// CPU %:  utime=fields[11], stime=fields[12]
		utime, _ := strconv.ParseInt(fields[11], 10, 64)
		stime, _ := strconv.ParseInt(fields[12], 10, 64)
		ticks := utime + stime
		newTicks[pid] = ticks

		var cpuPct float64
		if dtTotal > 0 {
			if prev, ok := m.prevProcTicks[pid]; ok {
				dt := float64(ticks - prev)
				if dt > 0 {
					cpuPct = round1(dt / float64(dtTotal) * float64(numCPU) * 100)
					if cpuPct > 100*float64(numCPU) {
						cpuPct = 100 * float64(numCPU)
					}
				}
			}
		}
		newCPUMap[pid] = cpuPct

		out = append(out, ProcInfo{PID: pid, Name: name, CPU: cpuPct, Mem: memPct, St: st})
	}

	// Update per-process tracking (single goroutine only)
	m.prevProcTicks = newTicks
	m.prevTotalTick = curTotal

	// Publish CPU cache under lock for allProcesses()
	m.mu.Lock()
	m.procCPUCache = newCPUMap
	m.mu.Unlock()

	// Sort by CPU desc, then mem desc  →  Python: sort(key=lambda x: x["cpu"], reverse=True)
	sort.Slice(out, func(i, j int) bool {
		if out[i].CPU != out[j].CPU {
			return out[i].CPU > out[j].CPU
		}
		return out[i].Mem > out[j].Mem
	})
	if len(out) > 20 {
		out = out[:20]
	}
	return out, nil
}

// ══════════════════════════════════════════════════════════════════════════
// Uptime  →  Python _uptime()
// ══════════════════════════════════════════════════════════════════════════

func (m *Monitor) getUptime() string {
	raw := readProc("/proc/uptime")
	if raw == "" {
		return "N/A"
	}
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return "N/A"
	}
	return fmtRuntime(atof(fields[0]))
}

// ══════════════════════════════════════════════════════════════════════════
// Process Search  →  Python all_procs()
// ══════════════════════════════════════════════════════════════════════════

func (m *Monitor) allProcesses(query string) []ProcSearch {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]ProcSearch, 0)
	if runtime.GOOS != "linux" {
		return out
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}

	// Read CPU cache
	m.mu.RLock()
	cpuMap := m.procCPUCache
	m.mu.RUnlock()

	// Total memory
	var totalMem int64
	memRaw := readProc("/proc/meminfo")
	for _, ln := range strings.Split(memRaw, "\n") {
		fields := strings.Fields(ln)
		if len(fields) >= 2 && strings.TrimSuffix(fields[0], ":") == "MemTotal" {
			v, _ := strconv.ParseInt(fields[1], 10, 64)
			totalMem = v * 1024
			break
		}
	}

	// System uptime for process runtime calc
	var upSec float64
	uptimeRaw := readProc("/proc/uptime")
	if uptimeRaw != "" {
		fs := strings.Fields(uptimeRaw)
		if len(fs) > 0 {
			upSec = atof(fs[0])
		}
	}

	for _, e := range entries {
		if !e.IsDir() || !rePid.MatchString(e.Name()) {
			continue
		}
		pid := atoi(e.Name())
		content := readProc(fmt.Sprintf("/proc/%d/stat", pid))
		if content == "" {
			continue
		}

		name, fields := parseProcStat(content)
		if fields == nil || len(fields) < 22 {
			continue
		}

		// Cmdline  →  Python p.cmdline()
		cmdline := readProc(fmt.Sprintf("/proc/%d/cmdline", pid))
		cmdline = strings.ReplaceAll(cmdline, "\x00", " ")
		cmdline = strings.TrimSpace(cmdline)
		if cmdline == "" {
			cmdline = name
		}

		// Filter
		if q != "" && !strings.Contains(strings.ToLower(name), q) && !strings.Contains(strings.ToLower(cmdline), q) {
			continue
		}

		// Status
		st := "unknown"
		if len(fields[0]) > 0 {
			if s, ok := statusMap[fields[0][0]]; ok {
				st = s
			}
		}

		// Memory %
		rss, _ := strconv.ParseInt(fields[21], 10, 64)
		var memPct float64
		if totalMem > 0 {
			memPct = round1(float64(rss*4096) / float64(totalMem) * 100)
		}

		// CPU% from cache
		cpuPct := float64(0)
		if cpuMap != nil {
			cpuPct = cpuMap[pid]
		}

		// Runtime  →  Python: elapsed = time.time() - create_time
		runtimeStr := "N/A"
		if upSec > 0 && len(fields) > 19 {
			startTicks, _ := strconv.ParseInt(fields[19], 10, 64)
			elapsed := upSec - float64(startTicks)/100
			if elapsed > 0 {
				runtimeStr = fmtRuntime(elapsed)
			}
		}

		cmd := cmdline
		if len(cmd) > 200 {
			cmd = cmd[:200]
		}
		out = append(out, ProcSearch{
			PID: pid, Name: name, Cmd: cmd,
			CPU: cpuPct, Mem: memPct, St: st, User: "—", Runtime: runtimeStr,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CPU != out[j].CPU {
			return out[i].CPU > out[j].CPU
		}
		return out[i].Mem > out[j].Mem
	})
	return out
}

// ══════════════════════════════════════════════════════════════════════════
// File Explorer  →  Python browse()
// ══════════════════════════════════════════════════════════════════════════

type FileItem struct {
	N  string `json:"n"`
	D  bool   `json:"d"`
	S  int64  `json:"s"`
	SF string `json:"sf"`
	I  string `json:"i"`
	P  string `json:"p"`
}

type BrowseResult struct {
	Path  string     `json:"path"`
	Items []FileItem `json:"items"`
	Error string     `json:"error,omitempty"`
}

var extIcons = map[string]string{
	"py": "code", "js": "code", "ts": "code", "sh": "term", "txt": "doc", "md": "doc",
	"jpg": "img", "png": "img", "gif": "img", "svg": "img",
	"zip": "pkg", "tar": "pkg", "gz": "pkg", "7z": "pkg",
	"mp3": "music", "mp4": "video", "pdf": "doc", "json": "code", "html": "web", "css": "code",
	"go": "code",
}

func fileIcon(name string) string {
	ext := filepath.Ext(name)
	if ext != "" {
		if icon, ok := extIcons[strings.ToLower(ext[1:])]; ok {
			return icon
		}
	}
	return "doc"
}

func browse(dirPath string) BrowseResult {
	p, err := filepath.Abs(dirPath)
	if err != nil {
		return BrowseResult{Path: dirPath, Error: err.Error()}
	}
	info, err := os.Stat(p)
	if err != nil {
		return BrowseResult{Path: p, Error: err.Error()}
	}
	if !info.IsDir() {
		return BrowseResult{Path: p, Error: "Not a directory"}
	}

	var items []FileItem
	parent := filepath.Dir(p)
	if parent != p {
		items = append(items, FileItem{N: "..", D: true, I: "dir", P: parent})
	}

	entries, err := os.ReadDir(p)
	if err != nil {
		return BrowseResult{Path: p, Error: err.Error()}
	}

	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	for _, e := range entries {
		full := filepath.Join(p, e.Name())
		isDir := e.IsDir()
		var sz int64
		if !isDir {
			if fi, err := e.Info(); err == nil {
				sz = fi.Size()
			}
		}
		icon := "dir"
		sf := ""
		if !isDir {
			icon = fileIcon(e.Name())
			sf = fmtBytes(float64(sz))
		}
		items = append(items, FileItem{N: e.Name(), D: isDir, S: sz, SF: sf, I: icon, P: full})
	}
	return BrowseResult{Path: p, Items: items}
}

// ══════════════════════════════════════════════════════════════════════════
// HTTP Server  →  Python Flask app
// ══════════════════════════════════════════════════════════════════════════

func main() {
	port := flag.Int("port", 8080, "HTTP port")
	host := flag.String("host", "0.0.0.0", "Bind address")
	flag.Parse()

	monitor := NewMonitor()

	// Find template
	execDir, _ := os.Getwd()
	htmlPath := filepath.Join(execDir, "templates", "index.html")
	if _, err := os.Stat(htmlPath); err != nil {
		exePath, _ := os.Executable()
		htmlPath = filepath.Join(filepath.Dir(exePath), "templates", "index.html")
	}

	mux := http.NewServeMux()

	// /  →  Python @app.route("/")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, htmlPath)
	})

	// /api/status  →  Python @app.route("/api/status")
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(monitor.Snap())
	})

	// /api/browse  →  Python @app.route("/api/browse")
	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Query().Get("path")
		if p == "" {
			p = "/data/data/com.termux/files/home"
			if _, err := os.Stat(p); err != nil {
				home, _ := os.UserHomeDir()
				p = home
			}
		}
		json.NewEncoder(w).Encode(browse(p))
	})

	// /api/download – serve a file as attachment
	mux.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Clean(r.URL.Query().Get("path"))
		if p == "." || p == "" {
			http.Error(w, "missing path", http.StatusBadRequest)
			return
		}
		info, err := os.Stat(p)
		if err != nil {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		if info.IsDir() {
			http.Error(w, "cannot download directory", http.StatusBadRequest)
			return
		}
		name := strings.ReplaceAll(filepath.Base(p), `"`, `\"`)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		http.ServeFile(w, r, p)
	})

	// /api/upload – accept multipart file(s) into a target directory
	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed"})
			return
		}
		dir := filepath.Clean(r.URL.Query().Get("path"))
		if dir == "." || dir == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing path"})
			return
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid directory"})
			return
		}
		if err := r.ParseMultipartForm(100 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "parse error: " + err.Error()})
			return
		}
		var saved []string
		for _, fh := range r.MultipartForm.File["file"] {
			name := filepath.Base(fh.Filename)
			if name == "." || name == "" {
				continue
			}
			dst := filepath.Join(dir, name)
			src, err := fh.Open()
			if err != nil {
				continue
			}
			out, err := os.Create(dst)
			if err != nil {
				src.Close()
				continue
			}
			io.Copy(out, src)
			out.Close()
			src.Close()
			saved = append(saved, name)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"saved": saved, "count": len(saved)})
	})

	// /api/processes  →  Python @app.route("/api/processes")
	mux.HandleFunc("/api/processes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		procs := monitor.allProcesses(q)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"query": q, "count": len(procs), "processes": procs,
		})
	})

	// /api/debug  →  Python @app.route("/api/debug")
	mux.HandleFunc("/api/debug", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"errors":     monitor.Errors(),
			"go":         runtime.Version(),
			"platform":   runtime.GOOS,
			"arch":       runtime.GOARCH,
			"pid":        os.Getpid(),
			"goroutines": runtime.NumGoroutine(),
			"mem_usage": map[string]interface{}{
				"alloc":      ms.Alloc,
				"heap_alloc": ms.HeapAlloc,
				"sys":        ms.Sys,
			},
		})
	})

	// /api/diag  →  Raw /proc diagnostics for troubleshooting on Termux
	mux.HandleFunc("/api/diag", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Read raw /proc samples
		procStat := readProc("/proc/stat")
		procStatSample := ""
		if procStat != "" {
			lines := strings.SplitN(procStat, "\n", 3)
			cnt := len(lines)
			if cnt > 2 {
				cnt = 2
			}
			procStatSample = strings.Join(lines[:cnt], "\n")
		}

		procMeminfo := readProc("/proc/meminfo")
		meminfoSample := ""
		if procMeminfo != "" {
			lines := strings.SplitN(procMeminfo, "\n", 6)
			cnt := len(lines)
			if cnt > 5 {
				cnt = 5
			}
			meminfoSample = strings.Join(lines[:cnt], "\n")
		}

		// Count readable PIDs
		readablePIDs := 0
		sampleProcStat := ""
		if runtime.GOOS == "linux" {
			entries, _ := os.ReadDir("/proc")
			for _, e := range entries {
				if e.IsDir() && rePid.MatchString(e.Name()) {
					s := readProc(fmt.Sprintf("/proc/%s/stat", e.Name()))
					if s != "" {
						readablePIDs++
						if sampleProcStat == "" {
							sampleProcStat = s
						}
					}
				}
			}
		}

		// Self stat
		selfPid := os.Getpid()
		selfStat := readProc(fmt.Sprintf("/proc/%d/stat", selfPid))
		selfName, selfFields := parseProcStat(selfStat)
		selfFieldCount := 0
		if selfFields != nil {
			selfFieldCount = len(selfFields)
		}

		// Storage probe
		storProbe := map[string]string{}
		for _, mp := range []string{"/data", "/storage/emulated/0", "/storage/emulated", "/"} {
			total, used, _, err := diskUsage(mp)
			if err != nil {
				storProbe[mp] = "error: " + err.Error()
			} else {
				storProbe[mp] = fmt.Sprintf("total=%s used=%s", fmtBytes(float64(total)), fmtBytes(float64(used)))
			}
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"platform":         runtime.GOOS,
			"arch":             runtime.GOARCH,
			"proc_stat":        procStatSample,
			"proc_meminfo":     meminfoSample,
			"readable_pids":    readablePIDs,
			"sample_proc_stat": sampleProcStat,
			"self_pid":         selfPid,
			"self_stat_name":   selfName,
			"self_stat_fields": selfFieldCount,
			"uptime_raw":       readProc("/proc/uptime"),
			"version_raw":      readProc("/proc/version"),
			"storage_probe":    storProbe,
			"cpuinfo_hw": func() string {
				raw := readProc("/proc/cpuinfo")
				for _, ln := range strings.Split(raw, "\n") {
					if strings.Contains(ln, "Hardware") || strings.Contains(ln, "model name") {
						return strings.TrimSpace(ln)
					}
				}
				return "(not found)"
			}(),
			"errors": monitor.Errors(),
		})
	})

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Println("\033[1;36m┌──────────────────────────────────────────┐\033[0m")
	fmt.Println("\033[1;36m│\033[0m  Termux Monitor Web                 \033[1;36m│\033[0m")
	fmt.Printf("\033[1;36m│\033[0m    http://localhost:%-21d\033[1;36m│\033[0m\n", *port)
	fmt.Printf("\033[1;36m│\033[0m    Goroutines: %-25d \033[1;36m│\033[0m\n", runtime.NumGoroutine())
	fmt.Println("\033[1;36m│\033[0m    Press Ctrl+C to stop                  \033[1;36m│\033[0m")
	fmt.Println("\033[1;36m└──────────────────────────────────────────┘\033[0m")

	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
