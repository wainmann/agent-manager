//go:build !windows

package sysstat

import (
	"github.com/shirou/gopsutil/v4/load"

	"github.com/YoanWai/agent-manager/internal/wsl"
)

var loadInGuest = wsl.Detect

// Under WSL the kernel's load average is the guest VM's, and it would sit
// beside the Windows host's CPU and memory, so it is left out.
func sampleLoad(snap *Snapshot) {
	if loadInGuest() {
		return
	}
	avg, err := load.Avg()
	if err != nil {
		return
	}
	snap.Load1, snap.Load5, snap.Load15 = avg.Load1, avg.Load5, avg.Load15
	snap.LoadCPUs = LogicalCPUs()
	snap.LoadOK = true
}
