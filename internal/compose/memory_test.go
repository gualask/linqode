package compose

import "testing"

// Memory is read back to bytes from either of docker's ladders, and a reading
// that is not one stays unread rather than becoming a zero.
func TestMemBytes(t *testing.T) {
	cases := []struct {
		usage string
		want  uint64
	}{
		{"153.6MiB / 1GiB", 161061273},
		{"1.234GiB / 31.31GiB", 1324997410},
		{"512KiB", 524288},
		{"900B / 1GiB", 900},
		{"1.5MB / 2GB", 1500000},
		{" 12.3mib ", 12897484},
	}
	for _, c := range cases {
		got, ok := ContainerStats{MemUsage: c.usage}.MemBytes()
		if !ok || got != c.want {
			t.Errorf("%q read as %d (%v), want %d", c.usage, got, ok, c.want)
		}
	}
	for _, usage := range []string{"", "--", "-1MiB / 1GiB", "MiB"} {
		if got, ok := (ContainerStats{MemUsage: usage}).MemBytes(); ok {
			t.Errorf("%q read as %d, want unread", usage, got)
		}
	}
}
