# Custom Sections and Section Graph — Design

Date: 2026-09-26
Status: approved in chat, pending spec review

## Goal

Replace the fixed phases (`setup`, `build`, `ci.<level>`) and project-level `dependencies` with:

- free-form sections in `stew.toml`;
- a dependency graph of `project:section` nodes, built only from each section's `requires`;
- one run command, `stew run <regex>...`, with `setup`, `build`, and `ci` as aliases;
- `--dry-run`, which prints what a run would execute.

This spec changes `2026-09-25-steward-design.md` and `2026-09-25-wrappers-design.md`. The implementation folds
these rules into both files, so they stay the current description of stew. Anything not mentioned here is unchanged.

## Terms

- **Section.** A table in `stew.toml` with commands. Replaces "phase" everywhere: code, docs, and output.
- **Key.** `<project>:<section>`, e.g. `my.lib:ci.full`. Project names cannot contain `:`, so the key splits
  at its only `:`. The same key is used in `requires`, patterns, log file names, `STEW_TAG`, and `blocked_by`.

## `stew.toml`

```toml
name = "api"
project_wrapper = ""

[setup]
skip_if = "test -d node_modules"
run = "npm ci"

[build]
run = "npm run build"
verify = "test -f dist/index.js"
requires = ["api:setup", "core:build"]

[ci.full]
run = "npm test"
requires = ["api:build"]

[ci.quick]
run = "npm run lint"
requires = ["api:build"]

[ci.pre-commit]
run = ""
requires = ["api:ci.quick"]
```

| Key / section     | Required | Rule                                                            |
| ----------------- | -------- | --------------------------------------------------------------- |
| `name`            | yes      | Unchanged. Unique; `[a-z0-9][a-z0-9._-]*`.                        |
| `project_wrapper` | yes      | Unchanged.                                                      |
| any other table   | no       | A section. A project may have zero sections.                   |
| `run`             | yes      | String. `""` allowed.                                           |
| `skip_if`         | no       | String. Absent and `""` both mean none.                         |
| `verify`          | no       | String. Absent and `""` both mean none.                         |
| `requires`        | no       | List of keys. Absent means `[]`.                                |

- `dependencies` is removed. It is now an unknown key.
- `setup`, `build`, and `ci.full` are no longer required. No section name is special.
- Unknown keys inside a section are rejected, as before.

### Section Names

- A dotted TOML table is a dotted name: `[ci.full]` is section `ci.full`.
- A table is a section when it holds any of `run`, `skip_if`, `verify`, `requires`.
  Otherwise it is a namespace and must hold only tables.
- A table cannot be both. `[ci]` with `run`, plus `[ci.full]`, is an error:
  `<file>: [ci]: a section cannot contain sections`.
- A namespace with a non-table key is an unknown key, as today.
- An empty table (`[lint]` with no keys) is a namespace with nothing in it, so it defines no section.
- Each name segment must match `[a-z0-9][a-z0-9_-]*`. So a quoted key with a `.`, as in `["ci.full"]`, is rejected:
  `<file>: invalid section name segment "ci.full" (want [a-z0-9][a-z0-9_-]*)`.
- The full name is the segments joined by `.`.

### `requires` Checks

At load, for every entry:

- It must contain exactly one `:`: `<file>: [build]: requires "core": want <project>:<section>`.
- Its project must exist: `... requires "core:build": unknown project "core"`.
- Its section must exist in that project: `... requires "core:build": core has no section "build"`.
- It must not name its own section: `... requires "api:build": section requires itself`.
- No duplicates within one list: `... duplicate requires "core:build"`.

Across the workspace, the node graph must have no cycle. The error prints the cycle by key:
`cycle: api:build -> core:build -> api:build`. It is found as today: names visited in sorted order.

A section may require another section of the same project. It must name the project: `api:setup`.

### `stew add` Template

```toml
name = "<name>"
# Wraps every command of this project, inside the workspace wrapper.
# {{STEW_STEP}} marks where the command goes. "" means none.
project_wrapper = ""

# Sections: any [name] with a `run` key. `stew run '<regex>'` runs sections whose
# <project>:<section> key matches; `stew build` is `stew run '.*:build'`.
# skip_if exit 0 skips the section. verify runs after run and must exit 0.
# requires lists sections that must succeed first, as "<project>:<section>".
#
# [build]
# run = ""
# skip_if = ""
# verify = ""
# requires = []
```

The template defines no section. It is valid on creation and does nothing until filled in.

## Section Algorithm

Replaces the Phase Algorithm table. Stew never invokes `sh -c ""`.

| Step      | When       | Result                                  |
| --------- | ---------- | --------------------------------------- |
| `skip_if` | not `""`   | exit 0: `skip`. Non-zero: continue.     |
| `run`     | not `""`   | Non-zero: `fail`.                       |
| `verify`  | not `""`   | Non-zero: `fail`.                       |
| end       |            | `run` or `verify` ran: `done`. Else `skip`. |

