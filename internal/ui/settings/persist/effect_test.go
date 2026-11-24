package persist

import (
	"errors"
	"testing"

	uisettings "github.com/YoanWai/agent-manager/internal/ui/settings"
)

// A stored ←→ value rebinds the sessions' keys in the save's own job. A
// rebind that fails is reported apart from the save, which still commits
// the values after it. A save that never stored the value rebinds nothing.
func TestSaveRebindsOnceTheArrowStepIsStored(t *testing.T) {
	arrow := uisettings.Value{Key: uisettings.ArrowStepSetting, Value: "attach"}
	layout := uisettings.Value{Key: uisettings.DiffLayoutSetting, Value: "unified"}
	rebinds := 0
	failing := func() error { rebinds++; return errors.New("scripted tmux failure") }

	writer := &scriptWriter{values: map[string]string{}}
	result, err := SaveRequest{Values: []uisettings.Value{arrow, layout}}.Run(Services{Writer: writer, Rebind: failing})
	saved := result.(SaveResult)
	if err != nil || saved.Committed != 2 || writer.values[uisettings.DiffLayoutSetting] != "unified" {
		t.Fatalf("a failed rebind failed the save: committed=%d err=%v", saved.Committed, err)
	}
	if rebinds != 1 || saved.RebindErr == nil {
		t.Fatalf("rebinds=%d RebindErr=%v, want one reported failure", rebinds, saved.RebindErr)
	}

	writer = &scriptWriter{failOn: uisettings.DiffLayoutSetting, values: map[string]string{}}
	if _, err = (SaveRequest{Values: []uisettings.Value{arrow, layout}}).Run(Services{Writer: writer, Rebind: failing}); err == nil || rebinds != 2 {
		t.Fatalf("a later failed write skipped the rebind: rebinds=%d err=%v", rebinds, err)
	}

	writer = &scriptWriter{failOn: uisettings.ArrowStepSetting, values: map[string]string{}}
	for _, values := range [][]uisettings.Value{{arrow}, {layout}} {
		SaveRequest{Values: values}.Run(Services{Writer: writer, Rebind: failing})
	}
	if rebinds != 2 {
		t.Fatalf("rebinds=%d, want none without a stored ←→ value", rebinds)
	}
}
