package compose

// Tests for the kernel's own container counters. The v2 sample is real output
// captured from the fixture as the unprivileged operator account; the v1
// sample is the same readings in the layout an older host reports them in,
// which is the one shape that cannot be checked against the fixture.

import (
	"strings"
	"testing"
	"time"
)

const v2Sample = `#cgcpu
/sys/fs/cgroup/docker/aaaaaaaaaaaa1111/cpu.stat:usage_usec 7655480
/sys/fs/cgroup/system.slice/docker-bbbbbbbbbbbb2222.scope/cpu.stat:usage_usec 154782224
#cgmem
/sys/fs/cgroup/docker/aaaaaaaaaaaa1111/memory.current:12939428
/sys/fs/cgroup/system.slice/docker-bbbbbbbbbbbb2222.scope/memory.current:655360
#cgfile
/sys/fs/cgroup/docker/aaaaaaaaaaaa1111/memory.stat:inactive_file 1939428
/sys/fs/cgroup/system.slice/docker-bbbbbbbbbbbb2222.scope/memory.stat:inactive_file 0
#cgmax
/sys/fs/cgroup/docker/aaaaaaaaaaaa1111/memory.max:max
/sys/fs/cgroup/system.slice/docker-bbbbbbbbbbbb2222.scope/memory.max:2147483648
#cgio
/sys/fs/cgroup/docker/aaaaaaaaaaaa1111/io.stat:253:16 rbytes=4100 wbytes=8190 rios=1 wios=2 dbytes=0 dios=0
#cgpids
/sys/fs/cgroup/docker/aaaaaaaaaaaa1111/pids.current:14
#cgnet
/proc/469832/net/dev:    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
/proc/469832/net/dev:  eth0:    1146      15    0    0    0     0          0         0      126       3    0    0    0     0       0          0
`

// The same two containers on a cgroup v1 host: a controller per directory,
// CPU in nanoseconds, the page cache under a different key, and one line per
// operation in the blkio file.
const v1Sample = `#cgcpu
/sys/fs/cgroup/cpuacct/docker/aaaaaaaaaaaa1111/cpuacct.usage:7655480000
#cgmem
/sys/fs/cgroup/memory/docker/aaaaaaaaaaaa1111/memory.usage_in_bytes:12939428
#cgfile
/sys/fs/cgroup/memory/docker/aaaaaaaaaaaa1111/memory.stat:inactive_file 1939428
/sys/fs/cgroup/memory/docker/aaaaaaaaaaaa1111/memory.stat:total_inactive_file 1939428
#cgmax
/sys/fs/cgroup/memory/docker/aaaaaaaaaaaa1111/memory.limit_in_bytes:9223372036854771712
#cgio
/sys/fs/cgroup/blkio/docker/aaaaaaaaaaaa1111/blkio.throttle.io_service_bytes:253:16 Read 4100
/sys/fs/cgroup/blkio/docker/aaaaaaaaaaaa1111/blkio.throttle.io_service_bytes:253:16 Write 8190
/sys/fs/cgroup/blkio/docker/aaaaaaaaaaaa1111/blkio.throttle.io_service_bytes:253:16 Total 12290
#cgpids
/sys/fs/cgroup/pids/docker/aaaaaaaaaaaa1111/pids.current:14
`