- A failed `skip_if` means "not done yet". Its output is not replayed, as with today's pre-run `verify`.
- A wrapper failure in `skip_if` fails the section, as today.
- `run = ""` with `verify` set runs `verify` once. It is an assertion.
- `run = ""` with only `skip_if` set gives `skip` either way: nothing is left to run.
- Step markers: `--- stew: <step>: <cmd>`, where `<step>` is `skip_if`, `run`, or `verify`.
- Status words for every section: `done`, `skip`, `fail`, `blocked`, `interrupted`. `pass` is removed.
- A section that runs no command gets no log files, as today.

## Commands

### `stew run <regex>... [--dry-run]`

- At least one regex. Each is Go RE2 and must match a whole key, as in `stew runs show`.
- Every regex must match at least one defined section. Otherwise exit 2 before anything runs:
  `pattern "web:build" matches no section`. The pattern prints as given, between plain double quotes.
- An invalid regex is exit 2 with the regex error.
- Selected: every matched section, plus its `requires`, transitively.

### Aliases

| Alias                                    | Runs                                                           |
| ---------------------------------------- | -------------------------------------------------------------- |
| `stew setup [project...] [--dry-run]`    | `stew run '<project>:setup'...`, or `'.*:setup'` with none.     |
| `stew build [project...] [--dry-run]`    | Same with `build`.                                              |
| `stew ci [project...] [-l <level>] [--dry-run]` | Same with `ci.<level>`. `--level` defaults to `full`.    |

- Project names and the section name are regex-quoted, so `my.lib` matches only `my.lib`.
- A named project without the section fails its pattern: `pattern "web:build" matches no section` (exit 2).
- An unknown project name keeps its own error: `unknown project "web"` (exit 2).
- `--level` takes any name that makes `ci.<level>` a valid section name. There is no fallback chain and
  no fixed level list. `stew ci -l lint` runs `ci.lint`. `stew ci -l 'a b'` is exit 2: `invalid CI level "a b"`.
- `run.json` `argv` records the command as typed, e.g. `["ci", "--level", "pre-commit"]`.
- Git hooks are unchanged: `stew ci --level pre-commit|pre-push`. In a workspace where no project defines that
  section, the hook fails with exit 2.

### Order and Execution

- Order: topological over selected nodes (Kahn). Among ready nodes, the smallest project name goes first,
  then the smallest section name. The order is identical on every run.
- Execution stays sequential, one section at a time.

### Failure and Blocking

- A section is `blocked` when any section it requires failed or was blocked.
- Blocking follows edges only. Other sections of the same project keep running.
- Nothing below a failure runs. Output is pruned: only sections that directly require a failed section print a line.
  - Line: `==> <project>: <section> ... blocked by <key>, <key>`, at its place in the order.
    It lists the direct requirements that failed, in execution order.
  - A blocked section whose failed-or-blocked requirements are all blocked prints nothing.
- The summary still shows `blocked` for every blocked section, printed or not.
- `run.json` records every blocked section. Its `blocked_by` lists all direct requirements that failed or were
  blocked, in execution order. `stew runs show` without regexes applies the same pruning rule from the recorded
  statuses. With regexes, every matched blocked section prints its line, listing its whole `blocked_by`,
  so a section asked for by name is never hidden.
- The exit code is 1 when any section failed or was blocked, as before. Interrupt handling is unchanged.

Example: `core:build` requires `core:setup`, `api:build` requires `core:build`, and `core:typecheck` requires nothing.

```
==> core: setup ... fail (2.7s)
--- stew: run: npm ci
(exit 2)
==> core: build ... blocked by core:setup
==> core: typecheck ... done (0.4s)
```

`api:build` prints nothing. Its summary cell is `blocked`, and its `run.json` record has `"blocked_by": ["core:build"]`.

### Summary

- Rows: projects, in order of their first section in execution order.
- Columns: sections, in order of first appearance in execution order.
- A cell is the status word, or `-` when that project's section was not selected.
- The `(quick)` fallback cell suffix is removed. Nothing else changes.

### `--dry-run`

Prints the execution order and each section's direct `requires`, then exits. Available on `run` and every alias.

```
┌───┬──────────────┬───────────────────────┐
│ # │ key          │ requires              │
├───┼──────────────┼───────────────────────┤
│ 1 │ api:setup    │ -                     │
│ 2 │ core:setup   │ -                     │
│ 3 │ core:build   │ core:setup            │
│ 4 │ api:build    │ api:setup, core:build │
│ 5 │ api:ci.full* │ api:build             │
└───┴──────────────┴───────────────────────┘
* matched by a pattern; others are pulled in by requires
```

- `*` marks sections a pattern matched. The footer line prints only when some row is not matched.
- `requires` lists direct requirements in execution order, or `-`.
- Runs nothing. Creates no run directory, step directory, or log. Adopts no orphans.
- Load, validation, and pattern errors are the same as a real run (exit 2). Otherwise exit 0.
- Same bordered table style as `stew list`. No `--porcelain`.

### `stew list`

