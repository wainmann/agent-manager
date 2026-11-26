//go:build !windows

package sysstat

import (
	"errors"
	"testing"

	"github.com/shirou/gopsutil/v4/load"
)

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

func TestSampleLoadWithoutAReading(t *testing.T) {
	origGuest, origAvg := loadInGuest, loadAvg
	t.Cleanup(func() { loadInGuest, loadAvg = origGuest, origAvg })
	loadInGuest = func() bool { return false }
	loadAvg = func() (*load.AvgStat, error) { return nil, errors.New("no load average") }

	var snap Snapshot
	sampleLoad(&snap)
	if snap != (Snapshot{}) {
		t.Fatalf("failed read left a load average behind: %+v", snap)
	}
}
