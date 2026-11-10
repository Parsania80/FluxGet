//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hardwareFixture() (*linuxHardwareReader, map[string]string, *time.Duration) {
	files := map[string]string{
		"/proc/self/status":              "VmRSS:\t4096 kB\nCpus_allowed_list:\t0-7\n",
		"/proc/meminfo":                  "MemTotal: 1048576 kB\nMemAvailable: 800000 kB\n",
		"/proc/pressure/memory":          "some avg10=0.00 avg60=0 avg300=0 total=0\nfull avg10=0.00 avg60=0 avg300=0 total=0\n",
		"/proc/self/cgroup":              "0::/parent/leaf\n",
		"/proc/self/mountinfo":           "1 2 0:3 / /cg rw - cgroup2 cgroup rw\n",
		"/cg/parent/leaf/cpu.max":        "max 100000",
		"/cg/parent/cpu.max":             "50000 100000",
		"/cg/parent/cpu.stat":            "usage_usec 1\nthrottled_usec 0\n",
		"/cg/parent/memory.high":         "104857600",
		"/cg/parent/memory.max":          "209715200",
		"/cg/parent/memory.current":      "52428800",
		"/cg/parent/memory.events.local": "high 0\nmax 0\noom 0\noom_kill 0\n",
	}
	usage := time.Duration(0)
	reader := &linuxHardwareReader{read: func(path string) ([]byte, error) {
		if value, ok := files[path]; ok {
			return []byte(value), nil
		}
		return nil, os.ErrNotExist
	}, usage: func() (time.Duration, error) { return usage, nil }, throttle: map[string]uint64{}, memory: map[string]uint64{}}
	return reader, files, &usage
}
func TestHardwareHierarchicalQuotaAndMemory(t *testing.T) {
	r, files, usage := hardwareFixture()
	now := time.Unix(100, 0)
	first := r.sample(now)
	if first.CPUPercent != nil || first.CPUCapacity == nil || *first.CPUCapacity != .5 {
		t.Fatal("quota must bound eight available CPUs", first)
	}
	*usage = 500 * time.Millisecond
	files["/cg/parent/cpu.stat"] = "usage_usec 500000\nthrottled_usec 250000\n"
	files["/cg/parent/memory.events.local"] = "high 1\nmax 0\noom 0\noom_kill 0\n"
	measured := r.sample(now.Add(time.Second))
	if measured.CPUPercent == nil || *measured.CPUPercent != 100 || measured.ThrottlePercent == nil || *measured.ThrottlePercent != 25 {
		t.Fatal("quota-normalized observations wrong", measured)
	}
	if measured.GroupMemoryRatio == nil || *measured.GroupMemoryRatio != .5 || !measured.MemoryEvent || measured.RSS == nil || *measured.RSS != 4096*1024 {
		t.Fatal("memory observations wrong", measured)
	}
	files["/cg/parent/cpu.max"] = "25000 100000"
	changed := r.sample(now.Add(2 * time.Second))
	if changed.Epoch == measured.Epoch || changed.CPUPercent != nil || *changed.CPUCapacity != .25 {
		t.Fatal("quota change reused a baseline", changed)
	}
}
func TestHardwareMissingMalformedAndResetObservations(t *testing.T) {
	for _, kind := range []string{"v1", "mount", "affinity", "quota", "counter-reset", "stale"} {
		t.Run(kind, func(t *testing.T) {
			r, f, usage := hardwareFixture()
			now := time.Unix(100, 0)
			r.sample(now)
			*usage = time.Second
			switch kind {
			case "v1":
				f["/proc/self/cgroup"] = "2:cpu:/group"
			case "mount":
				delete(f, "/proc/self/mountinfo")
			case "affinity":
				f["/proc/self/status"] = "Cpus_allowed_list: broken\n"
			case "quota":
				f["/cg/parent/cpu.max"] = "bad 0"
			case "counter-reset":
				*usage = -time.Second
			case "stale":
				now = now.Add(5 * time.Second)
			}
			s := r.sample(now.Add(time.Second))
			if s.CPUPercent != nil {
				t.Fatal("unknown CPU fabricated a utilization", s)
			}
		})
	}
	r, f, usage := hardwareFixture()
	now := time.Unix(100, 0)
	r.sample(now)
	f["/proc/self/status"] = "Cpus_allowed_list: invalid\n"
	f["/cg/parent/memory.events.local"] = "high 2\nmax 0\noom 0\noom_kill 0\n"
	*usage = time.Millisecond
	if s := r.sample(now.Add(time.Second)); !s.MemoryEvent {
		t.Fatal("missing CPU observations hid a memory event")
	}
}
func TestHardwareParsersAndMountBoundaries(t *testing.T) {
	for _, raw := range []string{"", "2-1", "1,1", "3-4,2", "0-1-2", "-2", "0-99999999"} {
		if _, err := parseCPUList(raw); err == nil {
			t.Fatal("accepted invalid affinity", raw)
		}
	}
	if n, err := parseCPUList("0-3,8,10-11"); err != nil || n != 7 {
		t.Fatal(n, err)
	}
	for _, raw := range []string{"max 0", "0 100000", "bad 2", "4", "4 2 1"} {
		if _, err := parseQuota([]byte(raw)); err == nil {
			t.Fatal("accepted invalid quota", raw)
		}
	}
	for _, raw := range []string{"some avg10=NaN\nfull avg10=0", "some avg10=101\nfull avg10=0", "some avg10=0", "some avg10=-1\nfull avg10=0"} {
		if _, _, err := parsePSI([]byte(raw)); err == nil {
			t.Fatal("accepted invalid PSI", raw)
		}
	}
	dirs, err := cgroupDirectories([]byte("0::/parent/child\n"), []byte("1 2 0:3 /parent /cg\\040space rw - cgroup2 cgroup rw\n"))
	if err != nil || strings.Join(dirs, ",") != "/cg space/child,/cg space" {
		t.Fatal("mount root or escaping wrong", dirs, err)
	}
	for _, path := range []string{"/parent/../outside", "/outside", "relative"} {
		if _, err := cgroupDirectories([]byte("0::"+path), []byte("1 2 0:3 /parent /cg rw - cgroup2 cgroup rw")); err == nil {
			t.Fatal("escaped mount boundary", path)
		}
	}
}
func TestHardwareNativeReadOnlyProbe(t *testing.T) {
	reader := newHardwareReader()
	first := reader.sample(time.Now())
	time.Sleep(25 * time.Millisecond)
	second := reader.sample(time.Now())
	t.Logf("native observations: CPU=%s memory=%s", second.CPUState, second.MemoryState)
	if second.CPUCapacity != nil {
		if *second.CPUCapacity <= 0 {
			t.Fatal("invalid CPU capacity")
		}
		t.Logf("available CPU capacity %.3f", *second.CPUCapacity)
	}
	if first.RSS != nil && *first.RSS == 0 {
		t.Fatal("invalid RSS")
	}
}

