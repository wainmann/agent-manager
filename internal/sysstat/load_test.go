//go:build !windows

package sysstat

import "testing"

func TestSampleLoad(t *testing.T) {
	orig := loadInGuest
	t.Cleanup(func() { loadInGuest = orig })

	loadInGuest = func() bool { return false }
	var host Snapshot
	sampleLoad(&host)
	if !host.LoadOK {
		t.Fatal("load average missing outside WSL")
	}
	if host.LoadCPUs < 1 {
		t.Fatalf("load average without a CPU count: %+v", host)
	}

	loadInGuest = func() bool { return true }
	var guest Snapshot
	sampleLoad(&guest)
	if guest.LoadOK {
		t.Fatalf("guest load average reported under WSL: %+v", guest)
	}
}
