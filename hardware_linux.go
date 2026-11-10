//go:build linux

package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type linuxHardwareReader struct {
	cpuValid  bool
	read      func(string) ([]byte, error)
	usage     func() (time.Duration, error)
	at        time.Time
	cpu       time.Duration
	signature string
	epoch     uint64
	throttle  map[string]uint64
	memory    map[string]uint64
}

func newHardwareReader() hardwareReader {
	return &linuxHardwareReader{read: readHardwareFile, usage: processCPUTime, throttle: map[string]uint64{}, memory: map[string]uint64{}}
}
func readHardwareFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if len(data) > 1<<20 {
		return nil, errors.New("hardware observation exceeds bound")
	}
	return data, err
}
func processCPUTime() (time.Duration, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()), nil
}
func parseCPUList(raw string) (int, error) {
	total, last := 0, -1
	for _, part := range strings.Split(strings.TrimSpace(raw), ",") {
		values := strings.Split(part, "-")
		if len(values) > 2 {
			return 0, errors.New("invalid CPU affinity")
		}
		first, err := strconv.Atoi(values[0])
		if err != nil || first < 0 || first <= last {
			return 0, errors.New("invalid CPU affinity")
		}
		end := first
		if len(values) == 2 {
			end, err = strconv.Atoi(values[1])
			if err != nil || end < first {
				return 0, errors.New("invalid CPU affinity")
			}
		}
		if end > 1<<20 {
			return 0, errors.New("CPU affinity exceeds bound")
		}
		total += end - first + 1
		last = end
	}
	if total == 0 {
		return 0, errors.New("empty CPU affinity")
	}
	return total, nil
}
func statusValue(data []byte, key string) string {
	for _, line := range strings.Split(string(data), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && name == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func parseKB(raw string) (uint64, error) {
	fields := strings.Fields(raw)
	if len(fields) != 2 || fields[1] != "kB" {
		return 0, errors.New("invalid memory observation")
	}
	value, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || value > math.MaxUint64/1024 {
		return 0, errors.New("invalid memory observation")
	}
	return value * 1024, nil
}
func parseQuota(data []byte) (float64, error) {
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return 0, errors.New("invalid cpu.max")
	}
	period, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || period == 0 {
		return 0, errors.New("invalid CPU period")
	}
	if fields[0] == "max" {
		return math.Inf(1), nil
	}
	quota, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil || quota == 0 {
		return 0, errors.New("invalid CPU quota")
	}
	return float64(quota) / float64(period), nil
}
func parseCounters(data []byte) (map[string]uint64, error) {
	values := map[string]uint64{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, errors.New("invalid resource counter")
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, err
		}
		values[fields[0]] = value
	}
	return values, nil
}
func parsePSI(data []byte) (float64, float64, error) {
	values := map[string]float64{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, field := range fields[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok || key != "avg10" {
				continue
			}
			n, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
				return 0, 0, errors.New("invalid PSI percentage")
			}
			values[fields[0]] = n
		}
	}
	some, ok := values["some"]
	if !ok {
		return 0, 0, errors.New("PSI some unavailable")
	}
	full, ok := values["full"]
	if !ok {
		return 0, 0, errors.New("PSI full unavailable")
	}
	return some, full, nil
}
func unescapeMount(raw string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(raw)
}
func cgroupDirectories(group, mounts []byte) ([]string, error) {
	path := ""
	for _, line := range strings.Split(string(group), "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) == 3 && fields[0] == "0" && fields[1] == "" {
			path = fields[2]
			break
		}
	}
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("cgroup v2 membership unavailable")
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fs := strings.Fields(right)
		fields := strings.Fields(left)
		if len(fs) < 1 || fs[0] != "cgroup2" || len(fields) < 6 {
			continue
		}
		root, mount := unescapeMount(fields[3]), unescapeMount(fields[4])
		if !filepath.IsAbs(root) || !filepath.IsAbs(mount) {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			continue
		}
		leaf := filepath.Join(mount, relative)
		directories := []string{}
		for current := leaf; ; current = filepath.Dir(current) {
			directories = append(directories, current)
			if len(directories) > 64 {
				return nil, errors.New("cgroup hierarchy exceeds bound")
			}
			if current == mount {
				return directories, nil
			}
			if filepath.Dir(current) == current {
				break
			}
		}
	}
	return nil, errors.New("cgroup v2 mount unavailable")
}
func (r *linuxHardwareReader) sample(now time.Time) hardwareSnapshot {
	s := hardwareSnapshot{At: now, CPUState: "Unknown: CPU observations unavailable", MemoryState: "Unknown: memory observations unavailable"}
	status, statusErr := r.read("/proc/self/status")
	if rss, err := parseKB(statusValue(status, "VmRSS")); err == nil {
		s.RSS = &rss
	}
	mem, memErr := r.read("/proc/meminfo")
	if memErr == nil {
		available, e1 := parseKB(statusValue(mem, "MemAvailable"))
		total, e2 := parseKB(statusValue(mem, "MemTotal"))
		if e1 == nil && e2 == nil && total > 0 && available <= total {
			s.HostAvailable = &available
			s.HostTotal = &total
			s.MemoryState = "Measured: host availability"
		}
	}
	if data, err := r.read("/proc/pressure/memory"); err == nil {
		if some, full, err := parsePSI(data); err == nil {
			s.MemorySome = &some
			s.MemoryFull = &full
			s.MemoryState = "Measured: memory stall pressure"
		}
	}
	group, e1 := r.read("/proc/self/cgroup")
	mounts, e2 := r.read("/proc/self/mountinfo")
	directories, groupErr := cgroupDirectories(group, mounts)
	if e1 != nil || e2 != nil || groupErr != nil {
		r.at = time.Time{}
		r.cpuValid = false
		r.signature = ""
		if s.HostAvailable != nil || s.MemorySome != nil {
			s.MemoryState = "Partial: host memory only; cgroup v2 unavailable"
		}
		return s
	}
	capacity, err := parseCPUList(statusValue(status, "Cpus_allowed_list"))
	cpuKnown := err == nil && statusErr == nil
	cores := float64(capacity)
	nextThrottle, nextMemory := map[string]uint64{}, map[string]uint64{}
	signature := directories[0] + ":" + fmt.Sprint(capacity)
	maxRatio := float64(0)
	ratioKnown := false
	for _, dir := range directories {
		if data, err := r.read(filepath.Join(dir, "cpu.max")); err == nil {
			quota, err := parseQuota(data)
			if err != nil {
				cpuKnown = false
			} else {
				cores = min(cores, quota)
				signature += ":" + string(data)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			cpuKnown = false
		}
		if data, err := r.read(filepath.Join(dir, "cpu.stat")); err == nil {
			if c, err := parseCounters(data); err == nil {
				if value, ok := c["throttled_usec"]; ok {
					nextThrottle[dir] = value
				}
			}
		}
		currentData, currentErr := r.read(filepath.Join(dir, "memory.current"))
		current, parseErr := strconv.ParseUint(strings.TrimSpace(string(currentData)), 10, 64)
		for _, name := range []string{"memory.high", "memory.max"} {
			data, err := r.read(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			signature += ":" + name + ":" + string(data)
			raw := strings.TrimSpace(string(data))
			if raw == "max" {
				continue
			}
			limit, err := strconv.ParseUint(raw, 10, 64)
			if currentErr == nil && parseErr == nil && err == nil && limit > 0 {
				ratioKnown = true
				maxRatio = max(maxRatio, float64(current)/float64(limit))
			}
		}
		if data, err := r.read(filepath.Join(dir, "memory.events.local")); err == nil {
			if c, err := parseCounters(data); err == nil {
				complete := true
				for _, name := range []string{"high", "max", "oom", "oom_kill"} {
					value, ok := c[name]
					complete = complete && ok
					if ok {
						nextMemory[dir+":"+name] = value
					}
				}
				s.MemoryEventsKnown = s.MemoryEventsKnown || complete
			}
		}
		if data, err := r.read(filepath.Join(dir, "memory.pressure")); err == nil {
			if some, full, err := parsePSI(data); err == nil {
				if s.MemorySome == nil || some > *s.MemorySome {
					s.MemorySome = &some
				}
				if s.MemoryFull == nil || full > *s.MemoryFull {
					s.MemoryFull = &full
				}
			}
		}
	}
	if ratioKnown {
		s.GroupMemoryRatio = &maxRatio
		s.MemoryState = "Measured: cgroup limits and memory observations"
	}
	usage, usageErr := r.usage()
	changed := signature != r.signature
	if changed {
		r.epoch++
		r.cpuValid = false
	}
	s.Epoch = r.epoch
	if cpuKnown && cores > 0 {
		s.CPUCapacity = &cores
		s.CPUState = "Learning: CPU interval"
		if usageErr == nil && r.cpuValid && !r.at.IsZero() && now.After(r.at) && now.Sub(r.at) <= 3500*time.Millisecond && usage >= r.cpu {
			percent := (usage - r.cpu).Seconds() / now.Sub(r.at).Seconds() / cores * 100
			s.CPUPercent = &percent
			s.CPUState = "Measured: engine CPU relative to quota/affinity"
		}
	}
	if !r.at.IsZero() && now.After(r.at) && now.Sub(r.at) <= 3500*time.Millisecond {
		var throttle float64
		known := false
		for dir, value := range nextThrottle {
			if before, ok := r.throttle[dir]; ok && value >= before {
				known = true
				throttle = max(throttle, float64(value-before)/now.Sub(r.at).Seconds()/1e4)
			}
		}
		if known {
			throttle = min(100, throttle)
			s.ThrottlePercent = &throttle
		}
	}
	// Event deltas do not need a utilization interval. A long reclaim stall
	// must not erase the evidence that caused the sampler itself to stall.
	for key, before := range r.memory {
		if _, ok := nextMemory[key]; !ok {
			for _, dir := range directories {
				if strings.HasPrefix(key, dir+":") {
					// Preserve a missing source until it can be observed again;
					// another ancestor is not proof that this scope recovered.
					s.MemoryEventsKnown = false
					nextMemory[key] = before
					break
				}
			}
		}
	}
	for key, value := range nextMemory {
		if before, ok := r.memory[key]; ok && value > before {
			s.MemoryEvent = true
		}
	}
	r.at, r.cpu, r.signature = now, usage, signature
	r.throttle, r.memory = nextThrottle, nextMemory
	r.cpuValid = usageErr == nil && cpuKnown
	return s
}
