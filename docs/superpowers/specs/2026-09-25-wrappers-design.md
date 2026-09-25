# Command Wrappers — Design

Date: 2026-09-25 (revised 2026-09-26: placeholder instead of appended argv)
Status: implemented

## Goal

Every command stew runs can get the environment a project needs to build, from any environment tool.
It works the same from a shell with that environment loaded, a bare shell, an IDE, or the git pre-commit hook.

- A wrapper provides the toolchain. It is not a build step.
- One workspace wrapper for all projects, plus an optional per-project wrapper nested inside it.
- Tool-agnostic. stew knows no environment tool and special-cases none.
- The user writes where the command goes. stew appends nothing and adds no options, variables, or shell settings
  to the wrapper. Anything like quieter logs or `set -e` is the user's to write.
- A wrapper that does not run the command exactly once fails the phase loudly. A command never passes without running.

## Why

Commands inherit stew's environment. That breaks in two places:

- The pre-commit hook, when git runs from an IDE, a GUI, or a shell without the environment loaded.
- `stew ci` from the root: only the root's environment is loaded, not each project's own.

### Why a placeholder, not an appended command

An earlier draft appended the command as `"$@"`, i.e. `sh -c <cmd>`. That hid argv from the user:

- A tool that parses options after its own arguments read the `-c` as its own option, so users had to know to write `--`.
- `;`, a trailing `# comment`, and a trailing newline changed where the hidden `"$@"` landed.

With a placeholder, the wrapper text is the whole command line. What the user reads is what runs.

## Configuration

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
- `.stew/projects.toml` stays a machine-written registry and holds no configuration.

### `stew.toml`

Required top-level key `project_wrapper`, after `dependencies`:

```toml
name = "api"
dependencies = ["core"]
project_wrapper = ""
```

| Key               | Required | Rule                           |
| ----------------- | -------- | ------------------------------ |
| `project_wrapper` | yes      | String. `""` means no wrapper. |

The `stew add` template has, after `dependencies = []`:

```toml
# Wraps every command of this project, inside the workspace wrapper.
# {{STEW_STEP}} marks where the command goes. "" means none.
project_wrapper = ""
```

### Validation

Checked while loading the workspace, like every other key. Errors exit 2 before anything runs.
The error names the file and key.

- A non-empty wrapper must contain `{{STEW_STEP}}` exactly once.
  Error: `<file>: <key>: must contain {{STEW_STEP}} exactly once (found N)`.
- With the placeholder replaced by a plain path, the wrapper must pass `sh -n -c <W>`.
  Error: `<file>: <key>: <the shell's message>`.
- `sh -n` only checks syntax. It runs nothing. The reach count (below) checks the rest at runtime.

## Wrapper Contract

A wrapper is shell code. `{{STEW_STEP}}` marks where the command goes.
stew replaces it with the path of an executable that runs the step's command, then runs `sh -c <W>`.
That is the whole contract.

Examples of the shape (not a supported-tools list):

| Shape                                   | Wrapper                                      |
| --------------------------------------- | -------------------------------------------- |
| A tool that runs a command in its env   | `<tool> exec . {{STEW_STEP}}`                |
| A tool that takes a command string      | `<tool> --run {{STEW_STEP}}`                 |
| Source a script, then run               | `. ./env.sh && {{STEW_STEP}}`                |
| Variable expansion                      | `<tool> exec "$STEW_ROOT" {{STEW_STEP}}`     |

- The path needs no shell quoting (see Step Directory). The placeholder works bare, inside quotes,
  and inside a string that a tool parses again as shell code.
- The path starts with `/`, so a tool that parses options never reads it as an option.
- The wrapper runs with cwd set to the project directory, like commands.
- It is shell code: `$STEW_*`, `$HOME`, and other variables expand.
- It applies to every step of every phase: `setup`, `build`, `ci.*`, `run` and `verify`.
- The wrapper also runs `setup`'s commands, so it must work before `setup` has run.
- It keeps normal `sh` meaning. `<tool> ; {{STEW_STEP}}` runs the command even when `<tool>` fails.
  Use `&&`, or `set -e;`, to stop on failure.

