# steward (`stew`) — Design

Date: 2026-09-25
Status: draft, pending review

## Goal

`stew` is a small monorepo orchestrator written in Go.
It runs setup, build, and CI for explicitly registered subprojects.

- Stack-agnostic. The only contract is the `stew.toml` interface: shell commands per phase.
- Explicit. No auto-discovery. Projects exist only after `stew add`.
- Predictable. Required sections, strict validation, deterministic order, no implicit behavior.

## CLI

```
stew init
stew add <path> [-a/--alias <name>]
stew remove <name>... [--clean]
stew list [--porcelain]
stew setup [name...]
stew build [name...]
stew ci [name...] [-l/--level full|quick|pre-commit|pre-push]
stew git install pre-commit|pre-push
stew runs show <run-id> [<project-phase-regex>...] [--porcelain] [--no-pager]
stew runs list [--porcelain] [--no-pager]
stew runs prune [--keep-since <time>] [--keep-last-n <n>]
```

- Projects are referenced only by `name`. Only `stew add` takes a path.
- No names means all registered projects.
- Flags may appear before or after names.

## Root Discovery

- Every command except `init` finds the root by walking up from cwd to the first directory containing `.stew/`.
- Reaching `/` without finding `.stew/` is an error: `not a stew workspace`.

## Files

### Layout

```
<root>/
  .stew/
    .gitignore             # committed; contains `runs/`
    config.toml            # committed; the workspace wrapper
    projects.toml          # committed; the registry
    runs/                  # ignored; per-run command logs
      20260925T043601Z-3f9a/
        run.json
        core-setup.log
        core-setup.stdout
        core-setup.stderr
        core-build.log
        core-build.stdout
        core-build.stderr
  libs/core/stew.toml
  services/api/stew.toml
```

### `.stew/projects.toml`

```toml
projects = ["libs/core", "services/api"]
```

- Holds only project paths: root-relative, `/`-separated, sorted, unique.
- A path must be relative, must not contain `..`, and must not escape the root.
- The root itself is a valid project path (`"."`).
- Names are not stored here. The only source of a name is the project's `stew.toml`.
- Written atomically: write a temp file in `.stew/`, then rename.

### `.stew/config.toml`

Committed and hand-edited. `stew init` writes it:

```toml
# Wraps every command stew runs. {{STEW_STEP}} marks where the command goes. "" means none.
workspace_wrapper = ""
```

| Key                 | Required | Rule                           |
| ------------------- | -------- | ------------------------------ |
| `workspace_wrapper` | yes      | String. `""` means no wrapper. |

- A missing file is an error: `.stew/config.toml: missing`.
- Unknown keys are rejected.
- `.stew/projects.toml` is a machine-written registry and holds no configuration.
- A non-empty wrapper must contain `{{STEW_STEP}}` exactly once.
  Error: `<file>: <key>: must contain {{STEW_STEP}} exactly once (found N)`.
- With the placeholder replaced by a plain path, the wrapper must pass `sh -n -c <W>`.
  The error names the file and key, then the shell's message.
  Example: `.stew/config.toml: workspace_wrapper: sh: -c: line 1: unexpected EOF while looking for matching '"'`.
- `sh -n` only checks syntax. It runs nothing. The reach count (see Command Execution) checks the rest at runtime.

### `.stew/.gitignore`

```
runs/
```

Written by `stew init`. Stew never rewrites it and does not check it on load.

### `.stew/runs/` (Run Logs)

Every `setup`, `build`, or `ci` invocation is one run. Each run captures all command output to disk.

- **Run directory.** `.stew/runs/<run-id>/`, created after validation and before the first phase.
  `.stew/runs/` itself is created on demand.
- **Run ID.** `<UTC time>-<4 random hex>`, e.g. `20260925T043601Z-3f9a`. IDs sort by start time.
  The directory is created with `os.Mkdir`. If it already exists, a new random suffix is drawn.
- **Lock.** From creation until it exits, stew holds an exclusive `flock` on the run directory.
  The kernel drops it when stew exits or dies, so an unlocked run is finished or crashed. Commands do not inherit it.
  After locking, stew checks the directory is still the one it made. If a concurrent `stew runs prune` removed
  or locked it first, stew draws a new suffix. Any other lock failure exits 1 before any phase runs,
  as when the directory cannot be created.
- **Files.** `<project>-<phase>.stdout`, `<project>-<phase>.stderr`, and `<project>-<phase>.log`,
  e.g. `api-build.stderr`. `<project>-<phase>` is the phase's key.
  - One run directory holds a triple for every project-phase the run executed.
    Because phases are cumulative, `stew ci api` writes `core-setup`, `core-build`, `api-setup`, `api-build`,
    and `api-ci.<level>` triples into the same directory.
  - For CI, `<phase>` is the level that ran after fallback, e.g. `api-ci.quick.stdout`.
  - `.log` holds stdout and stderr combined, in the order stew received the writes, with no stream tags.
    It holds the same bytes as the live failure replay; order across the two streams is approximate.
    A mutex keeps each write whole.
  - A phase that runs at least one command gets all three files, even if some stay empty.
    They are created together. If one cannot be created, the phase does not start.
  - A phase that runs no command (`skip` with both commands `""`, or `blocked`) gets no files.
  - Each step in a phase appends to the same files, in step order. That includes a pre-run `verify`.
- **Step markers.** Before each step's output, stew writes one line to all three files:
  `--- stew: <step>: <cmd>`, where `<step>` is `verify`, `run`, or `verify after run`.
- **Manifest.** `run.json` records the run for `stew runs show` (see Run Manifest).
- **Location in output.** After the summary and total, stew prints `logs: .stew/runs/<run-id>` (root-relative).
- **Log errors.** No command runs without a complete log.
  - Run directory cannot be created: the command exits 1 before any phase runs.
  - A phase's log files cannot be created: that phase does not start.
  - A write to a log file (output or step marker) fails while a command runs: stew stops the command
    (SIGTERM to its process group, then SIGKILL after 5 s).
  - In both phase cases, the phase reports `fail` with last line `(log error: <error>)` in its content area.
    It is handled like any other `fail`: the project stops, its dependents are `blocked`,
    and independent projects keep running.
- **Retention.** Runs are never deleted automatically. `stew runs prune` deletes them on request.

### Run Manifest (`run.json`)

```json
{
  "argv": ["ci", "--level", "pre-commit"],
  "workspace_wrapper": "tool exec . {{STEW_STEP}}",
  "project_wrapper": {"api": "other-tool run {{STEW_STEP}}"},
  "columns": ["setup", "build", "ci.pre-commit"],
  "projects": ["core", "api", "web"],
  "phases": [
    {"project": "core", "phase": "setup", "used": "setup", "status": "skip", "duration_ms": 104},
    {"project": "core", "phase": "build", "used": "build", "status": "skip", "duration_ms": 31},
    {"project": "core", "phase": "ci.pre-commit", "used": "ci.quick", "status": "pass", "duration_ms": 8210},
    {"project": "api", "phase": "setup", "used": "setup", "status": "skip", "duration_ms": 95},
    {"project": "api", "phase": "build", "used": "build", "status": "fail", "duration_ms": 63012, "cause": "exit 1"},
    {"project": "web", "phase": "setup", "used": "setup", "status": "blocked", "blocked_by": ["api"]}
  ],
  "total_ms": 75004
}
```

| Field               | Content                                                                        |
| ------------------- | ------------------------------------------------------------------------------ |
| `argv`              | Arguments after `stew`, for the `stew runs show` header.                       |
| `workspace_wrapper` | Workspace wrapper as written; `""` when none. An absent field loads as `""`.   |
| `project_wrapper`   | Non-empty project wrappers as written, by project name. Absent when none.      |
| `columns`           | Summary columns, as in the live summary.                                       |
| `projects`          | Selected projects in execution order: the summary rows.                        |
| `phases`            | Phases that ended or were blocked, in execution order.                         |
| `phase`, `used`     | Requested and actual phase name. They differ only on CI fallback.              |
| `duration_ms`       | Phase duration in milliseconds. Absent on `blocked` and `interrupted`.         |
| `cause`             | Content area last line without parentheses. Only on `fail` and `interrupted`.  |
| `blocked_by`        | Direct dependencies that failed or were blocked. Only on `blocked`.            |
| `total_ms`          | Run wall time in milliseconds. Present only once the run has finished.         |

