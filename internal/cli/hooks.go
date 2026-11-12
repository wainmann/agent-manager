package cli

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/YoanWai/agent-manager/internal/lifecyclehook"
	"github.com/YoanWai/agent-manager/internal/store"
)

const usageHooks = "hooks [--json]"

func hooksSection() section {
	return section{
		title: "Lifecycle hooks",
		commands: []command{
			{
				name:  "hooks",
				usage: usageHooks,
				about: "show the commands hooks.toml runs after a lifecycle event, and the recent failures",
				run: func(args []string, _ func() string, configDir string) error {
					return runHooks(os.Stdout, args, configDir)
				},
			},
		},
	}
}

type hooksReport struct {
	Path          string              `json:"path"`
	Found         bool                `json:"found"`
	Error         string              `json:"error"`
	SchemaVersion int                 `json:"schema_version"`
	Events        []string            `json:"events"`
	Hooks         []hookView          `json:"hooks"`
	Failures      []store.HookFailure `json:"failures"`
}

type hookView struct {
	Event   string   `json:"event"`
	Command []string `json:"command"`
	Events  []string `json:"events"`
	Notify  bool     `json:"notify"`
	Timeout string   `json:"timeout"`
	Shown   bool     `json:"shown"`
}

func runHooks(out io.Writer, args []string, configDir string) error {
	set := newFlagSet(usageHooks)
	asJSON := jsonFlag(set)
	if _, err := parseCommand(out, set, args, 0, 0); err != nil {
		return err
	}
	loaded, loadErr := lifecyclehook.Load(configDir)
	report := hooksReport{
		Path:          loaded.Path,
		Found:         loaded.Found,
		SchemaVersion: loaded.SchemaVersion,
		Events:        lifecyclehook.EventNames(),
		Hooks:         []hookView{},
	}
	if loadErr != nil {
		report.Error = loadErr.Error()
	}
	st, err := openSettingsStore(configDir)
	if err != nil {
		return err
	}
	defer st.Close()
	shown, err := st.HooksShown()
	if err != nil {
		return err
	}
	for _, hook := range loaded.Hooks {
		events := hook.Events
		if events == nil {
			events = []string{}
		}
		report.Hooks = append(report.Hooks, hookView{
			Event:   hook.Event,
			Command: hook.Command,
			Events:  events,
			Notify:  hook.Notify,
			Timeout: hook.Timeout.String(),
			Shown:   slices.Contains(shown, lifecyclehook.Fingerprint(hook.Event, hook.Command)),
		})
	}
	report.Failures, err = st.HookFailures()
	if err != nil {
		return err
	}
	if report.Failures == nil {
		report.Failures = []store.HookFailure{}
	}
	var human string
	if !*asJSON {
		human = hooksText(report)
	}
	if err := emit(out, *asJSON, report, human); err != nil {
		return err
	}
	return loadErr
}

func hooksText(report hooksReport) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "path\t%s\n", report.Path)
	fmt.Fprintf(w, "found\t%t\n", report.Found)
	if report.Error != "" {
		fmt.Fprintf(w, "error\t%s\n", report.Error)
	}
	fmt.Fprintf(w, "schema_version\t%d\n", report.SchemaVersion)
	fmt.Fprintf(w, "events\t%s\n", strings.Join(report.Events, ", "))
	for _, hook := range report.Hooks {
		fmt.Fprintf(w, "%s\t%s\tevents=%s\tnotify=%t\ttimeout=%s\tshown=%t\n",
			hook.Event, strings.Join(hook.Command, " "), strings.Join(hook.Events, ","), hook.Notify, hook.Timeout, hook.Shown)
	}
	if len(report.Failures) > 0 {
		fmt.Fprintln(w, "failures\t")
		for _, failure := range report.Failures {
			fmt.Fprintf(w, "\t%s\t%s\t%s\t%s\n", failure.At.Local().Format(time.RFC3339), failure.Event, failure.Command, failure.Reason)
		}
	}
	w.Flush()
	return b.String()
}