## Execution

### Without wrappers

With no wrappers (both `""`), a command runs as before: `sh -c <cmd>` with `STEW_*` in its environment.

### With wrappers

For each step, stew writes executables into the run's step directory, one per wrapper level below the outermost,
plus the step script. For a project with both wrappers:

| File                          | Content                                                                         |
| ----------------------------- | ------------------------------------------------------------------------------- |
| `<project>-<phase>.1`         | `#!/bin/sh`, then the project wrapper with `{{STEW_STEP}}` → the `.step` path    |
| `<project>-<phase>.step`      | `#!/bin/sh`, then: count one reach, export every `STEW_*`, run `sh -c <cmd>`,    |
|                               | write its exit status to `<project>-<phase>.status`, and exit with that status  |

The step script is, with every value shell-quoted:

```sh
#!/bin/sh
echo >> '<step dir>/<key>.reach' || exit 125
export 'STEW_RUN_ID=…' 'STEW_ROOT=…' …
sh -c '<cmd>'
code=$?
echo "$code" > '<step dir>/<key>.status' || exit 125
exit "$code"
```

stew runs `sh -c <workspace wrapper with {{STEW_STEP}} → the .1 path>`.
With only one wrapper, there is no `.1` file, and that wrapper's placeholder points to the `.step` file.

- `STEW_*` are in the environment of the whole chain, so a wrapper can read them.
- The step script sets them again, so no wrapper can change what the command sees.
  Their values are shell-quoted inside the script, so any value is safe.
- The command runs as `sh -c <cmd>`, the same as without wrappers.
- If the step script cannot count the reach, it exits 125 without running the command.
  If it cannot write the status, it exits 125.
- Scripts are mode `0700`. The process group, the kill ladder, stdin `/dev/null`, and no controlling terminal are
  unchanged. Signals reach the wrappers and everything they start in the same process group.
- A wrapped command killed by a signal shows as `exit 128+N`, e.g. `exit 143` for SIGTERM, because the step
  script's `sh` reports it that way.
- The wrapper strings are substituted and written as they are. stew adds nothing to them.

### Step Directory

- Created at run start, before the run directory, only when a project in the run has a workspace or project wrapper:
  `os.MkdirTemp` in `$TMPDIR` (made absolute), or `/tmp` when it is unset. The name starts with `stew-`.
- Its full path must match `[A-Za-z0-9/._-]+`, so no file in it ever needs quoting, however many times a tool
  parses it. Otherwise the run exits 1 before any phase:
  `cannot create step directory: <path> needs shell quoting; set TMPDIR to a path of letters, digits, and /._-`.
- It sits outside the workspace, so a workspace path with spaces or quotes never reaches a wrapper.
- A failure to create it exits 1 before any phase and before any run directory exists.
- The temp directory must allow executing files. On a `noexec` mount, every wrapped step fails with
  `wrapper did not run the command (exit 126)`. Set `TMPDIR` to a plain path that allows executing files.
- It is removed when the run ends. A killed stew leaves it for the OS to clean up.
- File names use the phase key, so they are unique among steps running at the same time,
  including under future phase parallelism.

### Reach Count

The step script appends one line to `<project>-<phase>.reach` each time it runs.
When the command finishes, the step script writes the command's exit status to `<project>-<phase>.status`.

- stew removes the step's `.reach` and `.status` files before every step, and all the step's files after it.
- After a wrapped step exits, stew counts the lines and reads the status.
  In a cause, `<exit N / signal S / cannot start: …>` is the outermost wrapper's own result.

  | Reaches | Status | Step result                                                                              |
  | ------- | ------ | ---------------------------------------------------------------------------------------- |
  | 1       | yes    | The command's own exit status. The wrapper's own exit status is ignored.                 |
  | 1       | no     | `fail`, cause `wrapper exited before the command finished (<exit N / signal S / …>)`.    |
  | 0       | —      | `fail`, cause `wrapper did not run the command (<exit N / signal S / cannot start: …>)`. |
  | N ≥ 2   | —      | `fail`, cause `wrapper ran the command N times (<exit N / signal S / …>)`.               |