- JSON via `encoding/json`. The file is machine-written only.
- Every save rewrites the whole file from memory: a temp file in the run directory, then rename.
  A failed save is repaired by the next successful one.
- Saves happen at run start, after each phase ends or is blocked, and at run end.
- A phase that started but has no entry has a `.log` whose key is missing from `phases`.
  Execution is sequential, so at most one exists, unless phase-end saves failed. Several are sorted by key.
- Save errors follow the log error rules:

  | Save fails at | Effect                                                                                    |
  | ------------- | ----------------------------------------------------------------------------------------- |
  | Run start     | Exit 1 before any phase runs, like a run directory that cannot be created.               |
  | Phase end     | The phase becomes `fail` with last line `(log error: <error>)`, unless it already is `fail` or `interrupted`. Dependents are `blocked`; independent projects keep running. The record is kept as that `fail`; the next successful save includes it. |
  | Blocked       | Nothing else changes. The next successful save includes it.                              |
  | Run end       | `stew: log error: <error>` on stderr. Exit 1 if the exit code would otherwise be 0.      |

### `stew.toml`

```toml
name = "api"
dependencies = ["core"]
project_wrapper = ""

[setup]
run = "npm ci"
verify = "test -d node_modules"

[build]
run = "npm run build"
verify = "test -f dist/index.js"

[ci.full]
run = "npm test"

[ci.quick]
run = "npm run lint"

[ci.pre-commit]
run = "npm run lint -- --cache"

[ci.pre-push]
run = "npm run test:e2e"
```

| Key / section     | Required | Rule                                                            |
| ----------------- | -------- | --------------------------------------------------------------- |
| `name`            | yes      | Unique across the workspace. Must match `[a-z0-9][a-z0-9._-]*`. |
| `dependencies`    | yes      | List of other projects' `name`s. `[]` for none.                 |
| `project_wrapper` | yes      | String. `""` means no wrapper. Nested inside the workspace one. |
| `[setup]`         | yes      | Keys `run` and `verify`, both required.                         |
| `[build]`         | yes      | Keys `run` and `verify`, both required.                         |
| `[ci.full]`       | yes      | Key `run` required.                                             |
| `[ci.quick]`      | no       | Key `run` required if the section exists.                       |
| `[ci.pre-commit]` | no       | Key `run` required if the section exists.                       |
| `[ci.pre-push]`   | no       | Key `run` required if the section exists.                       |

- Required keys must be present. Their value may be `""`.
- Sections are forced so that an empty command is always a deliberate choice.
- `[ci.*]` sections accept only `run`. A `verify` key there is an unknown key.
- Any unknown key, unknown section, or unknown CI level is rejected.
- `project_wrapper` gets the same wrapper checks as `workspace_wrapper` (see `.stew/config.toml`).
  The error names the project's `stew.toml` and `project_wrapper`.

### `stew add` Template

For a new project, `stew add` writes this exact file, with `<name>` substituted:

```toml
name = "<name>"
dependencies = []
# Wraps every command of this project, inside the workspace wrapper.
# {{STEW_STEP}} marks where the command goes. "" means none.
project_wrapper = ""

# Each phase: `verify` runs first; exit 0 skips `run`.
# Otherwise `run` runs, then `verify` confirms. Empty strings are no-ops.
[setup]
run = ""
verify = ""

[build]
run = ""
verify = ""

# Levels: pre-commit falls back to quick; quick and pre-push fall back to full.
[ci.full]
run = ""
```

The template is valid on creation and does nothing until filled in.

## Validation

Every command except `init` and `git install` loads and validates the whole workspace before running anything.
Any error stops the command with exit code 2 and a message naming the file and problem.

Checks:

- `.stew/projects.toml` parses; paths are valid and unique.
- `.stew/config.toml` parses; `workspace_wrapper` passes the wrapper checks (placeholder count and `sh -n`).
- Every registered path contains a `stew.toml`.
- Every `stew.toml` parses with no unknown keys and has all required sections and keys;
  `project_wrapper` passes the wrapper checks.
- Every `name` is valid and unique.
- Every dependency names an existing project and is not the project itself.
- The dependency graph has no cycle. The error prints the cycle, e.g. `cycle: a -> b -> c -> a`.

## Commands

### `stew init`

- Creates `./.stew/projects.toml` containing `projects = []`.
- Creates `./.stew/config.toml` with `workspace_wrapper = ""` (see `.stew/config.toml`).
- Creates `./.stew/.gitignore` containing `runs/`.
- Rejects if `./.stew` already exists.
- Does not check ancestors. Nested workspaces are allowed, as with git.

### `stew add <path> [-a/--alias <name>]`

1. Find the root and load the workspace. An invalid workspace is an error.
2. Resolve `<path>` against cwd. It must be an existing directory inside the root.
3. Reject if the root-relative path is already registered.
4. If `<path>/stew.toml` already exists, use it unchanged:
   - It must parse and validate like any `stew.toml`.
   - The name is its `name`. Reject if `--alias` is given and differs.
   - Reject if a dependency is the project itself or is not registered (`add it first`).
     Projects are therefore added in dependency order, and no cycle can form.
5. Otherwise, the name is `--alias` if given, otherwise the directory's basename.
   For the root itself (`"."`), the basename of the root directory. Reject an invalid name (the error suggests `-a`).
6. Reject if the name is already used by another project.
7. If `<path>/stew.toml` did not exist, write it from the template.
8. Add the path to `.stew/projects.toml` (sorted, atomic write).
   If this fails, remove the `stew.toml` written in step 7. A pre-existing `stew.toml` is never removed.
9. Print `using existing <path>/stew.toml` when step 4 applied, then `added <name> (<path>)`.

- Rejections exit 1 and change nothing.

### `stew list [--porcelain]`

Prints every registered project, sorted by name, in a bordered table like the Summary.
Dependencies are sorted and comma-separated, or `-` when there are none.

```
┌─────────┬──────────────┬──────────────┐
│ project │ path         │ dependencies │
├─────────┼──────────────┼──────────────┤
│ api     │ services/api │ core         │
│ core    │ libs/core    │ -            │
│ web     │ apps/web     │ api, core    │
└─────────┴──────────────┴──────────────┘
```

- An empty workspace prints `no projects (add one with: stew add <path>)`.
- `--porcelain` prints one line per project for scripts: `<name>\t<path>\t<dep>,<dep>`.
  No header, no borders. An empty workspace prints nothing.

### `stew remove <name>... [--clean]`

1. Find the root and load the workspace. An invalid workspace is an error (exit 2).
2. Reject before changing anything if:
   - a name is unknown: `unknown project "<name>"` (exit 2);
   - a project that is not removed depends on a removed one (exit 1). One line per blocked project:
     `stew: cannot remove core: needed by api, web`.
     Removing a project together with all its dependents is allowed.
3. Remove the paths from `.stew/projects.toml` (sorted, atomic write).
   If this fails: `unregister <paths>: <error>` (exit 1), and nothing is changed.
4. Print `removed <name> (<path>)` per project, sorted by name.
5. With `--clean`, delete each project's `stew.toml` and print `deleted <path>/stew.toml`.
   The registry is written first, so a registered path never loses its manifest.
   A failed delete prints `stew: delete <path>/stew.toml: <error>` to stderr and exits 1.
   The project stays unregistered.

- Repeated names are removed once.
- A project whose `stew.toml` is missing cannot be removed by name, because the workspace does not load.
  Edit `.stew/projects.toml` by hand.

### `stew setup | build | ci`

Phases are cumulative.

| Command                | Dependencies (transitive) get | Named projects get         |
| ---------------------- | ----------------------------- | -------------------------- |
| `stew setup [name...]` | setup                         | setup                      |
| `stew build [name...]` | setup → build                 | setup → build              |
| `stew ci [name...]`    | setup → build                 | setup → build → ci.<level> |

