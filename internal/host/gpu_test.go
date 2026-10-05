package host

// Tests for the graphics readings.
//
// Like the temperature samples, and for the same reason, these are **not**
// captured: there is no GPU on the e2e fixture, in the Linux VM under it, or
// anywhere else this suite can reach. They are written to the interfaces the
// vendors document — amdgpu's sysfs attributes and nvidia-smi's `--query-gpu`
// CSV — including the shapes that are easy to get wrong: a field the driver
// will not answer, and a DRM connector directory that looks like a second
// card. Real hardware is validated in the hardening phase.

import (
	"strings"
	"testing"
)

const gpuOutput = `#amdgpu
/sys/class/drm/card0/device/gpu_busy_percent:62
/sys/class/drm/card0/device/mem_info_vram_used:5368709120
/sys/class/drm/card0/device/mem_info_vram_total:17179869184
/sys/class/drm/card0/device/hwmon/hwmon4/temp1_input:68000
/sys/class/drm/card0/device/hwmon/hwmon4/power1_average:184000000
#nvidia
0, NVIDIA A10, 34, 4123, 23028, 61, 120.45
1, NVIDIA A10, 0, 1, 23028, 38, [Not Supported]
`

func TestParseGPUs(t *testing.T) {
	gpus := ParseGPUs([]byte(gpuOutput))
	if len(gpus) != 3 {
		t.Fatalf("read %d cards: %+v", len(gpus), gpus)
	}

	// AMD first, in card order, named by the only thing sysfs offers.
	amd := gpus[0]
	if amd.Name != "amdgpu card0" {
		t.Errorf("amd name = %q", amd.Name)
	}
	if !amd.BusyReported || amd.BusyPercent != 62 {
		t.Errorf("amd busy = %v, %v", amd.BusyPercent, amd.BusyReported)
	}
	if amd.MemTotalKB != 16*1024*1024 || amd.MemUsedKB != 5*1024*1024 {
		t.Errorf("amd memory = %d/%d KB", amd.MemUsedKB, amd.MemTotalKB)
	}
	// Temperature and power come from the hwmon the driver registers under
	// the card, which is a different directory from the card's own.
	if amd.TempMilliC != 68_000 {
		t.Errorf("amd temperature = %d", amd.TempMilliC)
	}
	if amd.PowerWatts != 184 {
		t.Errorf("amd power = %v W, want the microwatts converted", amd.PowerWatts)
	}

	nvidia := gpus[1]
	if nvidia.Name != "NVIDIA A10" || nvidia.BusyPercent != 34 {
		t.Errorf("nvidia = %+v", nvidia)
	}
	// nvidia-smi answers in mebibytes even with the units stripped.
	if nvidia.MemUsedKB != 4123*1024 || nvidia.MemTotalKB != 23028*1024 {
		t.Errorf("nvidia memory = %d/%d KB", nvidia.MemUsedKB, nvidia.MemTotalKB)
	}
	if nvidia.TempMilliC != 61_000 || nvidia.PowerWatts != 120.45 {
		t.Errorf("nvidia temperature/power = %d / %v", nvidia.TempMilliC, nvidia.PowerWatts)
	}
	if got := nvidia.MemUsedPercent(); got < 17 || got > 18 {
		t.Errorf("MemUsedPercent = %v", got)
	}

	// A field the driver will not answer is not a zero.
	if second := gpus[2]; second.PowerWatts != 0 {
		t.Errorf("an unsupported reading became %v", second.PowerWatts)
	}
	// But a real zero still is one: this card is genuinely idle.
	if second := gpus[2]; !second.BusyReported || second.BusyPercent != 0 {
		t.Errorf("an idle card = %v, %v", second.BusyPercent, second.BusyReported)
	}
}

// /sys/class/drm holds a directory per connector as well as per card, and a
// connector's `device` symlink points back at the card. Reading one as a
// second card would double every number on the screen.
func TestConnectorsAreNotCards(t *testing.T) {
	gpus := ParseGPUs([]byte(`#amdgpu
/sys/class/drm/card0/device/gpu_busy_percent:62
/sys/class/drm/card0/device/mem_info_vram_total:17179869184
/sys/class/drm/card0-DP-1/device/gpu_busy_percent:62
/sys/class/drm/card0-DP-1/device/mem_info_vram_total:17179869184
/sys/class/drm/renderD128/device/gpu_busy_percent:62
`))
	if len(gpus) != 1 {
		t.Fatalf("read %d cards, want one: %+v", len(gpus), gpus)
	}
}