- The command's status wins both ways. `{{STEW_STEP}} || true` and `{{STEW_STEP}}; echo post` fail when the command
  fails. `{{STEW_STEP}}; false` passes when the command passes.
- A wrapper failure (a reach count other than 1, or no status) fails the phase for every step, including a pre-run
  `verify`. A wrapper failure is not "not done yet".
- Precedence is unchanged: an interrupt gives `interrupted`, and a log error gives `log error: …`,
  before the reach check. An interrupt may stop the step script before the command finishes.
  The cause is then `wrapper exited before the command finished (signal S)`.
- Failing to write, read, or remove a step's files is a log error.

### Guards

| Guard                            | Catches                                                                                 |
| -------------------------------- | --------------------------------------------------------------------------------------- |
| Placeholder count at load        | A wrapper that never mentions the command, or mentions it twice.                        |
| `sh -n` at load                  | Syntax errors: unbalanced quotes, a dangling `if`, etc.                                  |
| Plain step path                  | Quoting that works on one machine and breaks on another.                                 |
| Reach count                      | A wrapper that does not run the command exactly once: `echo {{STEW_STEP}}`, `false && {{STEW_STEP}}`, a placeholder in a comment, a missing tool, a loop. |
| Status file                      | A wrapper that hides the command's failure (`{{STEW_STEP}} \|\| true`, `{{STEW_STEP}}; echo post`), or exits before the command finishes (`{{STEW_STEP}} &`). |
| `STEW_*` set in the step script  | A wrapper, or anything it loads, that sets `STEW_*`.                                     |

None of the guards change what the wrapper does. They only check the input or the result.

### Logs

- Step markers stay `--- stew: <step>: <cmd>`. The wrapper is not repeated per step.
- Wrapper output goes into the step's logs and replay like any other output. stew does not filter or silence it.

## Run Manifest

`run.json` records the wrappers exactly as written. They are not expanded.

```json
{
  "argv": ["ci"],
  "workspace_wrapper": "tool exec . {{STEW_STEP}}",
  "project_wrapper": {"api": "other-tool run {{STEW_STEP}}"},
  "columns": ["setup", "build", "ci.full"],
  ...
}
```

| Field               | Content                                                              |
| ------------------- | -------------------------------------------------------------------- |
| `workspace_wrapper` | Always present. `""` when there is none.                             |
| `project_wrapper`   | Non-empty wrappers of the run's projects, by name. Absent when none. |

A run recorded before these fields existed loads with both empty.

### `stew runs show`

After the `run <id>: …` header line, one line per non-empty wrapper:

```text
run 20260925T043601Z-3f9a: stew ci
wrapper: tool exec . {{STEW_STEP}}
wrapper api: other-tool run {{STEW_STEP}}
==> core: setup ... skip (0.1s)
```

- Project lines are in the manifest's `projects` order.
- A wrapper is printed verbatim, unless it has a control character (e.g. a newline) or invalid UTF-8.
  Then it uses the same `$'…'` quoting as the argv header, so the header stays one line per wrapper.
- `--porcelain` is unchanged.

## Code Changes

| Area                   | Change                                                                                       |
| ---------------------- | -------------------------------------------------------------------------------------------- |
| `workspace/config.go`  | Load and validate `.stew/config.toml`. `Workspace.Wrapper`.                                  |
| `workspace/project.go` | `project_wrapper` key, template, `Project.Wrapper`.                                          |
| `workspace/wrapper.go` | `CheckWrapper`: placeholder count and `sh -n`. The only place `workspace` runs a process.    |
| `cmd/stew/init.go`     | Write `config.toml`.                                                                         |
| `cmd/stew/plan.go`     | `Job.Wrappers = []string{workspace, project}`, empty ones dropped.                           |
| `cmd/stew/phase.go`    | Create and remove the step directory when a job has wrappers.                               |
| `runner/steps.go`      | `StepPlaceholder`, the step directory, writing step files, collecting reaches and statuses.  |
| `runner`               | Wrapped steps run through the step files; reach count and status checked. `Executor.Run` takes argv. |
| `runlog`               | The manifest fields.                                                                         |
| `report/show.go`       | Wrapper header lines.                                                                        |
| Main spec              | Files, `stew.toml`, template, Validation, Command Execution, Run Manifest, `runs show`, Code Layout, Testing. |
| This repo              | `.stew/config.toml` with `workspace_wrapper = "direnv exec . {{STEW_STEP}}"` (this repo has an `.envrc`), and `project_wrapper = ""` in `stew.toml`. |
| Fixtures               | Every testscript workspace gets `config.toml`, and every `stew.toml` gets `project_wrapper`. |