- Selection: the named projects plus all their transitive dependencies. No names selects all projects.
- With no names, every project is "named", so `stew ci` runs CI for every project.
- An unknown name is an error (exit 2) before anything runs.
- Order: topological by dependencies. Ties are broken by name, so the order is identical on every run.
- Execution is sequential and project by project.
  A project runs all its phases before the next project starts.

### CI Levels

- `--level` defaults to `full`.
- Resolution chains: `pre-commit → quick → full` and `pre-push → full`.
  A requested level that the project does not define falls back to the next level in its chain.
- `full` always exists, so resolution always succeeds.
- When a fallback is used, the phase header and summary show it (see Output).

## Phase Algorithm

Applies to `setup` and `build`. A CI level is the same with `verify` treated as `""`.
Stew never invokes `sh -c ""`.

| `run` | `verify` | Steps                   | Result                      |
| ----- | -------- | ----------------------- | --------------------------- |
| `""`  | `""`     | nothing                 | `skip`                      |
| set   | `""`     | run `run`               | exit 0: `done`; else `fail` |
| set   | set      | run `verify`            | exit 0: `skip`              |
|       |          | else run `run`          | non-zero: `fail`            |
|       |          | then run `verify` again | exit 0: `done`; else `fail` |
| `""`  | set      | run `verify` once       | exit 0: `skip`; else `fail` |

- The last row makes `verify` with an empty `run` an assertion.
- For CI levels, `done` is reported as `pass`. A CI level with `run = ""` reports `skip`, never `pass`.

### Status Words

| Phase            | Words                                            |
| ---------------- | ------------------------------------------------ |
| `setup`, `build` | `done`, `skip`, `fail`, `blocked`, `interrupted` |
| `ci.<level>`     | `pass`, `skip`, `fail`, `blocked`, `interrupted` |

### Command Execution

- Each command runs with cwd set to the project directory.
  With `workspace_wrapper` and `project_wrapper` both `""`, it runs as `sh -c <cmd>`.
  Otherwise it runs through the wrappers (see Wrapper Contract).
- The environment is inherited, plus the variables below. They replace inherited values of the same name,
  such as those of an outer stew run whose command runs stew.

  | Variable       | Value                                                            |
  | -------------- | ---------------------------------------------------------------- |
  | `STEW_RUN_ID`  | run ID, as in `.stew/runs/<run-id>/`                             |
  | `STEW_ROOT`    | absolute workspace root                                          |
  | `STEW_PROJECT` | project name                                                     |
  | `STEW_PHASE`   | phase that runs, after CI fallback: `setup`, `build`, `ci.quick` |
  | `STEW_TAG`     | `<project>.<phase>`, e.g. `core.ci.quick`                        |

  `STEW_TAG` is a label, not something to parse: project names may contain `.`.
- Nothing streams live. Each command's output goes to two places:
  - stdout to the phase's `.stdout` log file, stderr to its `.stderr` log file;
  - both, in write order, to one in-memory buffer per phase, which is replayed on failure.
- stdin is `/dev/null`. Output is hidden while a command runs, so an interactive prompt would hang unseen.
- Each command runs in its own session and process group, with no controlling terminal.
  Stew can signal the command and everything it started, and a command that opens `/dev/tty` fails at once
  instead of stopping in the background.
- When a command exits, stew stops what it left running in its process group: SIGTERM, then SIGKILL after 5 s.
  A step must not leave background processes behind. Stop a test server inside the step that started it.
- A process that leaves the group (its own session via `setsid`, a daemon, or its own group via `setpgid`)
  is not stopped. It must leave before the command exits; otherwise the SIGTERM can reach it first.
- On Linux stew is the child subreaper of its commands, so it reaps the leftovers it stopped itself.
  A step's end never waits on a PID 1 that does not reap, as in some containers.
  Elsewhere the system's PID 1 reaps them. A process that left the group has stew as its parent until stew exits.

#### Wrapper Contract

A wrapper is shell code. `{{STEW_STEP}}` marks where the command goes.
stew replaces it with the path of an executable that runs the step's command, through any inner wrapper.
That is the whole contract.

There are two levels. `workspace_wrapper` wraps every command. A project's `project_wrapper` wraps that
project's commands, inside the workspace wrapper. A level whose wrapper is `""` is dropped.

- A wrapper provides the toolchain. It is not a build step.
- stew knows no environment tool and special-cases none.
- The user writes where the command goes. stew appends nothing and adds no options, variables, or shell settings
  to the wrapper. Anything like quieter logs or `set -e` is the user's to write.
- A wrapper that does not run the command exactly once fails the phase. A command never passes without running.

Examples of the shape (not a supported-tools list):

| Shape                                 | Wrapper                                  |
| ------------------------------------- | ---------------------------------------- |
| A tool that runs a command in its env | `<tool> exec . {{STEW_STEP}}`            |
| A tool that takes a command string    | `<tool> --run {{STEW_STEP}}`             |
| Source a script, then run             | `. ./env.sh && {{STEW_STEP}}`            |
| Variable expansion                    | `<tool> exec "$STEW_ROOT" {{STEW_STEP}}` |

- The path needs no shell quoting (see Step Directory). The placeholder works bare, inside quotes,
  and inside a string that a tool parses again as shell code.
- The path starts with `/`, so a tool that parses options never reads it as an option.
- The wrapper runs with cwd set to the project directory, like commands.
- It is shell code: `$STEW_*`, `$HOME`, and other variables expand.
- It applies to every step of every phase: `setup`, `build`, `ci.*`, `run` and `verify`.
- The wrapper also runs `setup`'s commands, so it must work before `setup` has run.
- It keeps normal `sh` meaning. `<tool> ; {{STEW_STEP}}` runs the command even when `<tool>` fails.
  Use `&&`, or `set -e;`, to stop on failure.

#### Wrapped Execution

With at least one wrapper, stew writes executables for each step into the step directory.
For a project with both wrappers:

| File                     | Content                                                                                  |
| ------------------------ | ---------------------------------------------------------------------------------------- |
| `<project>-<phase>.1`    | `#!/bin/sh`, then `project_wrapper` with `{{STEW_STEP}}` replaced by the `.step` path    |
| `<project>-<phase>.step` | `#!/bin/sh`, then: count one reach, export every `STEW_*`, run `sh -c <cmd>`,            |
|                          | write its exit status to `<project>-<phase>.status`, and exit with that status           |

Then it runs `sh -c <workspace_wrapper with {{STEW_STEP}} replaced by the .1 path>`.
With one wrapper, there is no `.1` file. stew runs `sh -c <that wrapper>`, with `{{STEW_STEP}}` replaced by
the `.step` path.

- `<project>-<phase>` is the phase's key, as in the log file names.
- `STEW_*` are in the environment of the whole chain, so a wrapper can read them.
- The step script sets them again, so no wrapper can change what the command sees.
  Their values are shell-quoted inside the script, so any value is safe.
- The command runs as `sh -c <cmd>`, the same as without a wrapper.
- If the step script cannot count the reach, it exits 125 without running the command.
  If it cannot write the status, it exits 125.
- The scripts are mode `0700`. stdin `/dev/null`, the process group, and no controlling terminal apply as above.
  The kill ladder applies as in Interrupt. Signals reach the wrappers and everything they start in the same
  process group.
- A wrapped command killed by a signal shows as `exit 128+N`, e.g. `exit 143` for SIGTERM, because the step
  script's `sh` reports it that way.
- The wrapper strings are substituted and run as they are. stew adds nothing to them.

#### Step Directory

- Created at run start, before the run directory, only when a project in the run has a workspace or project
  wrapper. It is a new `stew-*` directory in `$TMPDIR` (made absolute), or in `/tmp` when `TMPDIR` is unset.
- Its full path must match `[A-Za-z0-9/._-]+`, so no file in it ever needs quoting, however many times a tool
  parses it. Otherwise the run exits 1 before any phase:
  `cannot create step directory: <path> needs shell quoting; set TMPDIR to a path of letters, digits, and /._-`.
- It sits outside the workspace, so a workspace path with spaces or quotes never reaches a wrapper.
- Any failure to create it exits 1 with `cannot create step directory: <error>`, before any run directory exists.
- The temp directory must allow executing files. On a `noexec` mount, every wrapped step fails with
  `wrapper did not run the command (exit 126)`. Set `TMPDIR` to a plain path that allows executing files.