func TestParseCgroupSampleV2(t *testing.T) {
	sample := ParseCgroupSample([]byte(v2Sample), time.Unix(100, 0))

	first := sample.Containers["aaaaaaaaaaaa1111"]
	if first.CPUMicros != 7655480 {
		t.Errorf("CPUMicros = %d", first.CPUMicros)
	}
	// Reclaimable page cache is not what the container is using, and docker
	// subtracts it too.
	if first.MemBytes != 12939428-1939428 {
		t.Errorf("MemBytes = %d, want the cache subtracted", first.MemBytes)
	}
	if first.MemLimitBytes != 0 {
		t.Errorf("MemLimitBytes = %d, want none for an unlimited container", first.MemLimitBytes)
	}
	if first.ReadBytes != 4100 || first.WriteBytes != 8190 {
		t.Errorf("io = %d/%d", first.ReadBytes, first.WriteBytes)
	}
	if first.PIDs != 14 {
		t.Errorf("PIDs = %d", first.PIDs)
	}

	// The systemd driver names the directory differently and must reach the
	// same id.
	second := sample.Containers["bbbbbbbbbbbb2222"]
	if second.CPUMicros != 154782224 {
		t.Errorf("the .scope layout did not resolve to a container: %+v", sample.Containers)
	}
	if second.MemLimitBytes != 2147483648 {
		t.Errorf("MemLimitBytes = %d, want the container's own limit", second.MemLimitBytes)
	}

	// Loopback is not traffic, so only eth0's counters are summed.
	network, ok := sample.Networks[469832]
	if !ok {
		t.Fatalf("no network reading: %+v", sample.Networks)
	}
	if network.RxBytes != 1146 || network.TxBytes != 126 {
		t.Errorf("net = %d/%d, want eth0 alone", network.RxBytes, network.TxBytes)
	}
}

func TestParseCgroupSampleV1(t *testing.T) {
	sample := ParseCgroupSample([]byte(v1Sample), time.Unix(100, 0))
	reading := sample.Containers["aaaaaaaaaaaa1111"]

	// v1 counts nanoseconds where v2 counts microseconds.
	if reading.CPUMicros != 7655480 {
		t.Errorf("CPUMicros = %d, want the nanoseconds converted", reading.CPUMicros)
	}
	if reading.MemBytes != 12939428-1939428 {
		t.Errorf("MemBytes = %d, want total_inactive_file subtracted", reading.MemBytes)
	}
	// v1 writes a number so large it means "no limit".
	if reading.MemLimitBytes != 0 {
		t.Errorf("MemLimitBytes = %d, want none", reading.MemLimitBytes)
	}
	// Read and Write are summed; Total is their sum and must not be counted
	// again.
	if reading.ReadBytes != 4100 || reading.WriteBytes != 8190 {
		t.Errorf("io = %d/%d", reading.ReadBytes, reading.WriteBytes)
	}
	if reading.PIDs != 14 {
		t.Errorf("PIDs = %d", reading.PIDs)
	}
}

func TestCgroupCacheUsesOneCounterRegardlessOfOrder(t *testing.T) {
	const path = "/sys/fs/cgroup/memory/docker/aaaaaaaaaaaa1111/memory.stat:"
	for _, tc := range []struct {
		name, fields string
		want         uint64
	}{
		{"local before total", path + "inactive_file 100\n" + path + "total_inactive_file 250\n", 750},
		{"total before local", path + "total_inactive_file 250\n" + path + "inactive_file 100\n", 750},
		{"zero total", path + "total_inactive_file 0\n" + path + "inactive_file 100\n", 1000},
		{"cache exceeds usage", path + "inactive_file 100\n" + path + "total_inactive_file 1100\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "#cgmem\n/sys/fs/cgroup/memory/docker/aaaaaaaaaaaa1111/memory.usage_in_bytes:1000\n#cgfile\n" + tc.fields
			sample := ParseCgroupSample([]byte(raw), time.Unix(100, 0))
			if got := sample.Containers["aaaaaaaaaaaa1111"].MemBytes; got != tc.want {
				t.Fatalf("memory = %d, want %d", got, tc.want)
			}
		})
	}
}

