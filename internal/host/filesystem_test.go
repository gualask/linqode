package host

import (
	"reflect"
	"slices"
	"testing"
)

// The mount list is the two or three filesystems an operator watches, not
// the screenful of tmpfs and per-container overlays df prints.
func TestFilesystemsDropWhatIsNotStorage(t *testing.T) {
	m, _ := Parse([]byte(fullOutput))
	var mounts []string
	for _, filesystem := range m.Filesystems {
		mounts = append(mounts, filesystem.Mount)
	}
	if want := []string{"/", "/var"}; !reflect.DeepEqual(mounts, want) {
		t.Errorf("filesystems = %v, want %v", mounts, want)
	}
}

// `/` on a containerised host is an overlay — the e2e fixture is one — so
// the rule that drops pseudo-filesystems must never reach the root.
func TestRootSurvivesEvenAsAnOverlay(t *testing.T) {
	sample := `#mounts
Filesystem     1024-blocks    Used Available Capacity Mounted on
overlay           20509264 3145728  16316416      17% /
tmpfs              1024000   12000   1012000       2% /dev/shm
`
	m, _ := Parse([]byte(sample))
	if len(m.Filesystems) != 1 || m.Filesystems[0].Mount != "/" {
		t.Fatalf("filesystems = %+v, want the overlay root alone", m.Filesystems)
	}
	// And it stands in for the root reading when its own df did not answer.
	if m.DiskTotalKB != 20509264 {
		t.Errorf("DiskTotalKB = %d, want the root from the mount list", m.DiskTotalKB)
	}
}

func TestDedicatedDockerDiskIsMonitored(t *testing.T) {
	m, err := Parse([]byte(`#disk
/dev/root 1000 100 900 10% /
#mounts
/dev/root 1000 100 900 10% /
/dev/docker 1000 990 10 99% /var/lib/docker
overlay 1000 990 10 99% /var/lib/docker/overlay2/abc/merged
/dev/docker 1000 990 10 99% /var/lib/docker/containers/abc/mount
`))
	if err != nil {
		t.Fatal(err)
	}
	fullest, ok := m.Fullest()
	if !ok || fullest.Mount != "/var/lib/docker" || fullest.UsedPercent() != 99 {
		t.Fatalf("fullest = %+v, want the dedicated Docker disk at 99%%", fullest)
	}
	if len(m.Filesystems) != 2 {
		t.Fatalf("filesystems = %+v, want root and Docker without overlays or duplicate bind mounts", m.Filesystems)
	}
}

// Fullest is what the band's single disk meter shows: a comfortable root
// says nothing about the volume that is about to stop the deployment.
func TestFullestIsTheOneAboutToFill(t *testing.T) {
	m, _ := Parse([]byte(fullOutput))
	fullest, ok := m.Fullest()
	if !ok {
		t.Fatal("no fullest filesystem on a sample with two")
	}
	if fullest.Mount != "/var" {
		t.Errorf("fullest = %q at %.0f%%, want /var", fullest.Mount, fullest.UsedPercent())
	}
}

// What `df -Pk` prints on an Apple silicon Mac: ten lines, of which two are
// worth a row. Measured on this machine — the numbers are its own.
func TestMacSystemVolumesAreNotStorageAnyoneWatches(t *testing.T) {
	metrics, _ := Parse([]byte("#mounts\n" +
		"Filesystem 1024-blocks Used Available Capacity Mounted on\n" +
		"/dev/disk3s1s1 482797652 12341016 285838376 5% /\n" +
		"devfs 200 200 0 100% /dev\n" +
		"/dev/disk3s6 482797652 2097172 285838376 1% /System/Volumes/VM\n" +
		"/dev/disk3s2 482797652 8836432 285838376 4% /System/Volumes/Preboot\n" +
		"/dev/disk3s4 482797652 1852 285838376 1% /System/Volumes/Update\n" +
		"/dev/disk1s2 512000 6164 490348 2% /System/Volumes/xarts\n" +
		"/dev/disk1s1 512000 6120 490348 2% /System/Volumes/iSCPreboot\n" +
		"/dev/disk1s3 512000 1468 490348 1% /System/Volumes/Hardware\n" +
		"/dev/disk3s5 482797652 176673560 285838376 39% /System/Volumes/Data\n"))

	var mounts []string
	for _, filesystem := range metrics.Filesystems {
		mounts = append(mounts, filesystem.Mount)
	}
	want := []string{"/", "/System/Volumes/Data"}
	if !slices.Equal(mounts, want) {
		t.Errorf("kept %v, want %v", mounts, want)
	}
	// The Data volume is the one an operator is actually filling, and it is
	// the row the band's single meter will land on.
	fullest, found := metrics.Fullest()
	if !found || fullest.Mount != "/System/Volumes/Data" {
		t.Errorf("Fullest() = %+v, want the data volume", fullest)
	}
}

// A Linux host has no such path, and must not lose a mount to a rule written
// for another operating system.
func TestALinuxMountNamedLikeAMacVolumeIsKept(t *testing.T) {
	metrics, _ := Parse([]byte("#mounts\n" +
		"Filesystem 1024-blocks Used Available Capacity Mounted on\n" +
		"/dev/sda1 100000 50000 50000 50% /\n" +
		"/dev/sdb1 200000 10000 190000 5% /System/Volumes/Data\n"))
	if len(metrics.Filesystems) != 2 {
		t.Errorf("kept %+v, want both", metrics.Filesystems)
	}
}