- It is removed when the run ends, including after an interrupt. A killed stew leaves it for the OS to clean up.

#### Reach Count

The step script appends one line to `<project>-<phase>.reach` in the step directory each time it runs.
When the command finishes, the step script writes the command's exit status to `<project>-<phase>.status`.

- stew removes the step's `.reach` and `.status` files before every step, and all the step's files after it.
- After a wrapped step exits, stew counts the lines and reads the status.
  In a cause, `<exit N / signal S / cannot start: …>` is the outermost wrapper's own result.

  | Reaches | Status | Step result                                                                              |
  | ------- | ------ | ---------------------------------------------------------------------------------------- |
  | 1       | yes    | The command's own exit status. The wrapper's own exit status is ignored.                 |
  | 1       | no     | `fail`, cause `wrapper exited before the command finished (<exit N / signal S / …>)`.    |
  | 0       | —      | `fail`, cause `wrapper did not run the command (<exit N / signal S / cannot start: …>)`. |
  | N ≥ 2   | —      | `fail`, cause `wrapper ran the command N times (<exit N / signal S / cannot start: …>)`. |

- The command's status wins both ways. `{{STEW_STEP}} || true` and `{{STEW_STEP}}; echo post` fail when the command
  fails. `{{STEW_STEP}}; false` passes when the command passes.
- A wrapper failure (a reach count other than 1, or no status) fails the phase for every step, including a pre-run
  `verify`. A wrapper failure is not "not done yet".
- Precedence: an interrupt gives `interrupted`, and a log error gives `log error: …`, before the reach check.
  An interrupt may stop the step script before the command finishes. The cause is then
  `wrapper exited before the command finished (signal S)`.
- Failing to write, read, or remove a step's files is a log error (see Log errors in `.stew/runs/`).

#### Guards

| Guard                           | Catches                                                                            |
| ------------------------------- | ---------------------------------------------------------------------------------- |
| Placeholder count at load       | A wrapper that never mentions the command, or mentions it twice.                   |
| `sh -n` at load                 | Syntax errors: unbalanced quotes, a dangling `if`, etc.                            |
| Plain step path                 | Quoting that works on one machine and breaks on another.                           |
| Reach count                     | A wrapper that does not run the command exactly once: `echo {{STEW_STEP}}`,        |
|                                 | `false && {{STEW_STEP}}`, a placeholder in a comment, a missing tool, a loop.      |
| Status file                     | A wrapper that hides the command's failure: `{{STEW_STEP}} \|\| true`,             |
|                                 | `{{STEW_STEP}}; echo post`. One that exits before the command finishes: `… &`.     |
| `STEW_*` set in the step script | A wrapper, or anything it loads, that sets `STEW_*`.                               |

None of the guards change what the wrapper does. They only check the input or the result.

#### Wrapper Output

- Step markers are `--- stew: <step>: <cmd>`. The wrapper is not repeated per step.
- Wrapper output goes into the step's logs and replay like any other output. stew does not filter or silence it.

### Failure Handling

- A project stops at its first phase that ends `fail`, `blocked`, or `interrupted`.
  Its later phases do not run, print no line, and show `-` in the summary.
- A project whose direct dependency failed or was blocked is `blocked` at its first phase (`setup`).
  Blocking spreads to transitive dependents one hop at a time.
- Independent projects keep running.
- After the run, the summary is printed and the exit code is 1.

### Interrupt

- On Ctrl-C stew catches SIGINT and forwards it to the running command's process group.
  Stew does not exit immediately. It waits for the command to exit and starts nothing new.
  It then stops the command's leftovers as after any command, for up to 5 s. Only a second stop signal cuts that short.
- On SIGTERM or SIGHUP (CI cancel, `timeout`, closed terminal) stew forwards that signal to the group,
  then sends SIGKILL after 5 s if the command is still running. It starts nothing new.
- A second stop signal (Ctrl-C, SIGTERM, or SIGHUP) while stew waits sends SIGKILL to the command's group at once.
- The phase in progress reports `interrupted`, followed by its content area (see Output).
- Stew then prints the summary and exits 128 + the first signal's number: 130 (SIGINT), 143 (SIGTERM), 129 (SIGHUP).

## Output

All output goes to stdout, except configuration and usage errors, which go to stderr.

### Phase Lines

One line per phase, and nothing else on it: `==> <project>: <phase> ... <status>`,
plus ` (<duration>)` when the status is `done`, `pass`, `skip`, or `fail`.
Exit codes, failing steps, and command output go in the content area below a `fail` or `interrupted` line.

Example 1, success: `stew ci api --level pre-commit`, where `api` depends on `core`.

```
==> core: setup ... skip (0.1s)
==> core: build ... skip (0.0s)
==> api: setup ... done (12.4s)
==> api: build ... done (1m3s)
==> api: ci.pre-commit -> ci.quick ... pass (8.2s)
```

Example 2, failure: `stew build`, where `app` depends on `api` and `backend`,
and `api` and `backend` depend on `core`.

```
==> core: setup ... fail (2.7s)
--- stew: run: npm ci
<captured output of core's setup run>
(exit 2)
==> api: setup ... blocked by core
==> backend: setup ... blocked by core
==> app: setup ... blocked by api, backend
```

- **Fallback.** A CI level that falls back shows `<requested> -> <used>`, e.g. `ci.pre-commit -> ci.full`.
- **Success.** `done`, `skip`, and `pass` print only the line. Command output is not shown.
- **Duration.** Wall time of the whole phase, from its first step to its last, for profiling.
  - Under one minute: seconds with one decimal, e.g. `(0.0s)`, `(10.5s)`. Rounded to the nearest 0.1 s.
  - One minute or more: rounded to the nearest second, then `<m>m<s>s` or `<h>h<m>m<s>s`,
    with zero units kept after the largest: `(1m2s)`, `(1m0s)`, `(1h5m12s)`, `(1h0m0s)`.
  - Rounding happens first: 59.96 s is `(1m0s)`, not `(60.0s)`.
  - A `skip` that ran no command still shows its duration, e.g. `(0.0s)`.
  - `blocked` and `interrupted` lines have no duration.
- **Content area.** Printed only after a `fail` or `interrupted` line:
  - For each replayed step, a marker line `--- stew: <step>: <cmd>`, then that step's output verbatim.
    `<step>` is `verify`, `run`, or `verify after run`, matching the log file markers.
  - Replayed steps: every step of the phase except a pre-run `verify` that failed.
    That failure only means "not done yet", so its output is noise.
  - A newline is added if the output does not end in one.
  - Last line: `(exit <n>)`, `(signal <name>)`, `(cannot start: <error>)`, or `(log error: <error>)`.
- **Blocked.** A blocked project prints one line, for its first phase: `==> <project>: setup ... blocked by <names>`,
  at the point in the order where it would have run. No animation. No content area.
  - `<names>` lists only **direct** dependencies that failed or were blocked, in execution order.
    Following `blocked by` names one hop at a time leads back to the failed project.
  - `blocked by` names dependencies only. A project is never blocked by itself.
- **Stopped projects.** After a `fail`, `blocked`, or `interrupted` line, that project prints nothing more.

### Progress Animation

- **Terminal (stdout is a TTY).** While a phase runs, the line ends in dots that cycle `.` → `..` → `...` → `.`,
  one frame every 300 ms, redrawn in place with `\r` and clear-to-end-of-line.
  When the phase ends, the line is redrawn as `... <status> (<duration>)` followed by a newline.
  (`interrupted` has no duration, in both modes.)
- **Not a terminal (CI logs, pipes).** Stew writes `==> <project>: <phase> ... ` when the phase starts,
  and `<status> (<duration>)` plus a newline when it ends. No escape codes.
  Long phases still show which phase is running.

### Summary

A bordered table prints after every `setup`, `build`, or `ci` run. Rows follow execution order.

Example: `stew ci core api web --level pre-commit`, where `api` depends on `core` and `lib`,
`web` depends on `api`, and `api`'s build fails.