// A percentage is the difference between two readings — the work `docker
// stats` spends two seconds doing on the server.
func TestDeltaDerivesThePercentages(t *testing.T) {
	// The short id `ps` reports, against the full one the cgroup path
	// carries: the mismatch the fixture caught and no captured sample would
	// have.
	services := []Service{{Service: "web", Name: "app-web-1", ID: "aaaaaaaaaaaa", Pid: 469832}}
	const gigabyte = 1 << 30

	before := ParseCgroupSample([]byte(v2Sample), time.Unix(100, 0))
	after := ParseCgroupSample([]byte(v2Sample), time.Unix(105, 0))
	// Half a core busy over five seconds.
	reading := after.Containers["aaaaaaaaaaaa1111"]
	reading.CPUMicros += 2_500_000
	after.Containers["aaaaaaaaaaaa1111"] = reading

	stats := before.Delta(after, services, 4*gigabyte)
	if len(stats) != 1 {
		t.Fatalf("got %d readings", len(stats))
	}
	if stats[0].CPUPerc != "50.00%" {
		t.Errorf("CPUPerc = %q, want half a core", stats[0].CPUPerc)
	}
	// No limit of its own: the machine's memory is the ceiling, as docker
	// shows it.
	if !strings.HasSuffix(stats[0].MemUsage, "/ 4GiB") {
		t.Errorf("MemUsage = %q, want the host's memory as the limit", stats[0].MemUsage)
	}
	if stats[0].NetAmount() != "1.146kB/126B" {
		t.Errorf("NetAmount = %q", stats[0].NetAmount())
	}
	if stats[0].BlockAmount() != "4.1kB/8.19kB" {
		t.Errorf("BlockAmount = %q", stats[0].BlockAmount())
	}
	if percent, ok := stats[0].MemPercent(); !ok || percent < 0.2 || percent > 0.3 {
		t.Errorf("MemPercent = %v, %v", percent, ok)
	}
}

// The counters accumulated over the host's interval, not over the gap
// between two replies arriving. Five seconds apart on the host, two apart on
// the client — the first reply held up by the link — and half a core busy
// is still half a core, not 125%.
func TestDeltaDividesByTheHostsClock(t *testing.T) {
	services := []Service{{Name: "app-web-1", ID: "aaaaaaaaaaaa"}}
	before := ParseCgroupSample([]byte("#cguptime\n3600.00 14000.50\n"+v2Sample), time.Unix(100, 0))
	after := ParseCgroupSample([]byte("#cguptime\n3605.00 14010.50\n"+v2Sample), time.Unix(102, 0))
	if after.Uptime != 3605*time.Second {
		t.Fatalf("Uptime = %v", after.Uptime)
	}
	reading := after.Containers["aaaaaaaaaaaa1111"]
	reading.CPUMicros += 2_500_000
	after.Containers["aaaaaaaaaaaa1111"] = reading

	if got := before.Delta(after, services, 0)[0].CPUPerc; got != "50.00%" {
		t.Errorf("CPUPerc = %q, want half a core over the host's five seconds", got)
	}
	// A host that did not answer leaves the client's clock to stand in.
	noClock := ParseCgroupSample([]byte(v2Sample), time.Unix(102, 0))
	noClock.Containers["aaaaaaaaaaaa1111"] = reading
	if got := before.Delta(noClock, services, 0)[0].CPUPerc; got != "125.00%" {
		t.Errorf("CPUPerc = %q, want the client's two seconds as the fallback", got)
	}
	if !strings.Contains(StatsCgroupCommand(nil), "cat /proc/uptime") {
		t.Error("the host's clock is not read with the counters")
	}
}

// `docker restart` keeps the container's id and starts its cgroup from zero,
// so the counter goes backwards across the interval. That interval belongs
// to no single life of the container: no percentage, not 0%.
func TestACounterResetIsNoReading(t *testing.T) {
	services := []Service{{Name: "app-web-1", ID: "aaaaaaaaaaaa"}}
	before := ParseCgroupSample([]byte(v2Sample), time.Unix(100, 0))
	after := ParseCgroupSample([]byte(v2Sample), time.Unix(105, 0))
	reading := after.Containers["aaaaaaaaaaaa1111"]
	reading.CPUMicros = 1_000
	after.Containers["aaaaaaaaaaaa1111"] = reading

	stats := before.Delta(after, services, 0)
	if _, ok := stats[0].CPUPercent(); ok {
		t.Errorf("CPUPerc = %q across a restart, want none", stats[0].CPUPerc)
	}
}

