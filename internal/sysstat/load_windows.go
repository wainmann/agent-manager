package sysstat

// Windows keeps no load average. gopsutil's stand-in smooths the processor
// queue length, a different figure under the same name.
func sampleLoad(*Snapshot) {}