```
┌─────────┬─────────┬───────┬───────────────┐
│ project │ setup   │ build │ ci.pre-commit │
├─────────┼─────────┼───────┼───────────────┤
│ core    │ skip    │ done  │ pass (quick)  │
│ lib     │ skip    │ done  │ -             │
│ api     │ done    │ fail  │ -             │
│ web     │ blocked │ -     │ -             │
└─────────┴─────────┴───────┴───────────────┘
total: 1m15s
logs: .stew/runs/20260925T043601Z-3f9a
```

- Columns are the phases the command runs. The CI column is named after the requested level.
- Cells hold the status word, or `-` for a phase that did not run: not selected, after its project stopped,
  or after an interrupt.
- A dependency-only project under `stew ci` shows `-` in the CI column.
- A CI cell that used a fallback names the level used, e.g. `pass (quick)`.
- Borders use Unicode box-drawing characters. Column width is the widest cell plus one space of padding per side.
- Two lines follow the table:
  - `total: <duration>`: wall time from run directory creation to the end of the last phase,
    in the phase duration format. Printed on every run, including failed and interrupted ones.
  - `logs: .stew/runs/<run-id>` (see Run Logs).

## Exit Codes

| Code | Meaning                                                                                              |
| ---- | ---------------------------------------------------------------------------------------------------- |
| 0    | Success.                                                                                             |
| 1    | A phase failed or was blocked; `init`/`add`/`remove`/`git install` rejected; a git command failed; `runs show` found no match or an unreadable run; `runs list` could not read `.stew/runs/`; `runs prune` could not read `.stew/runs/` or delete a run. |
| 2    | Invalid CLI usage or invalid workspace configuration.                                                |
| 130  | Interrupted by Ctrl-C (SIGINT). SIGTERM exits 143 and SIGHUP exits 129.                              |

## `stew runs show <run-id> [<project-phase-regex>...] [--porcelain] [--no-pager]`

Shows a past or running run from its logs.

- Root discovery only. The workspace is not loaded or validated, so logs stay readable while a `stew.toml` is broken.
- `<run-id>` is an exact run ID or `latest`, the ID that sorts last in `.stew/runs/`.
- Selection:
  - Each regex is Go RE2 and must match the whole phase key `<project>-<phase>`, e.g. `api-build`, `api-.*`,
    `.*-ci\..*`. `api` does not match `webapi-build`.
  - `<phase>` is the level used after fallback (`ci.quick`), as in the log file names.
  - Several regexes are OR-ed. No regex selects every phase of the run.
- Errors:

  | Case                                  | Message                    | Exit |
  | ------------------------------------- | -------------------------- | ---- |
  | Unknown run ID, or `latest` with none | `unknown run "<id>"`       | 2    |
  | Invalid regex                         | the regex error            | 2    |
  | Regexes given, no phase matches       | `no phase matches`         | 1    |
  | No `run.json`                         | `run <id> has no run.json` | 1    |
  | `run.json` unreadable or invalid      | `read run <id>: <error>`   | 1    |

  Runs recorded before `run.json` existed have no fallback reader.

### Default Output

The live plain format, without animation, plus a header line and one `wrapper` line per wrapper the run had.
Example: `stew runs show latest` for the run in Run Manifest, where `api` depends on `core` and `web` on `api`.

```
run 20260925T043601Z-3f9a: stew ci --level pre-commit
wrapper: tool exec . {{STEW_STEP}}
wrapper api: other-tool run {{STEW_STEP}}
==> core: setup ... skip (0.1s)
--- stew: verify: test -d node_modules
==> core: build ... skip (0.0s)
--- stew: verify: test -f dist/index.js
==> core: ci.pre-commit -> ci.quick ... pass (8.2s)
--- stew: run: npm run lint
lint ok
==> api: setup ... skip (0.1s)
--- stew: verify: test -d node_modules
==> api: build ... fail (1m3s)
--- stew: verify: test -f dist/index.js
--- stew: run: npm run build

> api@1.0.0 build
> tsc

src/db.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.
src/api.ts(40,1): error TS2304: Cannot find name 'handler'.
done in 4.1s
--- stew: verify after run: test -f dist/index.js
(exit 1)
==> web: setup ... blocked by api
┌─────────┬─────────┬───────┬───────────────┐
│ project │ setup   │ build │ ci.pre-commit │
├─────────┼─────────┼───────┼───────────────┤
│ core    │ skip    │ skip  │ pass (quick)  │
│ api     │ skip    │ fail  │ -             │
│ web     │ blocked │ -     │ -             │
└─────────┴─────────┴───────┴───────────────┘
total: 1m15s
logs: .stew/runs/20260925T043601Z-3f9a
```

- **Header.** `run <id>: stew <argv>`, with each argument shell-quoted when needed.
  An argument with a control character or invalid UTF-8 uses `$'…'` quoting with `\t`, `\n`, `\r`, and `\xHH` escapes.
- **Wrappers.** Right after the header, one line per non-empty wrapper, as in the example above:
  - `wrapper: <workspace_wrapper>` first, only when `workspace_wrapper` is not `""`.
  - Then `wrapper <project>: <project_wrapper>` for each project in `project_wrapper`, in `projects` order.
  - A wrapper prints verbatim, unless it has a control character (e.g. a newline) or invalid UTF-8.
    Then it uses the header's `$'…'` quoting, so each wrapper stays on one line.
- **Order.** Matched phases in execution order, from `run.json`.
- **Phase line.** Identical to the live line, including `<requested> -> <used>` and `blocked by <names>`.
- **Body.** Every phase that ran a command prints its whole `.log` verbatim, markers included.
  - This includes `done`, `skip`, and `pass` phases, and a failed pre-run `verify`.
  - A newline is added if the log does not end in one.
- **Last line.** `(<cause>)` after `fail` and `interrupted`, as in the live output.
- **No command.** `blocked` and a `skip` with both commands `""` print the line only.
- **Summary.** The summary table, `total:`, and `logs:` lines print only when no regex is given.
- **Unfinished run** (still running, or stew was killed):
  - A phase without an entry prints `==> <project>: <phase> ... unfinished`, then its partial `.log`.
    No duration, no last line. Such phases print after all phases in `run.json`.
  - Its project and used phase come from the `.log` name. A used `ci.*` level maps to the CI column in `columns`,
    which gives the requested name for the phase line and summary cell.
  - Its summary cell is `unfinished`. The `total:` line is `total: unfinished`.
  - Stew cannot tell a running run from a killed one, so both use the same word.
- **Stream order.** stdout and stderr share one body in arrival order. Order within one stream is exact.
  Across streams it is approximate, as in the live replay: tools often buffer stdout when it is a pipe,
  and the two streams are copied on separate goroutines. The `.stdout` and `.stderr` files keep them apart.

### `--porcelain`

One line per matched phase, in execution order, and nothing else:

```
<project>\t<phase>\t<status>\t<duration-ms>\t<log-path>
```

- `<phase>` is the level used. `<status>` is a status word or `unfinished`.
- `<duration-ms>` is `-` when there is no duration.
- `<log-path>` is root-relative, e.g. `.stew/runs/<id>/api-build.log`, or `-` when the phase has no log.
- Never paged.

### Paging