// Every host with a screen has a DRM card, virtual ones included. One that
// reports neither utilisation nor memory is a display adapter, not something
// worth a row.
func TestADisplayAdapterIsNotAGPU(t *testing.T) {
	gpus := ParseGPUs([]byte(`#amdgpu
/sys/class/drm/card0/device/hwmon/hwmon2/temp1_input:41000
`))
	if len(gpus) != 0 {
		t.Errorf("a display adapter was reported as a card: %+v", gpus)
	}
}

// The common case: no AMD sysfs, no nvidia-smi, nothing to draw.
func TestNoGPUsIsNotAnEmptyCard(t *testing.T) {
	if gpus := ParseGPUs([]byte("#amdgpu\n#nvidia\n")); len(gpus) != 0 {
		t.Errorf("a host with no GPU produced %+v", gpus)
	}
	if gpus := ParseGPUs(nil); len(gpus) != 0 {
		t.Errorf("an empty answer produced %+v", gpus)
	}
}

// ParseFloat accepts "NaN" and "Inf", which no reading is. A field that says
// either is a field the driver did not answer, and stays unreported.
func TestANonFiniteNVIDIAReadingIsNotOne(t *testing.T) {
	gpus := ParseGPUs([]byte("#nvidia\n0, NVIDIA A10, NaN, Inf, 23028, -Inf, NaN\n"))
	if len(gpus) != 1 {
		t.Fatalf("got %d cards", len(gpus))
	}
	gpu := gpus[0]
	if gpu.BusyReported || gpu.MemUsedKB != 0 || gpu.TempMilliC != 0 || gpu.PowerWatts != 0 {
		t.Errorf("a non-finite field was read as a number: %+v", gpu)
	}
	if gpu.MemTotalKB != 23028*1024 {
		t.Errorf("MemTotalKB = %d, want the one finite field kept", gpu.MemTotalKB)
	}
}

func TestGPUCommandGuardsTheSlowHalf(t *testing.T) {
	command := GPUCommand()
	// The AMD half is sysfs and costs what any other /sys read costs.
	for _, want := range []string{
		"/sys/class/drm/card*/device/gpu_busy_percent",
		"/sys/class/drm/card*/device/mem_info_vram_total",
	} {
		if !strings.Contains(command, want) {
			t.Errorf("%q missing from the command", want)
		}
	}
	// The NVIDIA half is not a file read — it initialises a driver context —
	// so a host without the tool must never reach it.
	if !strings.Contains(command, "command -v nvidia-smi") {
		t.Errorf("nvidia-smi is called unguarded: %s", command)
	}
	if !strings.Contains(command, "--format=csv,noheader,nounits") {
		t.Errorf("the query is not asked for in the shape parsed: %s", command)
	}
	// A driver that hangs — a card fallen off the bus does — must not hold
	// the reading forever: it is bounded like the mount list.
	if !strings.Contains(command, "then timeout 5 nvidia-smi") ||
		!strings.Contains(command, "else nvidia-smi") {
		t.Errorf("nvidia-smi has no time limit: %s", command)
	}
	// ioreg is on every Mac and on no Linux host, so the guard is what keeps
	// a server from reporting a missing binary on stderr.
	if !strings.Contains(command, "command -v ioreg") {
		t.Errorf("ioreg is called unguarded: %s", command)
	}
	// The whole node is 46 KB of bundle names and scheduler state. Asking
	// for it and throwing it away on the client is the thing this reading
	// exists not to do.
	if !strings.Contains(command, "grep -E") {
		t.Errorf("the ioreg node is fetched whole: %s", command)
	}
}