// A container on the host's network reads the host's interfaces through its
// process. Those are the machine's counters, and NET says unknown rather than
// attributing them to one container.
func TestAHostNetworkContainerHasNoNetworkReading(t *testing.T) {
	services := []Service{{Name: "app-agent-1", ID: "aaaaaaaaaaaa", Pid: 469832, HostNetwork: true}}
	sample := ParseCgroupSample([]byte(v2Sample), time.Unix(100, 0))
	stats := (CgroupSample{}).Delta(sample, services, 0)
	if len(stats) != 1 {
		t.Fatalf("got %d readings", len(stats))
	}
	if got := stats[0].NetAmount(); got != "-" {
		t.Errorf("NetAmount = %q, want unknown", got)
	}
}

// The first reading after connecting has nothing to be measured against. It
// still fills in everything a single reading can say.
func TestDeltaWithoutAPreviousReading(t *testing.T) {
	services := []Service{{Name: "app-web-1", ID: "aaaaaaaaaaaa1111"}}
	sample := ParseCgroupSample([]byte(v2Sample), time.Unix(100, 0))

	stats := (CgroupSample{}).Delta(sample, services, 0)
	if len(stats) != 1 {
		t.Fatalf("got %d readings", len(stats))
	}
	if _, ok := stats[0].CPUPercent(); ok {
		t.Errorf("CPUPerc = %q, want no percentage from one reading", stats[0].CPUPerc)
	}
	if stats[0].MemAmount() == "" {
		t.Error("memory is a single reading and should be there")
	}
}

// A container the sample does not know about — one that stopped between the
// service list and the reading — is left out rather than reported as zero.
func TestDeltaSkipsWhatItDidNotRead(t *testing.T) {
	services := []Service{{Name: "app-gone-1", ID: "ffffffffffff9999"}}
	sample := ParseCgroupSample([]byte(v2Sample), time.Unix(100, 0))
	if stats := (CgroupSample{}).Delta(sample, services, 0); len(stats) != 0 {
		t.Errorf("got %+v, want nothing for a container with no counters", stats)
	}
}

func TestStatsCgroupCommandCoversBothLayouts(t *testing.T) {
	command := StatsCgroupCommand([]int{4242})
	for _, want := range []string{
		"/sys/fs/cgroup/docker/*/cpu.stat",                    // v2, cgroupfs driver
		"/sys/fs/cgroup/system.slice/docker-*.scope/cpu.stat", // v2, systemd driver
		"/sys/fs/cgroup/cpuacct/docker/*/cpuacct.usage",       // v1
		"/proc/4242/net/dev",                                  // the network namespace
	} {
		if !strings.Contains(command, want) {
			t.Errorf("%q missing from the command", want)
		}
	}
	// A reading that does not exist on this host must not fail the rest.
	if !strings.Contains(command, "2>/dev/null") {
		t.Error("a missing path would put an error in the output")
	}
}

