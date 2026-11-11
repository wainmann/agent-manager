package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

const (
	NotifyCommandKey           = "notify_command"
	NotifyCommandShownKey      = "notify_command_shown"
	NotifyCommandMigratedKey   = "notify_command_migrated"
	NotifyCommandNotCarriedKey = "notify_command_not_carried"
	HookFailuresKey            = "hook_failures"
	HooksConfigErrorKey        = "hooks_config_error"
	HooksShownKey              = "hooks_shown"
)

const hookFailureCap = 20

// HookFailure is one hook that did not run as asked. Command is argv[0]
// only, and Reason is the exit or drop line, never the hook's output.
type HookFailure struct {
	At      time.Time `json:"at"`
	Event   string    `json:"event"`
	Command string    `json:"command"`
	Reason  string    `json:"reason"`
}

// SwapSetting writes next only while the stored value is old. A missing
// row matches old "". The loser of two writers gets false and changes
// nothing.
func (s *Store) SwapSetting(key, old, next string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var current string
	err = tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&current)
	switch {
	case err == sql.ErrNoRows:
		if old != "" {
			return false, nil
		}
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, key, next); err != nil {
			return false, err
		}
	case err != nil:
		return false, err
	case current != old:
		return false, nil
	default:
		if _, err := tx.Exec(`UPDATE settings SET value = ? WHERE key = ?`, next, key); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) HookFailures() ([]HookFailure, error) {
	raw, err := s.Setting(HookFailuresKey)
	if err != nil {
		return nil, err
	}
	return decodeFailures(raw)
}

func (s *Store) AddHookFailure(failure HookFailure) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	failures, err := readFailures(tx)
	if err != nil {
		return err
	}
	failures = append(failures, failure)
	if len(failures) > hookFailureCap {
		failures = failures[len(failures)-hookFailureCap:]
	}
	if err := writeFailures(tx, failures); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) HooksShown() ([]string, error) {
	raw, err := s.Setting(HooksShownKey)
	if err != nil {
		return nil, err
	}
	return decodeStrings(raw)
}

// ClaimHookShown records fingerprint and reports whether it was new.
func (s *Store) ClaimHookShown(fingerprint string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	shown, err := readStrings(tx, HooksShownKey)
	if err != nil {
		return false, err
	}
	if slices.Contains(shown, fingerprint) {
		return false, nil
	}
	shown = append(shown, fingerprint)
	if err := writeStrings(tx, HooksShownKey, shown); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) HooksConfigError() (string, error) {
	return s.Setting(HooksConfigErrorKey)
}

func (s *Store) SetHooksConfigError(msg string) error {
	_, err := s.SetSettingIfChanged(HooksConfigErrorKey, msg)
	return err
}

func readFailures(tx *sql.Tx) ([]HookFailure, error) {
	raw, err := readSetting(tx, HookFailuresKey)
	if err != nil {
		return nil, err
	}
	return decodeFailures(raw)
}

func decodeFailures(raw string) ([]HookFailure, error) {
	if raw == "" {
		return nil, nil
	}
	var failures []HookFailure
	if err := json.Unmarshal([]byte(raw), &failures); err != nil {
		return nil, fmt.Errorf("hook_failures: %w", err)
	}
	return failures, nil
}

func readStrings(tx *sql.Tx, key string) ([]string, error) {
	raw, err := readSetting(tx, key)
	if err != nil {
		return nil, err
	}
	return decodeStrings(raw)
}

func decodeStrings(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func readSetting(tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func writeFailures(tx *sql.Tx, failures []HookFailure) error {
	encoded, err := json.Marshal(failures)
	if err != nil {
		return err
	}
	return writeSettingValue(tx, HookFailuresKey, string(encoded))
}

func writeStrings(tx *sql.Tx, key string, values []string) error {
	encoded, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return writeSettingValue(tx, key, string(encoded))
}

func writeSettingValue(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}