// What this Mac's ioreg answers, verbatim. The keys come back in no
// particular order — PerformanceStatistics before model — which is why the
// node boundary is what the parser splits on.
const appleOutput = "#apple\n" +
	"+-o AGXAcceleratorG16G  <class AGXAcceleratorG16G, id 0x10000050b, registered, matched, active, busy 0 (655 ms), retain 51>\n" +
	`      "PerformanceStatistics" = {"In use system memory (driver)"=0,"Alloc system memory"=1574862848,"Tiler Utilization %"=20,"recoveryCount"=0,"Renderer Utilization %"=19,"TiledSceneBytes"=1605632,"Device Utilization %"=21,"SplitSceneCount"=0,"In use system memory"=351141888}` + "\n" +
	`      "model" = "Apple M4"` + "\n" +
	`      "gpu-core-count" = 10` + "\n"

func TestAppleSiliconReportsWhatItHasAndNothingElse(t *testing.T) {
	gpus := ParseGPUs([]byte(appleOutput))
	if len(gpus) != 1 {
		t.Fatalf("got %d cards, want 1: %+v", len(gpus), gpus)
	}
	gpu := gpus[0]
	if gpu.Name != "Apple M4 (10 cores)" {
		t.Errorf("Name = %q", gpu.Name)
	}
	if !gpu.BusyReported || gpu.BusyPercent != 21 {
		t.Errorf("busy = %v %v, want 21 reported", gpu.BusyPercent, gpu.BusyReported)
	}
	// "In use system memory", not the "(driver)" reading beside it, which is
	// 0 here and would pass unnoticed as the memory figure.
	if gpu.MemUsedKB != 351141888/1024 {
		t.Errorf("MemUsedKB = %d", gpu.MemUsedKB)
	}
	// Unified memory has no VRAM total, and hw.memsize in that field would
	// be a different quantity wearing this one's clothes. The temperature
	// and the power draw are equally absent: Apple's die sensors carry no
	// semantic name, and the power needs root.
	if gpu.MemTotalKB != 0 || gpu.TempMilliC != 0 || gpu.PowerWatts != 0 {
		t.Errorf("invented a total, a temperature or a power draw: %+v", gpu)
	}
	if gpu.MemUsedPercent() != 0 {
		t.Errorf("MemUsedPercent() = %v with no total", gpu.MemUsedPercent())
	}
}

// A node with no model is an accelerator this cannot describe, and a row
// naming nothing is worse than no row at all.
func TestAnUnnamedAcceleratorIsNotACard(t *testing.T) {
	gpus := ParseGPUs([]byte("#apple\n+-o IOAccelerator  <class IOAccelerator>\n" +
		`      "gpu-core-count" = 8` + "\n"))
	if len(gpus) != 0 {
		t.Errorf("got %+v", gpus)
	}
}

// Two accelerators in one answer stay two rows, which is what the node
// boundary is kept for.
func TestTwoAppleNodesAreTwoCards(t *testing.T) {
	gpus := ParseGPUs([]byte("#apple\n" +
		"+-o AGXAcceleratorG16G  <class A>\n" +
		`      "model" = "Apple M4"` + "\n" +
		`      "gpu-core-count" = 10` + "\n" +
		"+-o AGXAcceleratorG17G  <class B>\n" +
		`      "model" = "Apple M4 Max"` + "\n" +
		`      "gpu-core-count" = 40` + "\n"))
	if len(gpus) != 2 || gpus[0].Name != "Apple M4 (10 cores)" ||
		gpus[1].Name != "Apple M4 Max (40 cores)" {
		t.Errorf("got %+v", gpus)
	}
}

// The three vendors land in one list, in the command's order.
func TestEveryVendorInOneAnswer(t *testing.T) {
	gpus := ParseGPUs([]byte("#amdgpu\n" +
		"/sys/class/drm/card0/device/gpu_busy_percent:62\n" +
		"/sys/class/drm/card0/device/mem_info_vram_total:17179869184\n" +
		"#nvidia\n0, NVIDIA A10, 91, 21402, 23028, 74, 148.6\n" +
		appleOutput))
	if len(gpus) != 3 {
		t.Fatalf("got %d cards: %+v", len(gpus), gpus)
	}
	if gpus[0].Name != "amdgpu card0" || gpus[1].Name != "NVIDIA A10" ||
		gpus[2].Name != "Apple M4 (10 cores)" {
		t.Errorf("got %+v", gpus)
	}
}
