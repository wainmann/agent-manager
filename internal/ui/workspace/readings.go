package workspace

import (
	"fmt"
	"strings"

	uiframe "github.com/YoanWai/agent-manager/internal/ui/frame"
	"github.com/YoanWai/agent-manager/internal/ui/presentation"
	uitheme "github.com/YoanWai/agent-manager/internal/ui/theme"
	"github.com/charmbracelet/x/ansi"
)

// railInset is the blank run the foot keeps left of its readings, matching
// the Rail's gutter.
const railInset = uiframe.RailGutter - 1

// ComputerLines is the machine block docked at the rail's foot: a label and
// one thin meter per resource.
func ComputerLines(r Readings, net Net, width int, mode presentation.GlyphMode) []string {
	pad := strings.Repeat(" ", railInset)
	barWidth := MeterWidth(width)

	render := func(label string, percent float64, extra, bar string) string {
		line := pad + uitheme.LabelStyle().Width(5).Render(label) + bar +
			uitheme.ValueStyle().Render(fmt.Sprintf(" %3.0f%%", percent))
		if extra != "" {
			line += uitheme.SubtleStyle().Render(" " + extra)
		}
		return line
	}
	meter := func(label string, percent float64, ok bool, extra string) string {
		if !ok {
			return pad + uitheme.LabelStyle().Width(5).Render(label) + uitheme.SubtleStyle().Render("n/a")
		}
		return render(label, percent, extra, uitheme.Gauge(percent, barWidth, false, mode))
	}

	// wrapped moves extra under its meter when the two overflow the foot.
	wrapped := func(label string, percent float64, extra string) []string {
		line := meter(label, percent, true, extra)
		if ansi.StringWidth(line) <= width {
			return []string{line}
		}
		return []string{meter(label, percent, true, ""),
			pad + strings.Repeat(" ", 5) + uitheme.SubtleStyle().Render(extra)}
	}

	lines := []string{pad + uitheme.SubtleStyle().Render("computer")}
	lines = append(lines,
		meter("cpu", r.CPUPercent, r.CPUOK, ""),
	)
	if r.LoadOK {
		perCPU := r.Load1 / float64(r.LoadCPUs) * 100
		lines = append(lines, wrapped("load", perCPU, loadAverages(r))...)
	}
	lines = append(lines,
		meter("mem", r.MemPercent, r.MemOK, uiframe.HumanBytes(r.MemUsed)+"/"+uiframe.HumanBytes(r.MemTotal)),
	)
	if r.SwapOK && r.SwapTotal > 0 {
		// Percent is used/total of the current swap allocation (macOS
		// grows the file under pressure; Linux uses the fixed swap size).
		lines = append(lines, meter("swap", r.SwapPercent, true,
			uiframe.HumanBytes(r.SwapUsed)+"/"+uiframe.HumanBytes(r.SwapTotal)))
	}
	if r.DiskOK {
		extra := uiframe.DiskBytes(r.DiskAvailable) + " available"
		lines = append(lines, wrapped("disk", r.DiskPercent, extra)...)
	} else {
		lines = append(lines, meter("disk", 0, false, ""))
	}
	if r.BatteryOK {
		extra := ""
		if r.BatteryCharging {
			extra = "charging"
		}
		lines = append(lines, render("batt", r.BatteryPercent, extra,
			uitheme.Gauge(r.BatteryPercent, barWidth, true, mode)))
	}
	if temps := tempReadings(r); temps != "" {
		lines = append(lines, pad+uitheme.LabelStyle().Width(5).Render("temp")+temps)
	}
	if net.Rates {
		nav := presentation.NavigationFor(mode)
		lines = append(lines, pad+uitheme.LabelStyle().Width(5).Render("net")+
			uitheme.ValueStyle().Render(nav.DownGlyph+" "+uiframe.HumanBytes(net.Down)+"/s")+
			uitheme.SubtleStyle().Render("  "+nav.UpGlyph+" "+uiframe.HumanBytes(net.Up)+"/s"))
	}
	return append(lines, "")
}

func loadAverages(r Readings) string {
	return fmt.Sprintf("%.2f/%.2f/%.2f", r.Load1, r.Load5, r.Load15)
}

func tempReadings(r Readings) string {
	styled := make([]string, 0, 3)
	for _, reading := range Temperatures(r) {
		styled = append(styled, uitheme.ValueStyle().Render(reading))
	}
	return strings.Join(styled, uitheme.SubtleStyle().Render("  "))
}

// FootLine is the machine readings condensed to one line, for the full
// screen layout whose rail has a single row to spare.
func FootLine(r Readings, net Net, width int, mode presentation.GlyphMode) []string {
	reading := func(label, value string, ok bool) string {
		if !ok {
			return uitheme.LabelStyle().Render(label+" ") + uitheme.SubtleStyle().Render("n/a")
		}
		return uitheme.LabelStyle().Render(label+" ") + uitheme.ValueStyle().Render(value)
	}
	parts := []string{
		reading("cpu", fmt.Sprintf("%.0f%%", r.CPUPercent), r.CPUOK),
		reading("mem", fmt.Sprintf("%.0f%% %s/%s", r.MemPercent, uiframe.HumanBytes(r.MemUsed), uiframe.HumanBytes(r.MemTotal)), r.MemOK),
	}
	if r.SwapOK && r.SwapTotal > 0 {
		parts = append(parts, reading("swap", fmt.Sprintf("%.0f%% %s/%s", r.SwapPercent, uiframe.HumanBytes(r.SwapUsed), uiframe.HumanBytes(r.SwapTotal)), true))
	}
	parts = append(parts, reading("disk", fmt.Sprintf("%.0f%% %s available", r.DiskPercent, uiframe.DiskBytes(r.DiskAvailable)), r.DiskOK))
	if r.BatteryOK {
		value := fmt.Sprintf("%.0f%%", r.BatteryPercent)
		if r.BatteryCharging {
			value += " charging"
		}
		parts = append(parts, reading("batt", value, true))
	}
	if r.LoadOK {
		parts = append(parts, reading("load", loadAverages(r), true))
	}
	if temps := tempReadings(r); temps != "" {
		parts = append(parts, uitheme.LabelStyle().Render("temp ")+temps)
	}
	if net.Rates {
		nav := presentation.NavigationFor(mode)
		parts = append(parts, reading("net", nav.DownGlyph+" "+uiframe.HumanBytes(net.Down)+"/s "+nav.UpGlyph+" "+uiframe.HumanBytes(net.Up)+"/s", true))
	}
	line := strings.Repeat(" ", railInset) + strings.Join(parts, "  ")
	return []string{ansi.Truncate(line, max(width-railInset, 0), "…")}
}