## Testing

This feature is a footgun, so the tests cover every guard with tool-agnostic fake wrappers.
Smoke tests with real tools come on top.

### Unit: `workspace`

- `config.toml`: missing file; missing key; unknown key; wrong type; `""` accepted.
- `project_wrapper`: missing; wrong type; `""` accepted.
- Placeholder count: 0 and 2 are rejected with file, key, and count. A placeholder inside quotes counts.
- Syntax check: an unbalanced quote and a dangling `if` are rejected with file and key.
- Accepted: `tool exec . {{STEW_STEP}}`, `. ./env.sh && {{STEW_STEP}}`, `tool --run '{{STEW_STEP}}'`,
  a multi-line wrapper, and one ending in a newline.

### Unit: `runner`

- No wrappers: the argv is exactly `sh -c <cmd>`, and no step file is written.
- One level and two levels: the argv is `sh -c <outer wrapper with the placeholder replaced>`, the level files hold
  the inner wrapper and the step script, and the wrapper text is otherwise unchanged.
- With the real `sh`: a command runs through one and two levels; its exit status is recorded; `STEW_*` survive a
  wrapper that overwrites them, including a `STEW_ROOT` with spaces and quotes; a placeholder inside a string that
  `sh -c` parses again works; a wrapper that loops runs the command twice and is caught.
- With the real `sh`: `{{STEW_STEP}} || true` and `{{STEW_STEP}}; echo post` record the failing status, also as
  the inner level; `{{STEW_STEP}}; false` records 0; a backgrounded command has no status when the wrapper exits;
  a command killed by SIGTERM records 143.
- Reach count 0 or 2, or no status → `fail` with the matching cause, for `run`, pre-run `verify`, and
  `verify after run`. With one reach and a status, the command's status is the result, whatever the wrapper exits.
- A failed `Prepare` (a level script that cannot be written) removes the step script and forgets the key.
- Reach problem plus an interrupt → `interrupted`. Reach problem plus a log error → `log error`.
- Step directory: a `TMPDIR` that needs quoting is rejected; the created path matches the plain pattern;
  file names differ per phase.

### Integration: testscript

With the real `sh` and fake wrapper scripts on `PATH`. The fakes stand in for any tool that runs a command
in an environment: one `exec`s, one forks and waits, one sources a file, one takes a command string.

- Nesting order: the workspace wrapper runs outside the project wrapper.
- A project wrapper `{{STEW_STEP}} || true` inside the workspace wrapper does not hide a failing command.
- cwd is the project directory, for both the wrapper and the command.
- A variable the wrapper sets is visible to the command.
- `STEW_*` values are exact inside the command even when a wrapper overwrites them. A wrapper can read them.
- Variable expansion: `"$STEW_ROOT"` in a wrapper.
- A workspace whose path has a space and a `'`, with a wrapper that takes a command string: the command runs.
- A `TMPDIR` that needs quoting: exit 1 before any phase, with the step-directory error.
- Wrapper output (stdout and stderr) appears unchanged in the logs.
- Every footgun fails the phase with the reach cause, and the command does not run (checked with a marker file):
  - placeholder in a comment (`tool # {{STEW_STEP}}`)
  - `echo {{STEW_STEP}}` (exit 0)
  - `false && {{STEW_STEP}}`
  - missing tool (exit 127)
