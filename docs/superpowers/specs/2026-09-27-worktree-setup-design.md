# Worktree Setup and Trust — Design

Date: 2026-09-27
Status: approved in chat, pending spec review

## Goal

Make every new tree of a stew repository usable with no manual steps: a new git worktree, made by hand, by a
script, or by a coding agent, and a fresh clone.

- `stew git install post-checkout` installs a hook that sets up each new worktree.
- `stew setup-worktree` is an alias that runs the `worktree.setup` section, like `stew setup` runs `setup`.
- `workspace_trust` and `project_trust` name the commands that make a wrapper usable in a new tree, such as
  `direnv allow .` or `mise trust`. stew runs them, with the user's consent, before the first wrapped command.
- `stew trust` shows every trust command, runs them all again with consent, and replaces the record.

Trust exists because a wrapper can refuse to run in a new tree. direnv and mise trust files by path, and a new tree
is a new path. The wrapper wraps every command, so a section cannot fix it.

This spec changes `2026-09-25-steward-design.md`. The implementation folds these rules into it, so it stays the
current description of stew. Anything not mentioned here is unchanged.

## CLI

```
stew init
stew add <path> [-a/--alias <name>] [--trusted]
stew setup-worktree [project...] [--dry-run]
stew trust [--yes]
stew git install pre-commit|pre-push|post-checkout
```

## Keys

### `.stew/config.toml`

```toml
# Wraps every command stew runs. {{STEW_STEP}} marks where the command goes. "" means none.
workspace_wrapper = ""
# Makes the wrapper usable in a new tree, e.g. "direnv allow .". Runs once per tree, with consent. "" means none.
workspace_trust = ""
```

| Key               | Required | Rule                               |
| ----------------- | -------- | ---------------------------------- |
| `workspace_trust` | yes      | String. `""` means no trust step.  |

### `stew.toml`

| Key             | Required | Rule                              |
| --------------- | -------- | --------------------------------- |
| `project_trust` | yes      | String. `""` means no trust step. |

The `stew add` template gains, after `project_wrapper`:

```toml
# Makes the project wrapper usable in a new tree, e.g. "mise trust". Runs once per tree, with consent.
# "" means none.
project_trust = ""
```

- A missing key is an error, as for the wrappers: `<file>: missing key "workspace_trust"`.
- A trust command is not a wrapper. It has no placeholder and no `sh -n` check.

## Trust

### Entries

- A trust entry is the workspace, when `workspace_trust` is not `""`, and each project whose `project_trust` is not
  `""`. The name of an entry is `workspace` or the project name.
- Order: the workspace first, then projects by name.
- An entry is pending unless `.stew/trust.json` records the same command for it under the same absolute root.
  So a new worktree or clone (no state file), a moved or copied tree (another root), and a changed command all make
  entries pending.

### When Trust Is Needed

| Command                                      | Entries run                    |
| -------------------------------------------- | ------------------------------ |
| `stew run`, every alias, without `--dry-run` | pending entries                |
| `stew exec`                                  | pending entries                |
| `stew add`                                   | the added project, if pending  |
| `stew trust`                                 | every entry, pending or not    |

- The check happens after validation, and before the step directory, the run directory, and any command.
- `--dry-run`, `list`, `remove`, `runs *`, `init`, and `git install` never check or run trust.
- With no entry to run, nothing is printed and the command goes on.

### Consent

With entries to run, stew prints them and asks, unless consent was given on the command line.

```
┌───────────┬────────────────┐
│ trust     │ command        │
├───────────┼────────────────┤
│ workspace │ direnv allow . │
│ core      │ mise trust     │
└───────────┴────────────────┘
run these trust commands? [y/N] 
```

- Same bordered table style as `stew list`. The table and the prompt go to stdout.
- The answer is one line from stdin. `y` or `yes`, in any case, runs the entries. Anything else, including an empty
  line or end of input, declines.
- stew asks only when stdin and stdout are both terminals, as the pager checks stdout. Otherwise it takes the
  no-terminal row below, so `stew build | tee log` never waits on a prompt nobody sees.

