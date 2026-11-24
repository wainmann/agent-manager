package launch

import (
	"errors"
	"strings"
	"testing"
)

// A failed write whose readback also fails has nothing to say about what the
// store holds: the result must not pass off an empty read as a stored value,
// and the reported error names both failures.
func TestSaveChoiceFailedReadbackCarriesNoValue(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("disk full")
	st := &choiceStore{writeErr: writeErr, readErr: errors.New("database is locked")}
	result, err := SaveChoice(st, "default_tool", "claude")
	if !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "database is locked") {
		t.Fatalf("error = %v, want the write and the readback failure", err)
	}
	if result.Valid || result.Stored != "" {
		t.Fatalf("result = %+v, want an invalid readback", result)
	}
}

// A failed write whose readback succeeds hands the stored value back for the
// cache to reconcile.
func TestSaveChoiceFailedWriteCarriesStoredValue(t *testing.T) {
	t.Parallel()
	st := &choiceStore{writeErr: errors.New("disk full"), stored: "opencode"}
	result, err := SaveChoice(st, "default_tool", "claude")
	if err == nil {
		t.Fatal("a failed write must report")
	}
	if !result.Valid || result.Stored != "opencode" {
		t.Fatalf("result = %+v, want the stored opencode", result)
	}
}

// A value the store already holds costs no write.
func TestSaveChoiceSkipsUnchangedValue(t *testing.T) {
	t.Parallel()
	st := &choiceStore{stored: "claude"}
	if _, err := SaveChoice(st, "default_tool", "claude"); err != nil {
		t.Fatalf("SaveChoice: %v", err)
	}
	if st.writes != 0 {
		t.Fatalf("writes = %d, want the unchanged value skipped", st.writes)
	}
}

type choiceStore struct {
	writeErr error
	readErr  error
	stored   string
	writes   int
}

func (s *choiceStore) SetSettingIfChanged(key, value string) (bool, error) {
	if s.writeErr != nil {
		return false, s.writeErr
	}
	if s.stored == value {
		return false, nil
	}
	s.stored = value
	s.writes++
	return true, nil
}

func (s *choiceStore) Setting(string) (string, error) { return s.stored, s.readErr }
