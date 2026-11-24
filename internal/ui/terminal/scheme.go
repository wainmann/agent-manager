package terminal

import (
	"os"
	"reflect"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// schemeReports is false on Windows, where Bubble Tea v1 reads the console
// one key event at a time: a report would arrive as an escape, a bracket
// and digits typed into the manager, never as one sequence.
const schemeReports = runtime.GOOS != "windows"

// SchemeMsg is a mode 2031 report: the terminal's light or dark scheme,
// sent on every flip while reporting is on and in answer to a request.
type SchemeMsg struct{ Light bool }

// EnableSchemeReports asks the terminal to report its scheme as it
// changes. Terminals and tmux releases without mode 2031 ignore it and
// keep the scheme detected at startup.
func EnableSchemeReports() error { return writeScheme(ansi.SetModeLightDark) }

// DisableSchemeReports stops the reports, so a flip never reaches a
// program that does not ask for them.
func DisableSchemeReports() error { return writeScheme(ansi.ResetModeLightDark) }

// RequestSchemeReport asks for the scheme now. Turning mode 2031 back on
// does not make the terminal report, so a flip made while it was off is
// only learned this way.
func RequestSchemeReport() error { return writeScheme(ansi.RequestLightDarkReport) }

func writeScheme(seq string) error {
	if !schemeReports {
		return nil
	}
	_, err := os.Stdout.WriteString(seq)
	return err
}

// ReadScheme turns a mode 2031 report into SchemeMsg. Bubble Tea v1 has no
// message for one and delivers it as its unexported unknown-CSI message, a
// byte slice holding the raw sequence.
func ReadScheme(msg tea.Msg) tea.Msg {
	raw := reflect.ValueOf(msg)
	if raw.Kind() != reflect.Slice || raw.Type().Elem().Kind() != reflect.Uint8 {
		return msg
	}
	switch string(raw.Bytes()) {
	case ansi.LightDarkReport(true):
		return SchemeMsg{Light: false}
	case ansi.LightDarkReport(false):
		return SchemeMsg{Light: true}
	}
	return msg
}
