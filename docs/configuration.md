# Configuration

Everything you can change lives in Settings (`s` by default): the keys, the editor `o` opens, the theme, and the rest of its rows (see [Keys](usage.md#keys)). The manager stores each choice in `state.db` (SQLite) in your OS user config dir, `~/Library/Application Support/agent-manager/` on macOS and `~/.config/agent-manager/` on Linux, with `XDG_CONFIG_HOME` honored when set. Changes are saved as you leave Settings and take effect then, with no restart. Only the rows you changed are written, so a change another manager or the CLI made in the meantime is kept. `agent-manager settings` lists, reads and writes the same keys from a shell (see [Settings from a shell](usage.md#settings-from-a-shell)).

The **editor** row picks the command `o` opens a directory in (see [Opening the editor](usage.md#opening-the-editor)), and the **keybindings** row opens the [key picker](#key-bindings). Panes are polled every two seconds for status, preview, and stats.

## Lifecycle hooks

`hooks.toml` in the config directory runs commands after a lifecycle event is stored. There is no Settings row for it. `agent-manager hooks` prints the file, and `agent-manager hooks --json` prints the same facts as JSON. A missing file is normal and exits 0. A file the manager refuses exits 1.

```toml
schema_version = 1

[[hooks.notification]]
command = ["sh", "-c", 'curl -fsS -H "Title: $AM_TITLE" -d "$AM_BODY" https://ntfy.sh/topic']
timeout = "15s"

[[hooks.all]]
command = ["/usr/local/bin/agent-events"]
```

`schema_version` is required and must be `1`. An empty file, a missing version, or a version this manager does not read refuses the whole file. The manager posts that reason in Messages and writes it to `hooks_config_error`. A good load clears it.

Each `[[hooks.<event>]]` block names one command. The events are `session_start`, `session_state`, `notification`, `session_archive`, `session_restore`, `session_kill`, `task_claim`, `task_finish`, `review_round` and `reservation_set`. `command` is an argv, with no shell unless you put one there. `command[0]` is looked up on `PATH`. On Windows a PowerShell script runs through PowerShell, for example `command = ["pwsh", "-NoProfile", "-File", 'C:\Users\me\hook.ps1']`. `timeout` defaults to 5s and must sit between 1s and 30s. `events` filters `session_state` by the new status, and is refused on any other event. Empty or omitted means every status. A `session_state` hook runs on every stored change that its `events` list allows, whatever the banner settings say. A turn that starts and ends between two polls is still reported. The poll that finds its output stores `working`, and the next poll stores the status the turn ended on, so the hook sees both changes about one poll late. The manager tells such a turn by the newest prompt or reply on screen changing while the session rests at `finished` or `idle`, at the same pane width. A turn that ends on a question still reports `waiting` without the `working` before it. A turn whose prompt and reply match the turn before it word for word is not told apart, and neither is one on a CLI whose rules set no `user_echo`.

`notification` fires on every change that triggers the native banner, right after that change's `session_state`. That is `waiting`, `errored`, and `finished` while `notify on finish` is on. The `notifications` row in Settings only shows or hides the banner, so a `notification` hook still runs with banners off. Its JSON line carries the banner under `notification`, and its environment adds `AM_NOTIFY_KIND`, `AM_TITLE` and `AM_BODY`.

`[[hooks.all]]` runs for every event above, so one script can handle all of them. It gets the same JSON line and `AM_*` variables, with `AM_EVENT` set to the event that fired, so a banner trigger reaches it twice, once as `session_state` and once as `notification`. It takes `command` and `timeout`. `events` belongs to `session_state`, and an `all` block that sets it refuses the file. The script reads `AM_EVENT` and the status on stdin to pick what it handles. For each event the manager runs that event's own hooks first and the `all` hooks after them, each in file order. A command listed in both places runs twice. Later releases add events, and an `all` hook receives those too, so a script must ignore an `AM_EVENT` it does not know.

The manager writes one compact JSON line to the command's stdin.

| Field | Meaning |
| --- | --- |
| `v` | `1` |
| `event` | the event name |
| `ts` | RFC3339 UTC |
| `session` | id, name, tool, group, directory, worktree, branch, status, previous_status, conversation, spawned_by |
| `manager` | pid and version |
| `reason` | why this event happened, when it has one |
| `model`, `effort` | the launch choice, on `session_start` |
| `notification` | kind, title and body of the banner, on `notification` |
| `task` | id, title and owner, on a task event. The body is not included |
| `reservation` | action (`reserve` or `release`), pattern, mode and expires_at |
| `review` | comment_count, round and repo, on a confirmed review send |

`directory` is the repository checkout when the session has a worktree, and the session directory otherwise. `worktree` is the worktree checkout when one exists, and empty otherwise. `AM_CWD` is always the session directory, which is the worktree checkout when the manager made one. A hook that `cd`s to `directory` lands in the main checkout of a worktree session. A hook that `cd`s to `AM_CWD` lands in the checkout the agent is using.

The environment always carries `AM_EVENT`, `AM_SESSION_ID`, `AM_MANAGER_PID`, `AM_GROUP`, `AM_SPAWNED_BY`, `AM_SESSION_NAME`, `AM_TOOL`, `AM_CWD` and `AM_BRANCH`. A `notification` event adds `AM_NOTIFY_KIND`, `AM_TITLE` and `AM_BODY`, for its own hooks and the `all` hooks.

`session_start` reasons are `spawn` (the picker or `Sessions.Create`), `fork`, `revive`, `restart` and `restore`. `spawned_by` is set only when another session created this one through the CLI or MCP. A picker launch and a fork leave it empty. `session_kill` reasons are `kill`, `archive`, `after_turn`, `restart`, `delete` and `rollback`. A kill also emits `session_state` with status `dead`. A hook that stops a container has to treat that pair as one stop, because the poller can write `dead` as well. `restart` emits `session_kill` only when a pane was there to kill, and its `session_start` carries the new conversation id. `delete` emits `session_kill` after the row is gone, and does not emit `session_state`. `rollback` is a launch that was announced and then stopped. A launch that fails before it is announced emits nothing. Archive and restore reasons are `user` from the manager and `agent` from the CLI or MCP. `after_turn` is the reason when the session asked to be archived or killed at the end of its turn.

A terminal, and any other tool with `shell` set, emits nothing. Closing a terminal's child stays silent. Acknowledging a finished session emits nothing. Releasing a task without finishing it, and a refused claim, emit nothing. A reservation renew emits `reservation_set` again. Releasing every reservation is one event with an empty pattern. A review round emits only when the send is confirmed.

Four hooks run at once, across sessions. Within one manager process, hooks for one session stay in order, so its kill and the start that follows cannot overlap. A CLI command runs its own hooks, so they can overlap the manager's for the same session. A fifth session waits. The queue holds 64 waiting hooks and drops the rest. A dropped hook is recorded once. A short command waits up to 12 seconds for hooks at exit, and `spawn` waits up to 45 seconds. A remote kill, archive, restore, revive, move, delete or group delete allows for those 12 seconds on top of its own 15 second limit, and a remote spawn stays under its 60 second limit. The manager, `serve` and the MCP server wait up to 30 seconds at shutdown and then cancel what is running and drop what has not started. When that wait passes one second, the manager and `serve` print `Waiting up to 30s for running hooks to finish.` so a quit that is still running a hook says why the shell prompt is late. The MCP handlers themselves never wait. A remote session reads the hooks file on that host. A timeout or shutdown stops the hook's whole process tree, and a process the hook leaves running after it exits keeps running. If that process still holds the hook's output, the hook is recorded as failed a second later, so redirect it, as in `sleep 60 >/dev/null 2>&1 &`.

Failures go to `hooks.log` in the config directory, mode 0600, restarted past 256 KiB, with at most 2 KiB of output. Each line and each `hooks --json` failure names the session id. One command failing again for the same event and session within a minute is recorded once. A different command, event or session is recorded too. Messages shows the event, the first argument and the reason, up to eight recent lines. A new failure gets a new notice. The first time a hook is about to run, the manager posts a `New hook command` banner. A command you have already been shown stays quiet, including after a restart.

The file is trusted the way a shell rc file is. It runs as you. Do not commit a file that contains a token.

A `notify_command` from an older release is copied into `hooks.toml` the next time the manager or `serve` starts, as one `[[hooks.notification]]` hook with a 15 second timeout. On Unix the argv is `sh -c` and the old command. On Windows it is the PowerShell vector that release used. The carried-over command runs on every banner trigger, including with banners off in Settings. This is a change from `notify_command`, which ran only when the banner showed. To keep it quiet while banners are off, remove the hook. The JSON line on stdin is new. The old command saw only environment variables. If the write fails, Messages shows the reason and the manager tries again on its next start. If `hooks.toml` already exists, it is left alone and Messages shows the block to paste. Deleting a file that was written does not put `notify_command` back. Importing an old export whose `notify_command` differs from the one carried over raises the same notice, because the file is already there. `settings set notify_command ''` clears the leftover. Any other value is refused.

Archiving a group writes every session under that group, including one created in the moment between the list and the write. That new session is archived and gets no event. Subscribers that care about the group should read the session list again.

## Export and import

Move the manager's configuration between machines with JSON files.

```bash
agent-manager export manager-config.json
agent-manager import manager-config.json
```

Both commands include all four sections by default. Combine `--settings`,
`--presets`, `--groups` and `--connections` to select only those sections.
The same flags restrict what an import reads from a complete export.

```bash
agent-manager export --settings --presets preferences.json
agent-manager import --settings preferences.json
agent-manager export --groups > groups.json
agent-manager import --groups < groups.json
```

Omit the filename or use `-` for stdout on export and stdin on import.
Export writes its scope and secret warning to stderr so stdout stays valid JSON.
A named output file must not already exist. It is created with owner-only
permissions on systems supporting Unix file modes. Shell redirection uses the
shell's permissions instead.

Settings include stored preferences, shell-configured values, the editor and
both keybinding tables. Presets include their names and instructions. Groups
include their names, default paths, worktree choices, base refs and environment
profile names. Group and SSH connection order follows the arrays in the file.
Paths and commands are copied literally, including when moving between operating
systems. Review them on the destination machine before use.

Exported values are not redacted. They may contain tokens, private URLs or other
secrets. Keep exports private and import only files you trust. Editor and
notification commands remain executable configuration when later used by the
manager.

Import merges by setting key, preset name, group name and connection name.
Matching entries take the imported values. Other entries remain. A keybinding
table is one setting, so importing it replaces that scope's saved overrides.
Missing settings retain their destination values. Empty sections delete nothing.
Imported groups and connections receive positions starting at zero in array
order. Existing entries with the same position are ordered by name. Existing
groups keep their archived state, and newly imported groups start unarchived.

The file has a `version` field, currently `1`. Unknown fields, settings,
unsupported versions and invalid values are refused. Selected sections are
validated before any configuration is written, and the import commits in one
database transaction. Restart running managers after importing to load the
configuration.

This transfers configuration only. It does not restore sessions, tasks, inbox
messages, review state, worktrees on disk or agents' chat history. Runtime
bookkeeping, environment profiles, SSH keys and SSH client configuration are
also excluded. Recreate any environment profiles referenced by imported groups
on the destination machine.

## Upgrading from config.toml

Earlier releases read a `config.toml` in that directory. The first start after upgrading carries its `editor` line and its `[keybindings.session]` and `[keybindings.list]` tables into Settings, so every key you moved stays where you put it. That happens once: from then on Settings is the only place the manager reads, and the file is yours to delete. A file the manager cannot read is named in Messages (`M`) with the reason, and the keys start from the defaults.

The poll interval is fixed at two seconds, so a `poll_interval` line in that file has no effect. `[tools.<name>]` blocks have none either: every CLI's commands and status rules are built into the binary, which is how a fix for a CLI's new screen reaches you on upgrade.

## Saved prompts

One list in Settings, shared by the new-session form, the quick prompt, a focused session, `spawn --saved`, `send --saved`, and `saved` on `create_session` and `send_session`. Names are unique, up to 60 characters, with no brackets. The text is up to 64 KiB. `agent-manager prompts list`, `add` and `rm` edit the list from a shell. See [Saved prompts](usage.md#saved-prompts).

## Environment profiles

Named sets of environment variables a session launches under, kept in `state.db` and edited from a shell with `agent-manager env list`, `show`, `set`, `unset` and `rm`. Settings lists their names and variables, never their values. Names are up to 60 characters. See [Environment profiles](usage.md#environment-profiles).

## Agent CLIs

Agent Manager supports Claude Code, OpenCode, Codex, Grok Build, Gemini CLI, Antigravity CLI, Pi, Command Code, Hermes Agent, Muse Code, Oh My Pi, Cursor CLI, Kiro CLI, and GitHub Copilot CLI. OpenCode means OpenCode 2 (`@opencode/cli`), which the manager runs with `--standalone` so each session has a server of its own. Each one's launch command, revive and fork commands, MCP registration, status rules, and the flags a session's model, effort and profile launch with are built into the binary, so an upgrade brings the current version of all of them. The models, efforts and profiles themselves are read from each CLI when you create a session (see [Model, effort and profile](usage.md#model-effort-and-profile)). Hermes, Antigravity, Oh My Pi, Cursor CLI, Kiro CLI and GitHub Copilot CLI have no fork, and Pi and Oh My Pi get no MCP registration. Settings (`s`) has a `CLIs` row that picks which of them the session pickers offer (see [Which CLIs you get offered](usage.md#which-clis-you-get-offered)).

Agent Manager supports the current release of each CLI. A CLI can also carry the oldest release its launch, revive, fork and model list still work with. When yours is older, a spawn, a revive, or its model list opens a dialog that names your version, the one it needs, and the vendor installer that updates it. A CLI that lives under `/nix/store` gets no installer there, since the vendor script would not replace it; the dialog says to update it the way it was installed. Pi needs 0.84.3 or later, because earlier releases save every model they are asked about as your default.

Hermes is tested with Hermes Agent 0.20.0 and launches its classic REPL with `--cli`. This keeps the input, approval, and activity markers stable even when your Hermes preference selects its modern TUI.

Grok Build launches with `--no-leader`, so every session runs its own agent. A shared Grok leader runs each session's shell commands under the environment of the session that started it, which would make one session's agent-manager commands act as another. `[cli] use_leader` in Grok's `config.toml` keeps applying to the Grok sessions you start yourself.

Antigravity CLI is tested with agy 1.4.7 and needs 1.3.4 or later, the first release that takes a model without an effort. Its first launch registers the `agent-manager` server with `agy mcp add`, which writes `~/.gemini/config/mcp_config.json`, the file the Antigravity IDE reads its servers from too. agy has no command-line fork, and its `/fork` moves the running session onto the copy, so `f` does not offer one.

Muse Code is tested with Muse 1.3.0 and 1.4.0. Muse reads MCP servers only from its settings file, so the first Muse launch adds the `agent-manager` server to `~/.config/muse/settings.json` (under `$XDG_CONFIG_HOME` when set) and keeps every other setting and server. A symlinked settings file is written through the link. Muse starts that server with none of the session's environment, so the server finds its session through its process tree, which needs the manager's tmux server at the default `TMUX_TMPDIR`. Muse forks only from inside a running session: `f` types `/fork` into the source and opens the fork in its own pane.

Oh My Pi (`omp`) is tested with omp 18.2.11 and 18.4.2 in its default band composer and glyphs; another composer shape or glyph mode (Settings in omp) draws a screen the status rules do not read. omp mints its own session id and writes the session file once the first reply lands. The manager reads the id from the breadcrumb omp keeps for the session's own terminal, `~/.omp/agent/terminal-sessions/<tty>` (under `$PI_CODING_AGENT_DIR` when set), so sessions sharing a directory never swap conversations, and revive runs `omp --resume {id}`; without an id, `omp --resume` opens its picker. A session kept under `--profile`, `--session-dir`, or omp's XDG layout is not read back. omp sessions coordinate through the Agent Manager shell subcommands and get no MCP registration.

Cursor CLI is tested with cursor-agent 2026.10.19-6bbf2c4. It launches as `cursor-agent` and takes the prompt as a positional argument. Cursor mints the chat id. The manager reads it from `chats/<md5 of the workspace path>/<chat id>/meta.json` under `$CURSOR_CONFIG_DIR`, then `$XDG_CONFIG_HOME/cursor`, then `~/.cursor`. Revive with a captured id runs `cursor-agent --resume {id}`. With no id, `cursor-agent ls` opens the picker. The model list comes from `cursor-agent acp`. The id already carries effort, so the form offers no separate effort control. The first launch writes the agent-manager server into `~/.cursor/mcp.json` and runs `cursor-agent mcp enable agent-manager`. Cursor starts that server with none of the session's environment, so the server finds its session through the tmux pane or its process tree. On macOS, Linux and WSL the setup hint is `curl https://cursor.com/install -fsS | bash`, and on Windows it is `irm 'https://cursor.com/install?win32=true' | iex`. The chat store lookup was exercised on macOS; the bucket a Windows Cursor writes was not checked, so a Windows chat may not be found until it is.

Kiro CLI is tested with kiro-cli 3.1.0 and needs 3.1.0 or later, the V3 terminal UI its status rules read. It launches as `kiro-cli chat` and takes the prompt as a positional argument, which Kiro holds behind its workspace trust prompt until you accept it. Kiro mints the session id. The manager reads it from `kiro-cli chat --list-sessions -f json`, run in the session's directory, which lists the new session once the workspace is trusted. Revive with a captured id runs `kiro-cli chat --resume-id {id}`. With no id, `kiro-cli chat --resume-picker` opens the picker. The first launch registers the agent-manager server with `kiro-cli mcp add --scope global --force`, passing `AGENT_MANAGER_SESSION_ID=${AGENT_MANAGER_SESSION_ID}` so each session's server reports as that session. Every `kiro-cli` subcommand needs a signed-in Kiro, so sign in once in a plain terminal first. Tool approvals stay with Kiro's own prompts, and `--trust-all-tools` is yours to add. On macOS, Linux and WSL the setup hint is `curl -fsSL https://cli.kiro.dev/install | bash`, and on Windows it is `irm 'https://cli.kiro.dev/install.ps1' | iex`. The session list and the MCP registration were exercised on Linux only.

GitHub Copilot CLI is tested with copilot 1.0.114 and needs 1.0.51 or later, the first release whose `--session-id` starts a new session under the given id. It launches as `copilot` under an id the manager mints (`--session-id {id}`) and takes the prompt through `-i`, which Copilot holds behind its folder trust prompt until you accept it. Revive runs `copilot --resume {id}`, and with no id `copilot --resume` opens the picker. On macOS, Linux and WSL each launch loads a plugin from the manager's own directory with `--plugin-dir`, whose `sessionStart` and `userPromptSubmitted` hooks report the conversation the agent is on, so a `/new`, `/fork` or `/resume` moves the row at the next prompt. A subagent fires the same hooks under its own id with an `agentId`, which the main agent's input never carries, and those reports are skipped. Native Windows loads no plugin, as no CLI reports its conversation there, so a Copilot row there stays on the conversation it launched with. Copilot has no fork in the manager: `/fork` keeps the original running in the background of the same process, so a second pane opening either conversation stops on Copilot's "Session in use" prompt. The agent-manager server is passed for each session with `--additional-mcp-config`, pointing at a file in the manager's own directory, so your `~/.copilot/mcp-config.json` is never written. Copilot starts that server with the session's environment. The model list comes from `copilot --acp`, each model with its reasoning levels, launched with `--model` and `--reasoning-effort`. Copilot Free includes only Copilot Fusion, so on that plan ACP lists no models and the form says so. Tool approvals stay with Copilot's own prompts, and `--allow-all` is yours to add. Copilot draws its transcript in the alternate screen with a scrollbar in the last column, which the manager drops before it reads the pane. On macOS, Linux and WSL the setup hint is `curl -fsSL https://gh.io/copilot-install | bash`, and on Windows it is `winget install --id GitHub.Copilot -e`. Signing in is Copilot's own: run `copilot login` once, or export `COPILOT_GITHUB_TOKEN`. Status, revive, the conversation report and the MCP server were exercised on macOS and Linux, and the model list on Copilot Free and Copilot Business.

**When a status looks wrong.** The rules are ours to fix, for everyone. [Open an issue](https://github.com/wainmann/agent-manager/issues/new/choose) with the CLI and its version, plus the pane text it draws, which you can read the way the poller reads it. Replace `SESSION_ID` with the session id:

```bash
tmux -L agentmgr capture-pane -p -t am_SESSION_ID
```

A CLI that is not on the list above is a feature request; the `CLIs` row in Settings ends with `request CLI support`, which opens one prefilled.

## Tools

Tools are the panes Agent Manager provides itself, listed beside the agents: the terminal and the browser. `t` opens a shell tab under the selected agent or in the selected group (see [Terminal tabs](usage.md#terminal-tabs)), and `b` opens a browser tab the same way (see [Browser tabs](usage.md#browser-tabs)). A tool is built into the binary the way a CLI is, but it runs no agent. It stays out of the session pickers and the Settings `CLIs` row, and the keys that write a prompt into a pane refuse it. A session stores the name of its CLI or its tool, and no name is both.

The browser is the built-in `[tools.browser]` block. It runs `terminal-browser open --no-merge` in the group's directory, with the URL after it when one is given, so each pane holds a browser of its own. Its status rests at idle. It needs terminal-browser on PATH and tmux 3.3 or newer. Seeing the page needs the manager to run outside another tmux. Agents drive a browser either way. On Windows the browser runs only inside WSL, so run agent-manager inside your WSL distro.

## Key bindings

The **keybindings** row in Settings opens one picker with two tables: the keys the manager keeps inside a session, and the keys of its own list. Each action takes one key or several, and an action can be turned off. One key serves one action within a table.

### The picker

The session keys come first and the manager's below them. `↵` binds the key you press next, `a` adds a second key to an action, `d` turns the action off, and `r` names what would move and asks, then puts the shipped keys back on every action. A key its table cannot take is refused there with the reason. Leaving the picker stores each table you changed and puts the keys to work at once, so no restart is needed.

Only a key you moved is stored. An action at its shipped default follows that default, so a release that changes one reaches you, and a key you move back to its default follows it again. When a release adds an action whose default key you already gave to another action, your choice stays and the new action starts without a key, ready for you to give it one.

### Inside a session

Inside a session, attached or focused, the manager keeps a few keys for itself and hands every other key to the agent. The actions are `detach` (default `ctrl+q` and `ctrl+\`, back to the manager), `review` (default `ctrl+r`, the session's diff review), `editor` (default `f3`, its directory in your editor), `saved_prompt` (default `f4`, the saved prompts list; focused only, since an attach does not bind it), `resize` (default `ctrl+]`, the split's divider, the same gesture the list has; kept by a focused session, since an attach has no split and leaves the key to the agent) and `tmux_prefix` (off by default). Moving one frees a key that collides with a key your agent uses, and turning one off hands its key to the agent like any other.

In a focused session, PgUp/PgDn scroll the pane's tmux history when the pane is on its normal screen, has not claimed mouse input, and has history to show. Otherwise the agent gets them, and alt+PgUp/alt+PgDn always reach the agent. Hermes recalls earlier prompts with Up/Down.

A session key is `ctrl+<letter>` (the symbols `@ \ ] ^ _` too), `alt+<letter or digit>`, or `f1` to `f12`. A key with no modifier is refused here, since it would take a character away from the agent, as are `ctrl+i`, `ctrl+m` and `ctrl+[`, which the terminal sends as tab, enter and escape. Bubble Tea, the framework the manager is built on, cannot read `ctrl+shift` combinations yet, so those are out for now. `detach` always keeps at least one key: it is the way back from a focused session.

`tmux_prefix` gives managed sessions a tmux prefix of their own, one key or two for tmux's `prefix` and `prefix2`. With it set, the key your own tmux.conf uses as its prefix is free to be a session key, `detach` for one. Turned off, sessions go back to the prefix your tmux.conf sets.

### In the list

Every key the list answers to is an action in the picker: `up`, `down`, `open`, `attach`, `step_in`, `step_out`, `reorder_up`, `reorder_down`, `new_session`, `terminal`, `browser`, `new_group`, `fork`, `prompt`, `copy_reply`, `review`, `mark_idle`, `rename`, `move`, `editor`, `restart`, `kill`, `kill_all`, `revive`, `revive_all`, `archive`, `cancel_end`, `restore`, `delete`, `search`, `filter`, `archived`, `empty_groups`, `fold_all`, `resize`, `settings`, `messages`, `help` and `quit`.

A list key is a plain character (`n`, `N`, `?`, `|`), a key name (`space`, `enter`, `tab`, `backspace`, `delete`, `up`, `down`, `left`, `right`, `home`, `end`, `pgup`, `pgdn`), `shift+` an arrow or tab, or the `ctrl+`, `alt+` and `f1` to `f12` forms above. `esc` and `ctrl+c` stay as they are. `esc` cancels everywhere. `ctrl+c` quits the manager even with `quit` turned off, and a focused session sends it to the agent. With saved prompts open over that session, `ctrl+c` closes the list. `settings` keeps at least one key, so the picker stays reachable. The footer, the `?` key map and the empty-list hints all read the table, so a moved key is named where it moved to.

### Where the keys apply

The same table drives a full-screen attach, where the keys are tmux bindings on the `agentmgr` server, and focus mode, where the manager reads them itself; the session footer, the focus footer and the `?` key map all name whatever the table says. A session created from the `agent-manager` CLI or by an agent through the MCP server reads the same stored table, so it carries your keys too.

## Right-to-left text

Hebrew and Arabic rows are painted as the cells they occupy, the same on every host. A terminal that runs its own bidirectional layout, iTerm2's right-to-left support or WezTerm's `bidi_enabled`, reorders those rows itself; turn that support off to read the frame in the columns Agent Manager paints.
