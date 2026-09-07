package host

import (
	"slices"
	"strconv"
	"strings"
)

// Filesystem is one line of df.
type Filesystem struct {
	Device  string
	Mount   string
	TotalKB uint64
	UsedKB  uint64
}

// UsedPercent is 0 for a filesystem that reports no size — which several
// pseudo-filesystems do, and which is one reason they are dropped.
func (f Filesystem) UsedPercent() float64 {
	if f.TotalKB == 0 {
		return 0
	}
	return float64(f.UsedKB) / float64(f.TotalKB) * 100
}

// parseDF reads df -Pk output, keyed by mount point. df prints a header
// line, then one line per filesystem. Fields: device, 1024-blocks, used,
// available, capacity, mountpoint — and -P guarantees they are on one line,
// however long the device name is. A mount point containing spaces is
// rejoined, since it is always the last column.
func parseDF(section string) map[string]Filesystem {
	found := map[string]Filesystem{}
	for line := range strings.Lines(section) {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[1] == "1024-blocks" {
			continue
		}
		total, errTotal := strconv.ParseUint(fields[1], 10, 64)
		used, errUsed := strconv.ParseUint(fields[2], 10, 64)
		if errTotal != nil || errUsed != nil {
			continue
		}
		mount := strings.Join(fields[5:], " ")
		found[mount] = Filesystem{
			Device: fields[0], Mount: mount, TotalKB: total, UsedKB: used}
	}
	return found
}

// pseudoDevices are the filesystems that are not storage: listing them
// would bury the two or three mounts an operator actually watches under a
// screenful of tmpfs.
var pseudoDevices = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "ramfs": true, "shm": true,
	"overlay": true, "none": true, "udev": true, "efivarfs": true,
	"proc": true, "sysfs": true, "devpts": true, "mqueue": true,
	"cgroup": true, "cgroup2": true, "hugetlbfs": true, "squashfs": true,
}

// systemMounts excludes kernel bookkeeping and container configuration
// mounts such as /etc/hosts. Docker's storage tree can have a dedicated
// disk, so its mounts are filtered by device and deduplicated instead.
var systemMounts = []string{
	"/proc", "/sys", "/dev", "/run", "/snap", "/etc",
}

// realFilesystems is the mount list worth showing, sorted by mount point.
//
// The root filesystem is kept unconditionally. On a containerised host —
// which the e2e fixture is, and which a good number of real targets are —
// `/` is itself an overlay, and a rule that dropped pseudo-filesystems
// without that exception would drop the one mount that matters most.
func realFilesystems(mounts map[string]Filesystem) []Filesystem {
	kept := make([]Filesystem, 0, len(mounts))
	for mount, filesystem := range mounts {
		if mount != "/" && !keepMount(filesystem) {
			continue
		}
		kept = append(kept, filesystem)
	}
	// A bind mount reports the same device and the same numbers under a
	// second path. Only the shortest path is kept, which is the one the
	// filesystem is actually mounted at.
	deduped := kept[:0]
	for _, filesystem := range kept {
		duplicate := false
		for index, seen := range deduped {
			if seen.Device != filesystem.Device || seen.TotalKB != filesystem.TotalKB ||
				seen.UsedKB != filesystem.UsedKB {
				continue
			}
			duplicate = true
			if len(filesystem.Mount) < len(seen.Mount) {
				deduped[index] = filesystem
			}
			break
		}
		if !duplicate {
			deduped = append(deduped, filesystem)
		}
	}
	// By mount point, which puts "/" first: no other path sorts before a
	// bare slash.
	if len(deduped) == 0 {
		return nil
	}
	slices.SortFunc(deduped, func(a, b Filesystem) int {
		return strings.Compare(a.Mount, b.Mount)
	})
	return deduped
}

// keepMount is whether a filesystem other than the root is worth a row.
func keepMount(filesystem Filesystem) bool {
	if filesystem.TotalKB == 0 || pseudoDevices[filesystem.Device] {
		return false
	}
	for _, prefix := range systemMounts {
		if filesystem.Mount == prefix || strings.HasPrefix(filesystem.Mount, prefix+"/") {
			return false
		}
	}
	return true
}

// DiskUsedPercent is 0 when the root filesystem was not reported.
func (m Metrics) DiskUsedPercent() float64 {
	if m.DiskTotalKB == 0 {
		return 0
	}
	return float64(m.DiskUsedKB) / float64(m.DiskTotalKB) * 100
}

// Fullest is the filesystem closest to full — the one worth the band's
// single disk meter. A root that is comfortable says nothing about a
// /var/lib/docker that is not, and a full one is among the most common
// causes of a deployment that stopped working.
func (m Metrics) Fullest() (Filesystem, bool) {
	var fullest Filesystem
	found := false
	for _, filesystem := range m.Filesystems {
		if !found || filesystem.UsedPercent() > fullest.UsedPercent() {
			fullest, found = filesystem, true
		}
	}
	if !found && m.DiskTotalKB > 0 {
		return Filesystem{Mount: "/", TotalKB: m.DiskTotalKB, UsedKB: m.DiskUsedKB}, true
	}
	return fullest, found
}
