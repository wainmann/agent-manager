package spawn

import (
	"errors"
	"slices"
	"strings"
	"testing"

	catalog "github.com/YoanWai/agent-manager/internal/catalog/value"
	tea "github.com/charmbracelet/bubbletea"
)

var fiveLevels = []string{"low", "medium", "high", "xhigh", "max"}

var claudeCaps = Caps{Catalog: true, Models: true, Efforts: true}

var claudeAnswer = catalog.Catalog{Models: []catalog.Model{
	{ID: "default", Label: "Default (recommended)", Efforts: fiveLevels, Default: true},
	{ID: "opus", Label: "Opus 5.5", Efforts: fiveLevels},
	{ID: "haiku", Label: "Haiku 4.5"},
}}

// fakeChoiceHost answers a choice, and the features built on one, without
// a root model: the tool flags, the catalog answer and the settings cache
// are plain maps, and the status bar is a list.
type fakeChoiceHost struct {
	caps     map[string]Caps
	answers  map[string]*Answer
	settings map[string]string
	errs     []string
	cleared  int
	remote   string
}

func newFakeChoiceHost() *fakeChoiceHost {
	return &fakeChoiceHost{
		caps:     map[string]Caps{"claude": claudeCaps},
		answers:  map[string]*Answer{"claude": {Cat: claudeAnswer, Loaded: true}},
		settings: map[string]string{},
	}
}

func (h *fakeChoiceHost) ChoiceCaps(toolName string) Caps { return h.caps[toolName] }
func (h *fakeChoiceHost) ChoiceRemoteHost() string        { return h.remote }

func (h *fakeChoiceHost) ChoiceAnswer(toolName string) *Answer { return h.answers[toolName] }

func (h *fakeChoiceHost) ChoiceSetting(key string) string { return h.settings[key] }

func (h *fakeChoiceHost) SaveChoiceSetting(key, value string) { h.settings[key] = value }

func (h *fakeChoiceHost) ReportErr(text string) { h.errs = append(h.errs, text) }

func (h *fakeChoiceHost) ClearErr() { h.cleared++ }

// A choice restores, keeps and remembers through its host alone.
func TestChoiceThroughAFakeHost(t *testing.T) {
	h := newFakeChoiceHost()
	h.settings[savedChoiceKey("claude")] = `{"Model":"opus","Effort":"high"}`
	ch := NewChoice(h, "claude")
	if got := ch.current(h, "claude"); got != (Pick{Model: "opus", Effort: "high"}) {
		t.Fatalf("restored choice = %+v", got)
	}
	ch.pickModel(h, "claude", "default")
	if got := ch.EffortLevel(h, "claude"); got != "high" {
		t.Fatalf("a model offering the same level kept %q", got)
	}
	if saved := savedChoice(h, "claude"); saved == nil || saved.Model != "default" {
		t.Fatalf("the pick was not kept: %+v", saved)
	}
	ch.pickModel(h, "claude", "haiku")
	if shown, _ := ch.EffortShown(h, "claude"); shown || ch.EffortLevel(h, "claude") != "" {
		t.Fatal("haiku lists no levels, so no effort should show")
	}
	RememberModel(h, "claude", Pick{Model: "opus"})
	if recent := recentModels(h, "claude"); !slices.Equal(recent, []string{"opus"}) {
		t.Fatalf("recent = %v", recent)
	}
	if _, err := ch.Launch(h, "claude", "nope"); err == nil {
		t.Fatal("a model the CLI does not list launched")
	}
	h.settings[recentModelsKey("claude")] = "{"
	if recentModels(h, "claude") != nil || len(h.errs) != 1 {
		t.Fatalf("an unreadable setting should report once: %v", h.errs)
	}
}

// A failed local catalog is a warning for a session here, since the pick
// comes from it, but on a connection the host runs the CLI, so the note
// says its default applies there.
func TestModelNoteNamesTheHostWhenThisMachineListsNoModels(t *testing.T) {
	h := newFakeChoiceHost()
	h.answers["claude"] = &Answer{Err: errors.New("claude is not installed"), Loaded: true}
	note, listed := ModelNote(h, "claude", Styles{})
	if listed || !strings.Contains(note, "couldn't read models from claude: claude is not installed") {
		t.Fatalf("local note = %q, listed %v", note, listed)
	}
	h.remote = "gpu"
	note, listed = ModelNote(h, "claude", Styles{})
	if listed || note != "claude's default on gpu; this machine's claude lists no models to pick from" {
		t.Fatalf("remote note = %q, listed %v", note, listed)
	}
}

// A recent pick kept under a bare id still lists its model once, though
// the CLI now routes it through a provider.
func TestModelListListsARecentPickOnce(t *testing.T) {
	h := newFakeChoiceHost()
	h.caps["hermes"] = Caps{Catalog: true, Models: true}
	h.answers["hermes"] = &Answer{Loaded: true, Cat: catalog.Catalog{Models: []catalog.Model{{ID: "grok-4.6", Provider: "xai-oauth"}, {ID: "claude-opus-5", Provider: "anthropic"}}}}
	h.settings[recentModelsKey("hermes")] = `["grok-4.6"]`
	ch := NewChoice(h, "hermes")
	if keys := ch.Suggestions(h, "hermes"); !slices.Equal(keys, []string{"", "xai-oauth:grok-4.6", "anthropic:claude-opus-5"}) {
		t.Fatalf("list = %v", keys)
	}
}

// The saved choice keeps config.Choice's stored shape, field for field.
func TestPickKeepsTheStoredChoiceShape(t *testing.T) {
	h := newFakeChoiceHost()
	ch := NewChoice(h, "claude")
	ch.pickModel(h, "claude", "opus")
	if got, want := h.settings[savedChoiceKey("claude")], `{"Provider":"","Model":"opus","Effort":"","Profile":""}`; got != want {
		t.Fatalf("kept %s, want %s", got, want)
	}
}

// The list leads with the CLI's default, so a kept model has a way back
// to none from the list alone. It still opens on a model, a filter leaves
// the row out, and an emptied filter lands on it as the form's does.
func TestModelListLeadsWithTheCLIsDefault(t *testing.T) {
	h := newFakeChoiceHost()
	h.settings[savedChoiceKey("claude")] = `{"Model":"opus","Effort":"high"}`
	ch := NewChoice(h, "claude")
	ch.OpenFilter(h, "claude")
	if got := ch.sugg.index; got != 2 {
		t.Fatalf("the list opened on entry %d, want the kept model under the default row", got)
	}
	ch.PickEntry(h, "claude", 0)
	if got := ch.current(h, "claude"); got != (Pick{Effort: "high"}) {
		t.Fatalf("the default row picked %+v", got)
	}
	if got, want := h.settings[savedChoiceKey("claude")], `{"Provider":"","Model":"","Effort":"high","Profile":""}`; got != want {
		t.Fatalf("kept %s, want %s", got, want)
	}
	ch.OpenFilter(h, "claude")
	if ch.sugg.index != 1 {
		t.Fatalf("with no model picked the list opened on entry %d, want the first model", ch.sugg.index)
	}
	ch.TypeFilter(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("op")})
	ch.PickHighlighted(h, "claude")
	if got := ch.Model(); got != "opus" {
		t.Fatalf("enter on a filter picked %q", got)
	}
	ch.OpenFilter(h, "claude")
	ch.TypeFilter(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	ch.TypeFilter(tea.KeyMsg{Type: tea.KeyBackspace})
	ch.PickHighlighted(h, "claude")
	if got := ch.Model(); got != "" {
		t.Fatalf("enter on an emptied filter picked %q, want the default as in the form", got)
	}
}