| Case                                        | Result                                                                    |
| ------------------------------------------- | ------------------------------------------------------------------------- |
| `stew trust --yes`, `stew add --trusted`    | Runs the entries without asking. The table is not printed.                |
| Both terminals, answer yes                  | Runs the entries, then goes on with the command.                          |
| Both terminals, answer no                   | `stew: trust declined`, exit 1. Nothing runs.                             |
| Not both terminals                          | `stew: untrusted: workspace, core (run: stew trust)`, exit 1. Nothing runs. |

For `stew add`, the last row reads `(run: stew add --trusted)`, and exit 1 leaves the registry unchanged.
For `stew trust`, it reads `(run: stew trust --yes)`.

- A command with a control character (a multi-line TOML string) shows in the table with the `$'…'` quoting of the
  `stew runs show` header, so each entry stays on one row.

### Running an Entry

- Runs as `sh -c <cmd>`, with no wrapper, in the root for the workspace and in the project directory for a project.
- The environment is inherited, plus `STEW_ROOT`, and `STEW_PROJECT` for a project. Inherited `STEW_*` run variables
  are removed first, as in `stew exec`.
- stdin is `/dev/null`. stdout and stderr go to stew's stdout and stderr.
- Entries run one at a time, in entry order. After each success, stew records it in `.stew/trust.json`.
- A failure prints `stew: trust <name>: exit N` (or `signal <name>`, or `cannot start: <error>`), exits 1, and runs
  nothing else. Entries that succeeded before it stay recorded.
- A failed save of `.stew/trust.json` prints `stew: trust: save .stew/trust.json: <error>` and exits 1.

### `.stew/trust.json`

```json
{"root": "/home/zz/proj/steward", "workspace": "direnv allow .", "projects": {"libs/core": "mise trust"}}
```

| Field       | Content                                                                     |
| ----------- | ---------------------------------------------------------------------------- |
| `root`      | Absolute workspace root, symlinks resolved, when the entries were recorded. |
| `workspace` | The workspace trust command that last succeeded. Absent when none.          |
| `projects`  | By project path, the project trust command that last succeeded.             |

- Machine-written only, via `encoding/json`, without HTML escapes, as one line. Saved atomically: a temp file in
  `.stew/`, then rename.
- Keyed by path, not name: trust belongs to a directory. A save drops paths that are no longer registered.
- A `root` other than the current root makes every entry pending. The next save starts from an empty record.
  Both are compared with symlinks resolved, so a tree reached through a symlinked path keeps its record.
- A missing file records nothing. An unreadable or invalid file also records nothing, and the next save replaces it.
- stew cannot see a tool's own reasons to re-trust. If a pull changes `.envrc`, direnv blocks and the wrapper fails.
  Run `stew trust` to run every entry again.
- `.stew/.gitignore` holds `trust.json`, so each tree keeps its own.

## Commands

### `stew init`

- Writes `workspace_trust = ""` into `.stew/config.toml`, after `workspace_wrapper`, with the comment shown above.
- Writes `.stew/.gitignore` containing `runs/` and `trust.json`, one per line.

### `stew add <path> [-a/--alias <name>] [--trusted]`

After the name checks and before the registry write:

- When the project's `project_trust` is not `""`, the entry is checked as in Consent. `--trusted` grants consent.
- A declined or failed trust rejects the add (exit 1). A `stew.toml` written from the template is removed, as when
  the registry write fails.
- A new project from the template has `project_trust = ""`, so it never needs trust.

### `stew trust [--yes]`

- Loads and validates the workspace.
- Runs every entry, pending or not, after consent as in Consent. Use it when a tool needs trust again, e.g. after a
  pull changes `.envrc`.
- The record starts empty: `.stew/trust.json` ends up holding only the entries this run recorded, under the current
  root. After a failure, the entries after it are pending again.
- No entry at all (every trust command is `""`): prints `nothing to trust`, exit 0. With `--yes` it prints nothing,
  so the hook stays quiet.
- Exit 0 when every entry ran.

### `stew setup-worktree [project...] [--dry-run]`

An alias over `stew run`, in the Aliases table:

| Alias                                          | Runs                                                                            |
| ---------------------------------------------- | ------------------------------------------------------------------------------- |
| `stew setup-worktree [project...] [--dry-run]` | `stew run '<project>:worktree\.setup'...`, or `'.*:worktree\.setup'` with none. |

- Every alias rule applies: regex quoting, `--dry-run`, the errors, and the trust check of `stew run`.
- A named project without the section: `pattern "web:worktree\.setup" matches no section` (exit 2).
- An unknown project: `unknown project "web"` (exit 2).
- No project defines `worktree.setup`: `pattern ".*:worktree\.setup" matches no section` (exit 2).
- `run.json` `argv` records the command as typed, e.g. `["setup-worktree"]`.
- The alias name differs from its section name, unlike `setup` and `build`. `stew worktree` would read as a command
  that manages worktrees.

Example:

```toml
name = "api"
project_wrapper = "mise exec -- {{STEW_STEP}}"
project_trust = "mise trust"

[setup]
skip_if = "test -d node_modules"
run = "npm ci"

[worktree.setup]
run = "cp \"$(git worktree list --porcelain | sed -n '1s/^worktree //p')/services/api/.env\" .env"
requires = ["api:setup"]
```

- `worktree` is a namespace, so a project cannot also have a `[worktree]` section.
  This is the existing rule: `<file>: [worktree]: a section cannot contain sections`.

### `stew git install post-checkout`

`Supported` gains `post-checkout`. Install steps are unchanged: find the root, find the hooks directory with
`git rev-parse --git-path hooks`, reject an existing hook file, write it with mode `0755`.
All worktrees of a repository share one hooks directory, so one install covers every worktree.

The hook:

```sh
#!/bin/sh
# installed by stew
case "$1" in *[!0]*) exit 0 ;; esac
dir="$(git rev-parse --show-toplevel)/<rel>"
[ -d "$dir/.stew" ] || { echo "stew: no workspace in $dir, skipping worktree setup" >&2; exit 0; }
cd "$dir" && stew trust --yes && exec stew setup-worktree
```

- `<rel>` is the root's path below the git top level, escaped for a double-quoted string, as in the other hooks.
  When it is `.`, the line is `dir="$(git rev-parse --show-toplevel)"`.
- Installing the hook is the consent to trust new worktrees, so it passes `--yes`.
- The hook runs the checked-out commit's trust commands and `worktree.setup`, so `git worktree add <ref>` runs that
  ref's commands without asking. Installing the hook is consent to that, as sections were already branch-controlled.
- git passes the previous HEAD as `$1`. It is all zeros only when there was no previous HEAD: a new worktree, or a
  clone whose template installs the hook. Any other checkout (`git checkout`, `git switch`, a file checkout) exits 0
  at once, and stew does not start.
- The check matches the null ref of any length: 40 zeros for SHA-1, 64 for SHA-256.
- A worktree at a commit without the workspace (a branch from before stew) prints the note on stderr and exits 0.
- `git worktree add` exits with the hook's status, and keeps the worktree either way.
  A failed trust or section gives exit 1. No project defining `worktree.setup` gives exit 2, as for `ci.<hook>`.
  A repo that needs only trust defines `worktree.setup` with `run = ""`.
  Rerun with `stew setup-worktree` after fixing the cause.
- `git worktree add --no-checkout` runs no hook. Run `stew setup-worktree` after the checkout.
- git runs the hook with the new worktree as cwd and top level, and without `GIT_DIR` or `GIT_INDEX_FILE`, so
  section commands act on the new worktree.
- The hook calls `stew` from `PATH`, as the other hooks do.
- A repository that already has a `post-checkout` hook (git-lfs installs one) is rejected, as for the other hooks.

### A Fresh Clone

No hook runs in a fresh clone. The first wrapped command asks for trust on a terminal. Run `stew setup-worktree`
to set it up as the hook would. CI runs `stew trust --yes` before its first wrapped command.

## Exit Codes

Code 1 gains: trust declined, untrusted with no terminal, and a failed trust entry or state save.

## Code Layout