- A loop runs the command twice: `wrapper ran the command 2 times`.
- `failing-tool ; {{STEW_STEP}}` semantics: the command runs, as `;` means in sh. Documented, not guarded.
- A pre-run `verify` whose wrapper fails → `fail`, and `run` does not run.
- Missing placeholder, doubled placeholder, or a syntax error in either file → exit 2 before anything runs.
- A wrapper that `exec`s and one that forks, under SIGINT and SIGTERM: exit codes 130 and 143, and neither the
  command, the step script, nor the wrapper is still running after stew exits.
- A command that exits non-zero through a wrapper gets a plain `exit N` cause, not the wrapper cause.
- The command's status wins: `{{STEW_STEP}} || true` with `exit 7` fails `(exit 7)`; `{{STEW_STEP}}; echo post`
  with `exit 3` fails `(exit 3)`; `{{STEW_STEP}}; false` with a passing command passes.
- A wrapper that backgrounds the command and exits first fails with
  `wrapper exited before the command finished (exit 0)`.
- No step directory is left after a run.
- `run.json` fields, and the `runs show` header lines.
- `init` writes `config.toml`, and `add` writes the new template.
- The pre-commit hook runs through the workspace wrapper.

### Smoke: real tools (manual, throwaway)

- A throwaway `flake.nix` in the scratchpad gives a dev shell with every tool below. It is not committed.
- Each tool runs in a scratch workspace outside the repo, **whose path has a space**.
- Each case must pass, and must also fail with the right cause when broken on purpose.
- This only checks that the contract holds with real tools. stew gets no tool-specific code or docs from it.

| Tool                                | Wrapper                                               | Broken on purpose             |
| ----------------------------------- | ----------------------------------------------------- | ----------------------------- |
| direnv                              | `direnv exec . {{STEW_STEP}}`                         | a blocked `.envrc`            |
| direnv, one `.envrc` per project    | `direnv exec . {{STEW_STEP}}`                         | a project without an `.envrc` |
| nix flake                           | `nix develop -c {{STEW_STEP}}`                        | a flake with an eval error    |
| nix shell                           | `nix shell nixpkgs#jq --command {{STEW_STEP}}`        | an unknown package            |
| nix-shell (command string)          | `nix-shell -p jq --run {{STEW_STEP}}`                 | an unknown package            |
| bash -lc (command string)           | `bash -lc '. ./env.sh && {{STEW_STEP}}'`              | a missing `env.sh`            |
| uv                                  | `uv run {{STEW_STEP}}`                                | a broken `pyproject.toml`     |
| poetry                              | `poetry run {{STEW_STEP}}`                            | no `pyproject.toml`           |
| venv (created outside stew first)   | `. .venv/bin/activate && {{STEW_STEP}}`               | a missing `.venv`             |
| mise                                | `mise exec -- {{STEW_STEP}}`                          | an untrusted `mise.toml`      |
| pixi                                | `pixi run {{STEW_STEP}}`                              | no `pixi.toml`                |
| micromamba                          | `micromamba run -n <env> {{STEW_STEP}}`               | an unknown env                |
| devenv                              | `devenv shell -- {{STEW_STEP}}`                       | an eval error                 |
| nested                              | workspace `direnv exec . {{STEW_STEP}}`, project `uv run {{STEW_STEP}}` | —   |

For each tool, also check:

- Ctrl-C mid-command stops everything and leaves no processes behind.
- `STEW_*` values are correct.
- The pre-commit hook works from `env -i` with a minimal `PATH`, so no environment is preloaded.
- No step directory is left behind.

A tool that can't run on this machine (e.g. it needs a daemon or network access that's missing) is reported
as not covered, with the reason.

## Delivery

Three standalone commits on main:

1. `docs: add command wrappers design and plan`
2. `feat: add workspace wrapper for commands`: `config.toml`, init, placeholder, step directory, guards, manifest,
   `runs show`, spec, fixtures, and this repo's config.
3. `feat: add project wrapper nested in the workspace wrapper`: `project_wrapper`, template, nesting,
   manifest map, spec, and fixtures.

## Out of Scope

- Per-phase wrappers.
- Caching a wrapper's environment between steps. Each step runs the wrapper again.
- Showing wrappers in `stew list`.
- A literal `{{STEW_STEP}}` in a wrapper for any other purpose.
