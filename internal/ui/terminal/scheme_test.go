package terminal

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type schemeRecorder struct{ got []tea.Msg }

func (r *schemeRecorder) Init() tea.Cmd { return nil }

func (r *schemeRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if scheme, ok := msg.(SchemeMsg); ok {
		r.got = append(r.got, scheme)
		if len(r.got) == 2 {
			return r, tea.Quit
		}
	}
	return r, nil
}

func (r *schemeRecorder) View() string { return "" }

// Reports go through Bubble Tea's real input reader, so a release that
// renames or decodes its unknown-CSI message breaks this test, not users.
func TestReadSchemeTranslatesBubbleTeaInput(t *testing.T) {
	input := strings.NewReader(ansi.LightDarkReport(false) + ansi.LightDarkReport(true))
	recorder := &schemeRecorder{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	program := tea.NewProgram(recorder, tea.WithInput(input), tea.WithOutput(io.Discard),
		tea.WithoutRenderer(), tea.WithContext(ctx),
		tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg { return ReadScheme(msg) }))
	if _, err := program.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := []tea.Msg{SchemeMsg{Light: true}, SchemeMsg{Light: false}}
	if !reflect.DeepEqual(recorder.got, want) {
		t.Fatalf("got %v, want %v", recorder.got, want)
	}
}

func TestReadSchemePassesOtherMessages(t *testing.T) {
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
	if got := ReadScheme(key); !reflect.DeepEqual(got, key) {
		t.Fatalf("ReadScheme changed %v into %v", key, got)
	}
}