| File                          | Change                                                                                |
| ----------------------------- | ------------------------------------------------------------------------------------- |
| `internal/workspace/config.go`  | `workspace_trust`.                                                                  |
| `internal/workspace/project.go` | `project_trust`; the template.                                                      |
| `internal/trust/`             | Entries, pending check, `trust.json` load and save, running entries. No terminal code. |
| `cmd/stew/trust.go`           | `stew trust`, the consent table and prompt, shared by `run`, aliases, `exec`, `add`.  |
| `cmd/stew/main.go`            | `process` gains stdin and whether it is a terminal. Registers `trust` and `setup-worktree`. |
| `cmd/stew/run.go`             | `newAliasCmd` takes the command name and the section separately.                      |
| `cmd/stew/init.go`            | Writes `workspace_trust` and the new `.gitignore` line.                               |
| `internal/githook/githook.go` | A hook→script table. `Supported` derives from it; `Script` renders the hook's body.   |
| Repo files                    | `.stew/config.toml`: `workspace_trust = "direnv allow ."`. `stew.toml`: `project_trust = ""` and `[worktree.setup]` with `run = ""` and `requires = ["steward:setup"]`. `.stew/.gitignore`: `trust.json`. |

- `internal/trust` runs entries through a small runner interface, so unit tests use a fake.

## Testing

### Unit: `workspace`

- `workspace_trust` and `project_trust`: missing key, wrong type, `""` accepted, a command accepted. The template
  parses with `project_trust = ""`.

### Unit: `trust`

- Entries: `""` skipped; workspace first, then projects by name.
- Pending: no file; same root and command (not pending); changed command; other root (all pending); invalid file.
- Running: records after each success; a failure stops and keeps earlier records; the save is atomic.
- Env: `STEW_ROOT` and `STEW_PROJECT` set, inherited `STEW_*` run variables removed; cwd per entry.

### Unit: `githook`

- `Script` bytes for `post-checkout`, at `rel` `.` and at a nested path with characters that need escaping.
- An unsupported hook name lists all three supported hooks.

### Integration: testscript

Trust, with in-process `stew` where stdin is not a terminal:

- `stew build` with a pending entry: `untrusted` message, exit 1, no section runs, no run directory.
- `stew trust --yes` runs entries in order, in the right directories, and records them; `stew build` then runs no
  trust entry.
- A second `stew trust --yes` runs every entry again. A stale record (an entry since set to `""`) is gone after it.
- `stew trust` with every trust command `""` prints `nothing to trust`; with `--yes` it prints nothing.
- A changed trust command becomes pending again. A failing entry: exit 1, the message, earlier entries recorded,
  later ones pending.
- `stew exec` checks trust. `--dry-run`, `list`, and `runs list` do not.
- `stew add` of a project with trust: without `--trusted` exits 1 and leaves the registry unchanged; with it, runs the
  entry and registers the project.
- `init` writes `workspace_trust` and the `.gitignore` lines; `git status` does not show `.stew/trust.json`.

Consent prompt, on a pseudo-terminal (the existing shell session test):

- A terminal stdin with a piped stdout takes the no-terminal path.

- `y` runs the entries and the command; `N`, an empty line, and end of input decline with exit 1.
- Before a wrapped command, the table lists exactly the pending entries. For `stew trust`, it lists every entry.

`setup-worktree` alias, with in-process `stew`:

- It runs `worktree.setup` in every project that defines it, after the sections it requires.
- A named project; a named project without the section; an unknown project; no project defining it (exit 2).
- `--dry-run`.

`post-checkout` hook, with `exec stew` (git hooks run the `stew` on `PATH`), in its own script:

- Each triggering scenario runs two successful stew processes, about 2 s under `-race`. Fold checks into as few
  `git worktree add` calls as the scenarios allow. This script is expected to be the slowest under `TestScripts`.


- `git worktree add` trusts and runs the section in the new worktree: its markers land there, not in the main
  worktree.
- `git switch -c` in an existing worktree runs nothing.
- A failing section: `git worktree add` exits 1, and `git worktree list` still lists the worktree.
- No project defines `worktree.setup`: `git worktree add` exits 2.
- A SHA-256 repository triggers the hook.
- A new worktree at a commit without `.stew/` prints the note and exits 0.
- A workspace below the git top level: the hook runs stew there.
