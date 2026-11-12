package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/YoanWai/agent-manager/internal/update"
)

const usageUpdate = "update [--json]"

// The seams let tests steer the update path without hitting GitHub or
// swapping the running binary.
var (
	updateExecutable = os.Executable
	updateDetect     = update.DetectManager
	updateRefresh    = update.Refresh
	updateApply      = update.Apply
	updateUpgraded   = update.Manager.Upgraded
	updateForInstall = update.ForInstall
)

func updateSection(version string) section {
	return section{
		title: "Updates",
		commands: []command{
			{
				name:  "update",
				usage: usageUpdate,
				about: "bring agent-manager to its newest release, through its package manager or in place",
				run: func(args []string, _ func() string, configDir string) error {
					return runUpdate(version)(os.Stdout, args, configDir)
				},
			},
		},
	}
}

// updateReport is the --json record; exactly one field names what happened.
type updateReport struct {
	Version  string `json:"version,omitempty"`
	Delegate string `json:"delegate,omitempty"`
	UpToDate bool   `json:"up_to_date,omitempty"`
}

func runUpdate(version string) func(out io.Writer, args []string, configDir string) error {
	return func(out io.Writer, args []string, configDir string) error {
		set := newFlagSet(usageUpdate)
		asJSON := jsonFlag(set)
		if _, err := parseCommand(out, set, args, 0, 0); err != nil {
			return err
		}

		execPath, err := updateExecutable()
		if err != nil {
			return err
		}

		manager := updateDetect(execPath)
		if manager.Advice != "" {
			return errors.New(manager.Advice)
		}
		if manager.Delegated() {
			execPath = update.RestartTarget(execPath)
			command := manager.UpgradeCommand()
			command.Stdin = os.Stdin
			command.Stderr = os.Stderr
			if *asJSON {
				// A machine parses stdout as one record, so the manager's
				// own progress stays out of it.
				command.Stdout = os.Stderr
			} else {
				command.Stdout = out
			}
			if err := command.Run(); err != nil {
				return fmt.Errorf("%s: %w", manager.String(), err)
			}
			_, upgraded, err := updateUpgraded(manager, execPath, version, "")
			if err != nil {
				return fmt.Errorf("%s: checking the installed version: %w", manager.String(), err)
			}
			if !upgraded {
				return reportUnchanged(out, *asJSON, manager, version, configDir)
			}
			return emit(out, *asJSON, updateReport{Delegate: manager.String()},
				fmt.Sprintf("updated with %s", manager.String()))
		}

		if !update.IsRelease(version) {
			// A dev build carries no release tag, so there is nothing to
			// compare against or download.
			return errors.New("update: this build is not a release; reinstall to upgrade")
		}
		result, err := updateRefresh(context.Background(), configDir, version)
		if err != nil {
			return fmt.Errorf("update: could not reach GitHub: %w", err)
		}
		if result.Latest == "" {
			return emit(out, *asJSON, updateReport{UpToDate: true}, "already up to date")
		}
		// The download can take a while, so say what is coming; --json keeps
		// stdout to the single record.
		if !*asJSON {
			if _, err := fmt.Fprintf(out, "updating to %s...\n", result.Latest); err != nil {
				return err
			}
		}
		if err := updateApply(context.Background(), result.Latest, execPath); err != nil {
			return fmt.Errorf("update: %w", err)
		}
		return emit(out, *asJSON, updateReport{Version: result.Latest},
			fmt.Sprintf("updated to %s", result.Latest))
	}
}

// reportUnchanged tells a current install from one its package manager failed
// to move, since both end with the same version and exit status.
func reportUnchanged(out io.Writer, asJSON bool, manager update.Manager, version, configDir string) error {
	ctx := context.Background()
	result, err := updateRefresh(ctx, configDir, version)
	if err == nil {
		result, err = updateForInstall(ctx, configDir, version, manager, true, result)
	}
	if err != nil {
		return fmt.Errorf("%s finished, still on %s: %w", manager.String(), version, err)
	}
	if result.Latest != "" {
		return fmt.Errorf("%s finished, still on %s, and %s is available", manager.String(), version, result.Latest)
	}
	return emit(out, asJSON, updateReport{UpToDate: true}, "already up to date")
}
