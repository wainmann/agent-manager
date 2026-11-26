package sysstat

import "testing"

func TestSampleLoadLeavesWindowsWithoutOne(t *testing.T) {
	var snap Snapshot
	sampleLoad(&snap)
	if snap.LoadOK {
		t.Fatalf("Windows reported a load average: %+v", snap)
	}
}
