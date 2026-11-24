package persist

import (
	"slices"

	uieffects "github.com/YoanWai/agent-manager/internal/ui/effects"
	uisettings "github.com/YoanWai/agent-manager/internal/ui/settings"
)

// Services is what the Settings effect lane captures for one job: the writer
// a save writes and reads back through, the key saver a key save writes
// through, the preset catalog a prompt change reads back, and the rebind
// of the in-session tmux keys a stored ←→ value asks for.
type Services struct {
	Writer  Writer
	Keys    KeySaver
	Presets Presets
	Rebind  func() error
}

func (r SaveRequest) EffectName() string { return "settings save" }

func (r SaveRequest) EffectCopy() uieffects.Request { return r.Clone() }

// Run writes the captured values in the order the dialog listed them. A
// committed ←→ value rebinds the sessions' keys in the same job. A rebind
// that fails is reported on its own, since the value is stored either way.
func (r SaveRequest) Run(s Services) (uieffects.Result, error) {
	result, err := RunSave(r.Clone(), s.Writer)
	committed := r.Values[:min(result.Committed, len(r.Values))]
	if slices.ContainsFunc(committed, func(value uisettings.Value) bool { return value.Key == uisettings.ArrowStepSetting }) {
		result.RebindErr = s.Rebind()
	}
	return result, err
}

func (r KeysRequest) EffectName() string { return "key binding save" }

// Run writes the changed key tables in list-then-session order.
func (r KeysRequest) Run(s Services) (uieffects.Result, error) { return RunKeys(r, s.Keys) }

func (r PromptRequest) EffectName() string { return "saved prompt change" }

// Run saves or deletes one name, then reads the catalog back.
func (r PromptRequest) Run(s Services) (uieffects.Result, error) { return RunPrompt(r, s.Presets) }

// KeysToSubmit is the key save the picker's tables ask for, if they ask for
// one at all.
func KeysToSubmit(change uisettings.KeysChange, changed bool) (KeysRequest, bool) {
	if !changed {
		return KeysRequest{}, false
	}
	return KeysRequest{
		List:           change.List,
		Session:        change.Session,
		ListChanged:    change.ListChanged,
		SessionChanged: change.SessionChanged,
	}, true
}