// v1 with the systemd driver, and rootless docker under the user's own
// systemd manager, put a container where neither of the layouts above looks.
// Both must be asked for, and both must reach the container.
func TestTheSystemdV1AndRootlessLayoutsReachTheirContainers(t *testing.T) {
	command := StatsCgroupCommand(nil)
	for _, want := range []string{
		"/sys/fs/cgroup/cpuacct/system.slice/docker-*.scope/cpuacct.usage",
		"/sys/fs/cgroup/memory/system.slice/docker-*.scope/memory.usage_in_bytes",
		"/sys/fs/cgroup/memory/system.slice/docker-*.scope/memory.stat",
		"/sys/fs/cgroup/memory/system.slice/docker-*.scope/memory.limit_in_bytes",
		"/sys/fs/cgroup/blkio/system.slice/docker-*.scope/blkio.throttle.io_service_bytes",
		"/sys/fs/cgroup/pids/system.slice/docker-*.scope/pids.current",
		"/sys/fs/cgroup/user.slice/user-*.slice/user@*.service/user.slice/docker-*.scope/cpu.stat",
		"/sys/fs/cgroup/user.slice/user-*.slice/user@*.service/user.slice/docker-*.scope/memory.current",
	} {
		if !strings.Contains(command, want) {
			t.Errorf("%q missing from the command", want)
		}
	}

	const v1 = "/sys/fs/cgroup/%s/system.slice/docker-cccccccccccc3333.scope/"
	const rootless = "/sys/fs/cgroup/user.slice/user-1000.slice/user@1000.service/user.slice/docker-dddddddddddd4444.scope/"
	raw := strings.NewReplacer("%s", "cpuacct").Replace("#cgcpu\n"+v1+"cpuacct.usage:2000000000\n") +
		rootless + "cpu.stat:usage_usec 3000000\n" +
		"#cgmem\n" + strings.Replace(v1, "%s", "memory", 1) + "memory.usage_in_bytes:4096\n" +
		rootless + "memory.current:8192\n" +
		"#cgpids\n" + strings.Replace(v1, "%s", "pids", 1) + "pids.current:3\n"
	sample := ParseCgroupSample([]byte(raw), time.Unix(100, 0))

	if got := sample.Containers["cccccccccccc3333"]; got.CPUMicros != 2_000_000 || got.MemBytes != 4096 || got.PIDs != 3 {
		t.Errorf("v1 systemd container = %+v", got)
	}
	if got := sample.Containers["dddddddddddd4444"]; got.CPUMicros != 3_000_000 || got.MemBytes != 8192 {
		t.Errorf("rootless container = %+v", got)
	}
	if len(sample.Containers) != 2 {
		t.Errorf("containers = %+v, want exactly the two", sample.Containers)
	}
}

// Formatting matches what docker prints, because the live stream still comes
// from docker and one table must not show two shapes of the same number.
func TestByteFormatting(t *testing.T) {
	cases := []struct {
		bytes           uint64
		binary, decimal string
	}{
		{0, "0B", "0B"},
		{999, "999B", "999B"},
		{4100, "4.004KiB", "4.1kB"},
		{12939428, "12.34MiB", "12.94MB"},
		{1325142016, "1.234GiB", "1.325GB"},
	}
	for _, test := range cases {
		if got := formatBinary(test.bytes); got != test.binary {
			t.Errorf("formatBinary(%d) = %q, want %q", test.bytes, got, test.binary)
		}
		if got := formatDecimal(test.bytes); got != test.decimal {
			t.Errorf("formatDecimal(%d) = %q, want %q", test.bytes, got, test.decimal)
		}
	}
}

// `compose ps` reports the short id; the cgroup directory is named after the
// full one. Matching them is not a leniency, it is the normal case.
func TestReadingMatchesTheShortIDDockerReports(t *testing.T) {
	sample := ParseCgroupSample([]byte(v2Sample), time.Unix(100, 0))
	if _, ok := sample.Reading("aaaaaaaaaaaa"); !ok {
		t.Error("a twelve-character id did not find its container")
	}
	if _, ok := sample.Reading("aaaaaaaaaaaa1111"); !ok {
		t.Error("the full id did not find its container")
	}
	// Too short to be unambiguous: docker would not print it either.
	if _, ok := sample.Reading("aaaa"); ok {
		t.Error("a four-character prefix matched a container")
	}
	if _, ok := sample.Reading("ffffffffffff"); ok {
		t.Error("an unknown id matched a container")
	}
}