// Explicitly invoked in an isolated systemd cgroup; the ordinary suite never
// changes host limits or allocates enough memory to induce an OOM.
func TestHardwareConstrainedNativeProbe(t *testing.T) {
	if os.Getenv("FLUXGET_RESOURCE_SMOKE") != "1" {
		t.Skip("requires explicit isolated cgroup")
	}
	reader := newHardwareReader()
	reader.sample(time.Now())
	until := time.Now().Add(1500 * time.Millisecond)
	var work uint64
	for time.Now().Before(until) {
		for i := 0; i < 100000; i++ {
			work = work*1664525 + 1013904223
		}
	}
	s := reader.sample(time.Now())
	if s.CPUCapacity == nil || *s.CPUCapacity > .51 || s.CPUPercent == nil || *s.CPUPercent < 70 {
		t.Fatalf("CPU quota was not observed: %+v", s)
	}
	if s.GroupMemoryRatio == nil {
		t.Fatal("memory cgroup limit unavailable")
	}
	t.Logf("constrained native probe: CPUs=%.2f CPU=%.1f%% group memory=%.3f checksum=%d", *s.CPUCapacity, *s.CPUPercent, *s.GroupMemoryRatio, work)
}
func TestStorageDeviceRolesRemainSeparate(t *testing.T) {
	dir, err := os.MkdirTemp("/dev/shm", "fluxget-observation-")
	if err != nil {
		t.Skip(err)
	}
	defer os.RemoveAll(dir)
	file, err := os.Create(filepath.Join(t.TempDir(), "data.part"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, err = file.WriteString("verified")
	if err != nil {
		t.Fatal(err)
	}
	m := &transferMeasurements{}
	c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: strings.TrimSuffix(file.Name(), ".part"), Partial: file.Name(), Completed: 8}
	if err := saveCheckpointMeasured(filepath.Join(dir, "checkpoint.json"), file, c, nil, m); err != nil {
		t.Fatal(err)
	}
	s := m.storageSnapshot()
	if s.Destination == "" || s.Checkpoint == "" {
		t.Fatal("filesystem observations unavailable")
	}
	if s.Destination == s.Checkpoint {
		t.Skip("temporary directories share a filesystem")
	}
	for _, kind := range []int{storageDataSync, storageCheckpointWrite, storageCheckpointSync, storageCheckpointPublish} {
		if _, ok := storageP95(s, storageCounters{}, kind); !ok {
			t.Fatal("missing measured storage operation", fmt.Sprint(kind))
		}
	}
}

func TestHardwareConstrainedMemoryProbe(t *testing.T) {
	if os.Getenv("FLUXGET_RESOURCE_MEMORY_SMOKE") != "1" {
		t.Skip("requires explicit isolated memory cgroup")
	}
	reader := newHardwareReader()
	s := reader.sample(time.Now())
	if s.GroupMemoryRatio == nil || *s.GroupMemoryRatio <= 0 || s.RSS == nil || *s.RSS == 0 {
		t.Fatalf("memory limits or process memory were not observed: %+v", s)
	}
	t.Logf("bounded native memory probe: cgroup ratio %.3f; engine RSS %d bytes", *s.GroupMemoryRatio, *s.RSS)
}

func TestHardwareMemoryEventsSurviveLongGapsAndIgnoreSiblings(t *testing.T) {
	r, f, _ := hardwareFixture()
	now := time.Unix(100, 0)
	r.sample(now)
	f["/cg/parent/memory.events"] = "high 100\nmax 0\noom 0\noom_kill 0\n"
	if s := r.sample(now.Add(time.Second)); s.MemoryEvent {
		t.Fatal("hierarchical sibling events were attributed to this process")
	}
	f["/cg/parent/memory.events.local"] = "high 1\nmax 0\noom 0\noom_kill 0\n"
	if s := r.sample(now.Add(10 * time.Second)); !s.MemoryEvent || s.CPUPercent != nil {
		t.Fatal("long gaps hid a real event or fabricated CPU utilization", s)
	}
}

func TestHardwareMissingMemoryEventsStayUnknown(t *testing.T) {
	r, f, _ := hardwareFixture()
	now := time.Unix(100, 0)
	r.sample(now)
	f["/cg/parent/memory.events.local"] = "high 1\nmax 0\noom 0\noom_kill 0\n"
	if !r.sample(now.Add(time.Second)).MemoryEvent {
		t.Fatal("event not detected")
	}
	delete(f, "/cg/parent/memory.events.local")
	for second := 2; second < 5; second++ {
		if r.sample(now.Add(time.Duration(second) * time.Second)).MemoryEventsKnown {
			t.Fatal("missing source reported known")
		}
	}
	f["/cg/parent/memory.events.local"] = "high 2\nmax 0\noom 0\noom_kill 0\n"
	s := r.sample(now.Add(5 * time.Second))
	if !s.MemoryEventsKnown || !s.MemoryEvent {
		t.Fatal("event evidence lost over missing interval")
	}
}