- The dependencies column is derived: the other projects named in any `requires` of the project, sorted.
- `--porcelain` uses the same derived list.

### `stew remove`

- "Needed by" is derived the same way: a kept project whose `requires` name a removed project blocks the removal.
- The message is unchanged: `stew: cannot remove core: needed by api, web`.

### `stew add` (existing `stew.toml`)

- Every `requires` entry must name a registered project (`add it first`) and a section that project defines.
- An entry naming the project itself is checked against its own sections.

### `stew runs show`

- Regexes match the key `<project>:<section>`, e.g. `api:build`, `.*:ci\..*`.
- The section line has no `-> <used>` form.
- `--porcelain`: `<project>\t<section>\t<status>\t<duration-ms>\t<log-path>`.
- An unfinished section's project and section come from its `.log` name, split at `:`.

## Environment

| Variable       | Value                                            |
| -------------- | ------------------------------------------------ |
| `STEW_SECTION` | section name, e.g. `ci.quick`. Replaces `STEW_PHASE`. |
| `STEW_TAG`     | the key, e.g. `core:ci.quick`. It is now parseable at `:`. |

`STEW_RUN_ID`, `STEW_ROOT`, and `STEW_PROJECT` are unchanged.

## Files

- Run log files: `<project>:<section>.stdout`, `.stderr`, `.log`, e.g. `.stew/runs/<id>/api:build.log`.
- Step directory files keep a `:`-free name, `<project>-<section>.step`, so the step path still matches
  `[A-Za-z0-9/._-]+`. Two keys can map to the same name (`a-b:c`, `a:b-c`), which is harmless:
  steps run one at a time, and a step's files are removed after it.
- `run.json`:
  - `columns` holds section names.
  - `phases` becomes `sections`. Each record's `phase` becomes `section`. `used` is removed.
  - `blocked_by` holds keys.
  - Old run directories are not migrated. `stew runs show` reads them without error:
    an absent `sections` field loads as empty, and a `.log` name without `:` is not an unfinished section.
    So an old run shows its header and a summary whose cells are all `-`. `stew runs prune` removes them.

## Code Layout

- `internal/workspace`: `Project` holds `Sections map[string]*Section` (`Run`, `SkipIf`, `Verify`, `Requires`).
  `Level`, `ParseLevel`, `ResolveCI`, and `Dependencies` are removed.
  The graph is over keys: validation, cycle search, topological order, and selection by regex.
  Derived project dependencies come from one helper used by `list` and `remove`.
- `internal/runner`: `Plan` is an ordered list of sections, each with its key, directory, wrappers, commands,
  and direct requirements. `Phase`, `Job`, `CI`, `Used`, and `Fallback` are replaced.
  The runner blocks per section instead of per project.
- `cmd/stew`: `phase.go` becomes the `run` command plus the three aliases, sharing one code path.
  `plan.go` builds the plan from the selected keys. `--dry-run` renders the plan through `internal/report`.
- Old and new schemas cannot coexist without shims. So the core change lands as one standalone commit:
  schema, graph, runner, `run` and aliases, `runs show`, `list`, `remove`, `add`, this repo's `stew.toml`,
  and the doc fold. `--dry-run` lands as a second standalone commit.

### This Repo's `stew.toml`

Its git hooks run `stew ci --level pre-commit|pre-push`, which today rely on fallback.
Without fallback, it must define both levels, or every commit and push fails with exit 2:

```toml
name = "steward"
project_wrapper = ""

[setup]
run = "go mod tidy"

[build]
run = "go build ./..."
requires = ["steward:setup"]

[ci.full]
run = "go test -race ./..."
requires = ["steward:build"]

[ci.quick]
run = "go test ./..."
requires = ["steward:build"]

[ci.pre-commit]
run = ""
requires = ["steward:ci.quick"]

[ci.pre-push]
run = ""
requires = ["steward:ci.full"]
```

Other workspaces using `stew ci --level pre-commit|pre-push` hooks need the same two sections.

### Hook and Binary During the Change

- The dev shell's `stew` rebuilds from `$STEW_ROOT` on every call, and the hook execs that `stew`.
- A session whose `STEW_ROOT` points at the main checkout builds main's old source. That binary rejects the new
  schema, so worktree commits run with `STEW_ROOT` set to the worktree: `STEW_ROOT=$PWD git commit`.
- `git cherry-pick` onto main runs no pre-commit hook. After it, `stew ci --level pre-commit` runs in main
  to confirm the new binary accepts main's new `stew.toml`.

## Testing

- Unit tests for section parsing: dotted names, namespace vs section, segment rules, `requires` checks, cycles.
- Unit tests for selection (regex full match, no-match error, transitive requires) and tie-breaking order.
- Runner tests for the new algorithm table, per-section blocking, and `blocked by` keys.
- testscript scripts updated for the new schema, plus new ones for `stew run`, alias quoting (`my.lib`),
  `--dry-run`, `--level` with a custom name, and a git hook in a workspace without the section.
- `go test -race ./...` passes at every commit that lands on main.
