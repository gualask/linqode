package host

import (
	"reflect"
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