- The default output goes through a pager only when stdout is a terminal (the reporter's TTY check).
- Pager: the first non-empty value of `STEW_PAGER`, `PAGER`, then `less`, run as `sh -c "<pager>"`.
- If `LESS` is unset, the pager gets `LESS=FRX`: quit if the output fits one screen, pass escape codes,
  keep the output on screen after quitting.
- `--no-pager`, `STEW_PAGER=cat`, or `PAGER=cat` disables paging.
- If the pager cannot run (`sh` fails to start, or exits 126 or 127), stew prints the whole page directly.
- While the pager runs, stew catches and drops SIGINT, so Ctrl-C reaches only the pager.
- Quitting the pager early is not an error: stew ignores the broken pipe, waits for the pager, and exits 0.

## `stew runs list [--porcelain] [--no-pager]`

Lists every run, newest first.

- Root discovery only, as in `stew runs show`.
- Runs: every directory in `.stew/runs/` whose name is a run ID. Other entries are ignored.

```
┌─────────────────────┬───────────────────────┬────────────┬───────┬────────────────────────────┐
│ started             │ run                   │ result     │ total │ command                    │
├─────────────────────┼───────────────────────┼────────────┼───────┼────────────────────────────┤
│ 2026-09-25 12:12:00 │ 20260925T051200Z-a1c2 │ unfinished │ -     │ stew build                 │
│ 2026-09-25 11:36:01 │ 20260925T043601Z-3f9a │ fail       │ 1m15s │ stew ci --level pre-commit │
│ 2026-09-25 11:10:10 │ 20260925T041010Z-77e0 │ ok         │ 8.4s  │ stew ci core               │
│ 2026-09-25 05:00:01 │ 20260924T220001Z-0b3d │ unreadable │ -     │ -                          │
└─────────────────────┴───────────────────────┴────────────┴───────┴────────────────────────────┘
```

- **Started.** The run ID's UTC time in local time (`$TZ`, else the system zone), as `YYYY-MM-DD hh:mm:ss`.
  `-` when the ID's time is not a valid date.
- **Result.** From `run.json`, first match wins:

  | Result        | When                                                             |
  | ------------- | ---------------------------------------------------------------- |
  | `unreadable`  | `run.json` is missing, invalid, or cannot be read                |
  | `unfinished`  | no `total_ms`: the run is still going or stew was killed         |
  | `interrupted` | a phase is `interrupted`                                         |
  | `fail`        | a phase is `fail` or `blocked`                                   |
  | `ok`          | otherwise                                                        |

- **Total.** `total_ms` in the phase duration format, or `-`.
- **Command.** `stew <argv>`, quoted as in the `stew runs show` header, or `-` for an unreadable run.
- An unreadable run still gets a row and exits 0. `stew runs show <id>` names the error.
- No runs prints `no runs`.
- `.stew/runs/` cannot be read: `read runs: <error>`, exit 1.
- Paged as in `stew runs show` (see Paging).

### `--porcelain`

One line per run, in the same order, and nothing else. No runs prints nothing. Never paged.

```
<started>\t<run-id>\t<result>\t<total-ms>\t<command>
```

- `<started>` is RFC 3339 local time with offset, e.g. `2026-09-25T11:36:01+07:00`, or `-`.
- `<total-ms>` is `-` when there is no total.
- `<command>` is last. Quoting keeps it on one line with no tabs.

## `stew runs prune [--keep-since <time>] [--keep-last-n <n>]`

Deletes the runs that no keep rule keeps. A run in progress is never deleted.

- Root discovery only, as in `stew runs show`.
- Runs: every directory in `.stew/runs/` whose name is a run ID. Other entries, including symlinks, are never touched.
- A run is kept when at least one given rule keeps it:
  - `--keep-since <time>`: the run ID's start time is at or after `<time>`;
  - `--keep-last-n <n>`: the run is one of the `n` newest by ID.
- At least one rule is required. With neither: `at least one of --keep-since or --keep-last-n is required`, exit 2.
- `<time>` is one of:
  - a duration before now, as whole numbers with units `w` (7 days), `d` (24 h), `h`, `m`, `s`: `36h`, `7d`, `1w2d`;
  - a local date `YYYY-MM-DD` (midnight), or a local date and time `YYYY-MM-DD hh:mm:ss`;
  - an RFC 3339 time, e.g. `2026-09-01T15:04:05+07:00`.

  Anything else: `invalid --keep-since "<value>": <reason>`, exit 2.
- `<n>` is 0 or more. A negative `n`: `invalid --keep-last-n <n>: must be 0 or more`, exit 2.
- Age is the start time. A run that started before `<time>` is deleted even if it ended after it.
- A run ID whose time is not a valid date is never kept by `--keep-since`.
- `run.json` is not read. Finished, `unfinished`, and `unreadable` runs are all deleted by the same rules.
- Runs in progress hold their lock (see Run Logs) and are kept. Prune deletes a run while holding its lock.
  A run whose stew crashed or was killed, even with SIGKILL, is unlocked and deleted like any other.
- One line per run the rules do not keep, oldest first, and nothing else. Never paged.

  ```
  pruned 20260924T220001Z-0b3d
  kept 20260925T051200Z-a1c2: running
  ```

- A run another process removed meanwhile is skipped silently.
- A failed delete prints `stew: prune <id>: <error>` to stderr. Prune goes on with the next run and exits 1.
  A partly deleted run is listed as `unreadable` by `stew runs list`.
- `.stew/runs/` cannot be read: `read runs: <error>`, exit 1. A missing `.stew/runs/` prunes nothing.

## `stew git install <hook>`

`<hook>` is `pre-commit` or `pre-push`. The hook runs the CI level of the same name.

1. Find the root. The workspace is not validated.
2. Find the hooks directory with `git rev-parse --git-path hooks`, run from the root.
   This respects `core.hooksPath` and worktrees. Not being in a git repo is an error.
3. Compute the root's path relative to `git rev-parse --show-toplevel`.
4. Reject if the hook file already exists.
5. Write the hook with mode `0755`:

   ```sh
   #!/bin/sh
   # installed by stew
   cd "$(git rev-parse --show-toplevel)/<rel>" && exec stew ci --level <hook>
   ```

   `<rel>` is the path from step 3. When it is `.`, the line is `cd "$(git rev-parse --show-toplevel)"`.

- Only `pre-commit` and `pre-push` are supported. Any other hook name is an error listing the supported hooks.
- The hook calls `stew` from `PATH`.
- The hook checks the working tree, not the staged snapshot. Unstaged edits can affect the result.

## Code Layout

```
cmd/stew/            main: cobra commands, error → exit code mapping
  runs.go            `runs show` and `runs list`: run lookup, regex selection, result, porcelain lines
  prune.go           `runs prune`: keep flags, --keep-since parsing, output
  pager.go           pager choice, LESS default, direct-output fallback, broken-pipe handling
internal/workspace/
  root.go            Find(cwd) → root
  registry.go        load/save .stew/projects.toml
  config.go          load .stew/config.toml: strict parse, workspace_wrapper
  wrapper.go         wrapper checks: placeholder count, `sh -n` syntax check
  project.go         stew.toml schema, strict parse, required-key checks, project_wrapper, add template
  graph.go           name index, dependency validation, cycle detection, Select(names) → plan
internal/runner/     plan execution: phase algorithm, level resolution, blocked propagation, results
  steps.go           {{STEW_STEP}}, the step directory, step scripts, reach counts and statuses
  orphans_*.go       Linux child subreaper; a no-op elsewhere
internal/report/     phase lines, progress animation, failure replay, summary table, `runs show` page
internal/runlog/     run ID, run directory and its lock, per-phase log files and step markers, run.json save/load
internal/githook/    hooks-dir lookup via git, hook install
```

- `workspace` knows nothing about running commands. Its only process is `sh -n` for the wrapper checks.
- `runner` knows nothing about TOML or terminals. It receives a plan: ordered projects, each with its phases and
  commands. It emits events to a `Reporter` interface: phase start,
  phase end (status, cause, duration, captured output), project blocked, and the final results.
- `runner` measures durations with an injected clock (real: `time.Now`, monotonic), so tests control them.
  `report` formats them.
- `runner` runs commands through an `Executor` interface: `Run(ctx, dir, env, argv []string, stdout, stderr) Result`.
  `runner` builds the argv: `sh -c <cmd>`, or `sh -c <outermost wrapper>` from its `Steps`.
  `Result` holds the exit code, the signal, or the start error, and for a wrapped step the reach count and whether
  the command finished. After a single finished reach, the exit code is the command's.
  The real implementation runs argv with `os/exec` and stdin `/dev/null`.
  Tests use a fake that records calls and writes scripted output.
- `runner` prepares and checks wrapped steps through a `Steps` interface: `Prepare(key, wrappers, env, cmd)` writes
  the step's files and returns the argv, and `Collect(key)` returns the reach count and, when the command
  finished, its exit status, then removes the files.
  `runner.StepDir` implements it. `cmd/stew` creates one per run only when a project in the run has a workspace
  or project wrapper, and removes it when the run ends.
- `runner` builds each writer as a tee: log file plus the phase's combined replay buffer.
  The combined buffer is guarded by a mutex, because `os/exec` copies stdout and stderr on separate goroutines.
- `runlog` gives `runner` a writer pair per phase. `runner` does not know log file paths.
  Each writer also writes to the phase's `.log`, so `runner` is unaware of the combined file.
- `runner` records results through a `Recorder` interface: `PhaseEnd(project, ph, out) error` and
  `Blocked(project, ph, by) error`. Each is called before the matching `Reporter` event,
  so a `PhaseEnd` save error can still turn the phase into `fail`. `runlog` implements it by saving `run.json`.
- `runlog` also provides `Load(stewDir, id)` and `Latest(stewDir)`, and finds the unfinished phase from `.log` names.
- `runlog.Retention` picks the runs to delete from their IDs alone. `runlog.Delete` removes one run while holding
  its lock, and returns `ErrRunning` for a run in progress.
- `report` builds the `runs show` page from a loaded run. It reuses the phase-line, duration, and summary helpers,
  and rebuilds the summary from `run.json`.
- The pager's TTY check and environment are injected, so tests can drive it.
- A log write error cancels the command's context. The real `Executor` then signals the command's process group
  (SIGTERM, SIGKILL after 5 s). The phase then fails like any other.
- `report` has two `Reporter` implementations: TTY (animated) and plain. `cmd/stew` picks one by checking whether
  stdout is a terminal. The animation takes its ticker as a parameter, so tests can drive frames.

### Dependencies

| Need      | Module                                       | Version | Reason                                                         |
| --------- | -------------------------------------------- | ------- | -------------------------------------------------------------- |
| TOML      | `github.com/BurntSushi/toml`                 | v1.6.0  | `IsDefined`: missing key vs `""`. `Undecoded()`: unknown keys. |
| CLI       | `github.com/spf13/cobra`                     | v1.10.2 | Flags after args, nested `git install`, generated help.        |
| CLI tests | `github.com/rogpeppe/go-internal/testscript` | v1.16.0 | Script-driven tests of the real binary.                        |

- The `stew.toml` template is emitted from a fixed text template.
- `projects.toml` is emitted by hand as one sorted list.
- TTY detection uses `os.Stdout.Stat()` and `os.ModeCharDevice`. No extra module.

## Testing

### Unit: `workspace`

- Parse and validate, table-driven:
  - each required section and key missing;
  - `""` values accepted;
  - unknown key, unknown section, unknown CI level, `verify` inside `[ci.*]`;
  - invalid name, duplicate name, unknown dependency, self-dependency;
  - cycle, with the cycle path in the message.
- Registry: round trip, sorting, invalid and duplicate paths.
- Selection: transitive dependencies, deterministic topological order with name tie-break, unknown name.
- `config.toml`: missing file; missing key; unknown key; wrong type; `""` accepted.
- Placeholder count: 0 and 2 are rejected with file, key, and count. A placeholder inside quotes counts.
- Syntax check: an unbalanced quote and a dangling `if` are rejected with file and key.
- Accepted: `tool exec . {{STEW_STEP}}`, `. ./env.sh && {{STEW_STEP}}`, `tool --run '{{STEW_STEP}}'`,
  a multi-line wrapper, and one ending in a newline.
- `project_wrapper`: missing; wrong type; no placeholder; syntax error; each rejected with `stew.toml` and the key.
  `""` and `tool run {{STEW_STEP}}` are accepted. The template parses with `project_wrapper = ""`.

### Unit: plan

- A project gets the workspace wrapper, then its own. Empty ones are dropped.

### Unit: `runner`

- Every row of the phase algorithm table, including a failing `verify` after `run` and a failing assertion `verify`.
- No executor call is ever made with an empty command.
- Phase duration spans all steps of the phase (fake clock).
- Level resolution for every combination of defined levels.
- Cumulative phases per command.
- Blocked propagation: transitive dependents blocked at `setup` only, independent projects still run.
- Captured output passed on failure excludes a failed pre-run `verify` and includes every later step.
- Every step of a phase gets the same `STEW_*` variables; `STEW_PHASE` is the phase used after CI fallback.
- Blocked names list only direct dependencies that failed or were blocked, in execution order.
  Case: `app -> {api, backend} -> core` with `core` failing gives `api`/`backend` blocked by `core`,
  and `app` blocked by `api, backend`.
- After `fail`, `blocked`, or `interrupted`, that project's later phases do not run, emit no line, and show `-`.
- Log errors:
  - run directory creation failure: exit 1, no executor call;
  - log file creation failure: the phase never calls the executor and reports `fail` with `(log error: ...)`;
  - log write failure mid-command: the command's context is cancelled and the phase reports `fail`;
  - in both phase cases, dependents are `blocked` and independent projects still run.
- Final results and exit status.
- `Recorder`:
  - called in execution order for phase end and blocked, and not called for phases after an interrupt;
  - a `PhaseEnd` error turns `done`, `skip`, or `pass` into `fail` with `(log error: ...)`;
    dependents are `blocked` and independent projects still run;
  - a `PhaseEnd` error never overrides `fail` or `interrupted`.
- Wrappers:
  - no wrapper: the argv is exactly `sh -c <cmd>`, and no step file is written;
  - with wrappers: `Prepare` gets the job's wrappers in order, outermost first;
  - with two wrappers: the argv is `sh -c <outer wrapper with the .1 path>`, and the `.1` script is the inner wrapper
    with the `.step` path; the wrapper text is otherwise unchanged, and both scripts are mode `0700`;
  - with the real `sh`: the command runs and its exit status is recorded; `STEW_*` survive a wrapper that overwrites
    them, including values with spaces, quotes, newlines, and backslashes; a placeholder inside a string that
    `sh -c` parses again works; a loop runs the command twice and is caught; `{{STEW_STEP}} || true` and
    `{{STEW_STEP}}; echo post` record the failing status; `{{STEW_STEP}}; false` records 0; a backgrounded
    command has no status when the wrapper exits, and it is stopped; a command killed by SIGTERM records 143;
  - reach count 0 or 2, or no status → `fail` with the matching cause, for `run`, pre-run `verify`, and
    `verify after run`; a command that ran once and fails has a plain `exit N` cause, whatever the wrapper exits;
  - reach problem plus an interrupt → `interrupted`; reach problem plus a log error → `log error`;
  - a reach that cannot be counted: the step script exits 125 and the command does not run;
  - a stale `.reach` or `.status` is removed before the step; an unreadable status is a log error;
  - step files are removed after each step, even when reading the reach fails or `Prepare` fails partway,
    and removing one step's files leaves another step's files alone;
  - step directory: a `TMPDIR` that needs quoting is rejected; the created path matches the plain pattern;
    step keys differ per phase.

### Unit: `runlog`

- Run ID format and sort order; retry with a new suffix when the directory already exists.
- File names, including CI fallback level; both files created; step markers in both files; append order.
- Creation and write errors are returned to the caller, never swallowed.
- `.log`: markers once per step; both streams in write order; created with the pair or not at all.
- `run.json`: round trip; atomic save leaves no temp file; `total_ms` only after the run-end save;
  a file without `workspace_wrapper` loads it as `""`; `project_wrapper` saved as a map, and absent when nil;
  a `project_wrapper` name not in `projects` is invalid.
- `Latest` over several IDs; unknown ID; missing `run.json`; invalid JSON.
- Unfinished phase: a `.log` without an entry is found; none when all logs have entries.
- A created run is locked until `Close`; a second lock on its directory fails with `ErrRunning`.
- `Retention.Expired`: `LastN` alone, `LastN` 0, `Since` alone including its boundary and an invalid-date ID,
  both rules as a union, no rule.
- `Delete`: a locked run is `ErrRunning` and untouched; an unlocked run is removed; a missing run, a non-ID,
  a symlink, and a file are `ErrUnknownRun` and untouched; a removal error is returned.

### Unit: `report`

- Plain reporter: exact bytes for every status word, fallback lines, and blocked lines.
- Phase lines never contain anything after the status word, except ` (<duration>)` or ` by <names>`.
- Duration format, table-driven: 0 → `0.0s`, 49 ms → `0.0s`, 10.46 s → `10.5s`, 59.94 s → `59.9s`,
  59.96 s → `1m0s`, 62.4 s → `1m2s`, 3600 s → `1h0m0s`, 3912 s → `1h5m12s`.
- Duration appears on `done`, `pass`, `skip`, `fail`; never on `blocked` or `interrupted`.
- Content area: step markers, missing trailing newline added, last line for exit, signal, and start error.
- TTY reporter: a fake ticker drives frames `.` → `..` → `...` → `.`; the final redraw clears the line.
- Summary: exact bytes for the example above, including `total:` and `logs:` lines; column widths, `-` cells,
  fallback cells.
- `runs show` page: exact bytes for the Default Output example; body without a trailing newline;
  argv shell quoting; unfinished phase, `unfinished` cell, and `total: unfinished`; no summary when regexes are given;
  the `wrapper:` line, verbatim or `$'…'`-quoted, and absent when the wrapper is `""`;
  `wrapper <project>:` lines after it, in the given order, verbatim or `$'…'`-quoted.

### Unit: pager

- `STEW_PAGER` → `PAGER` → `less` precedence; empty values skipped.
- `LESS=FRX` set only when `LESS` is unset.
- No pager when stdout is not a TTY, with `--porcelain`, or with `--no-pager`.
- A pager that cannot start falls back to direct output.
- A pager that exits early gives exit 0.

### Integration: testscript

- `init`: creates the registry and `.gitignore`; rejects an existing `.stew`.
- Run logs: a run with passing and failing commands writes every expected file with stdout and stderr separated;
  phases that run no command have no files; the `logs:` line names the run directory.
- In a git repo, `git status` does not show `.stew/runs/`.
- Read-only `.stew/runs/`: `stew build` exits 1 and no project command runs (checked via a marker file).
- `add`: writes the exact template; each rejection case; rollback when the registry write fails.
- `add` with an existing `stew.toml`: registers it unchanged, named by its `name`; invalid file, `-a` mismatch,
  self or unregistered dependency, and name clash are rejected; re-adding after `remove`; a failed registry write
  keeps the file.
- `list`: table and `--porcelain` output, sorted by name; empty workspace; invalid workspace.
- `remove`: unregisters; dependents and unknown-name rejections change nothing; a dependency chain and the root
  project removed together; `--clean`; registry write failure; `stew.toml` delete failure.
- Walk-up discovery from a nested directory; the not-a-workspace error.
- `build` creates a file via `run`; a second `build` reports `skip` via `verify`.
- Commands run with cwd set to the project directory.
- A passing command's output does not appear; a failing command's output does.
- stdin is `/dev/null`: a command that reads stdin gets EOF and does not hang.
- Commands get the exact `STEW_*` values, replacing inherited ones.
- A command's background processes are stopped when it exits. A process that left the group first keeps running
  and does not hold the run's lock.
- Runner unit tests: a leftover that ignores SIGTERM gets SIGKILL after the kill delay; a step with no leftovers
  adds no wait; on Linux, a stopped leftover adopted by stew is reaped at once instead of stalling the step.
- Output under testscript is not a TTY, so it matches the plain format with no escape codes.
- Cumulative phases and dependency selection with real `sh`.
- Exit codes 0, 1, 2.
- Run logs: `run.json` and a `.log` per phase exist after `build`.
- A phase command that makes the run directory read-only: that phase reports `fail` with `(log error: ...)`, exit 1.
- `runs show`:
  - `latest` and an exact ID after a failing `build`: phase lines, full bodies, summary;
  - regex selection, several regexes OR-ed, whole-key matching (`api` does not match `webapi-build`);
  - `--porcelain` lines;
  - no match exits 1; invalid regex exits 2; unknown run exits 2; missing `run.json` exits 1;
  - works while a `stew.toml` is invalid;
  - output is not paged under testscript.
- `runs prune`:
  - both rules as a union, oldest first; nothing to prune prints nothing; non-run entries untouched;
  - no rule, bad `--keep-since`, too-long duration, negative `--keep-last-n`, and an extra argument exit 2;
  - a run in progress is `kept`; a process that left the command's group does not keep the lock after stew exits;
  - stew killed with SIGKILL mid-run leaves an `unfinished` run that prune deletes;
  - a failed delete continues with the other runs and exits 1;
  - works while `projects.toml` is invalid, and with no `.stew/runs/`.
- Workspace wrapper, with the real `sh` and fake wrapper scripts on `PATH`. The fakes stand in for any tool that
  runs a command in an environment: one `exec`s, one forks and waits, one sources a file, one takes a command string.
  - `init` writes `config.toml`.
  - cwd is the project directory, for both the wrapper and the command.
  - A variable the wrapper sets is visible to the command.
  - `STEW_*` values are exact inside the command even when the wrapper overwrites them. The wrapper can read them.
  - Variable expansion: `"$STEW_ROOT"` in a wrapper.
  - A workspace whose path has a space and a `'`, with a wrapper that takes a command string: the command runs.
  - A `TMPDIR` that needs quoting: exit 1 before any phase and before any run directory, with the step-directory error.
  - Wrapper stdout and stderr appear unchanged in the logs.
  - No step directory is left after a run, including after an interrupt, and no step file lands in the run directory.
  - Every footgun fails the phase with `wrapper did not run the command (…)`, and the command does not run
    (checked with a marker file): a placeholder in a comment, `echo {{STEW_STEP}}` (exit 0),
    `false && {{STEW_STEP}}`, and a missing tool (exit 127).
  - A loop runs the command twice: `wrapper ran the command 2 times (…)`.
  - `failing-tool ; {{STEW_STEP}}` semantics: the command runs, as `;` means in sh. Documented, not guarded.
  - A pre-run `verify` whose wrapper fails → `fail`, and `run` does not run.
  - A missing `config.toml`, no placeholder, two placeholders, a syntax error, or an unknown key → exit 2
    before anything runs. A wrapper ending in a newline is accepted and runs the command.
  - A wrapper that `exec`s and one that forks, each under Ctrl-C and SIGTERM: the command gets the signal,
    and neither it, the step script, nor the wrapper is still running after stew exits. Exit codes are 130 and 143.
  - A command that exits non-zero through a wrapper gets a plain `exit N` cause, not the wrapper cause.
  - The command's status wins: `{{STEW_STEP}} || true` with `exit 7` fails `(exit 7)`; `{{STEW_STEP}}; echo post`
    with `exit 3` fails `(exit 3)`; `{{STEW_STEP}}; false` with a passing command passes.
  - A wrapper that backgrounds the command and exits first fails with
    `wrapper exited before the command finished (exit 0)`, and the backgrounded command is stopped.
  - `run.json` `workspace_wrapper`, and the `runs show` `wrapper:` line; neither shows a wrapper when it is `""`.
- Project wrapper, with fake wrapper scripts on `PATH`:
  - `add` writes the template with `project_wrapper = ""`.
  - Nesting order: the workspace wrapper runs outside the project wrapper.
    A project without one gets only the workspace wrapper.
  - `STEW_*` values are exact inside the command even when the project wrapper overwrites them.
  - A project wrapper alone works without a workspace wrapper.
    A project wrapper that never runs the command (`true # {{STEW_STEP}}`) fails the phase;
    the command does not run.
  - A project wrapper `{{STEW_STEP}} || true` inside the workspace wrapper does not hide a failing command:
    `(exit 7)`.
  - In the workspace whose path has a space and a `'`, both levels take a command string: the command runs,
    and no step file is left.
  - `run.json` `project_wrapper` holds only projects with a wrapper; `runs show` prints a `wrapper <project>:` line
    for each.

### Integration: git hook

- `git init` in a temp dir with `.stew/` in a subdirectory.
- Install: hook content and mode `0755`; second install rejected; unsupported hook name rejected.
- `git commit` runs `stew ci --level pre-commit`.
- A failing CI command blocks the commit.
- `git push` runs `stew ci --level pre-push`, falling back to `full`; a failing CI command blocks the push.
- Fallback to `quick`/`full` works from the hook.
- The hook runs through the workspace wrapper, and the command gets the wrapper's environment.

### Gates

`gofmt`, `go vet ./...`, and `go test -race ./...` pass.

## Out of Scope (v1)

- Colors in the `runs show` page
- Parallel execution
- Changed-only selection (`--staged`, `--since`)
- Windows
- Git hooks other than `pre-commit` and `pre-push`
- `--force` overwrite for `init`, `add`, or `git install`
