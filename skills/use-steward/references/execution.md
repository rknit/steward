# Execution

## Contents

- Selection
- Order
- Section steps
- Statuses
- Failure and blocking
- Command environment
- Processes
- Wrappers
- Trust
- Interrupts

## Selection

- `stew run <regex>...` selects every section whose whole key matches a regex (Go RE2 syntax, anchored at both
  ends). `core` does not match `core:build`; use `core:.*`.
- Every regex must match at least one section, or the command exits 2 before anything runs:
  `pattern "web:build" matches no section`.
- The selection also includes each selected section's `requires`, transitively.
- Aliases build the patterns for you. Names are regex-quoted, so `my.lib` matches only `my.lib`.

  | Alias | Same as |
  | --- | --- |
  | `stew setup [project...]` | `stew run '<project>:setup'...`, or `'.*:setup'` with no project |
  | `stew build [project...]` | the same with `build` |
  | `stew ci [project...] [-l <level>]` | the same with `ci.<level>`; the level defaults to `full` |
  | `stew setup-worktree [project...]` | the same with `worktree\.setup` |

- With no project named, an alias runs in every project that defines the section. If none defines it, the alias
  exits 2 (`pattern ".*:build" matches no section`). A fresh workspace therefore fails `stew build`.
- A named project without the section exits 2 the same way. An unknown project: `unknown project "web"`, exit 2.
- `-l` takes any level that makes `ci.<level>` a valid section name. `stew ci -l lint` runs `ci.lint`.

## Order

- Sections run in parallel. A section starts once every section it requires has ended.
- At most the job limit run at once: `-j N`, else `jobs` in `.stew/config.toml`, else the CPU count.
- `concurrency = "serial"` in `.stew/config.toml` runs one section at a time and ignores `-j` and `jobs`.
- The plan order is a topological sort over `requires`. Among ready sections, the smallest project name comes first,
  then the smallest section name. Free slots go to sections in this order. With one job, sections run exactly in
  this order.
- Sections of different projects share the machine and may run at once. Use `exclusive = true` for a section that
  needs the machine alone, and `concurrency = "serial"` in a project whose sections share files.
- An exclusive section waits until nothing runs, and no section after it in the order starts before it.
- `--dry-run` prints this order with each section's direct requirements and runs nothing:

  ```text
  ┌───┬──────────────┬────────────┐
  │ # │ key          │ requires   │
  ├───┼──────────────┼────────────┤
  │ 1 │ core:setup   │ -          │
  │ 2 │ core:build   │ core:setup │
  │ 3 │ api:build    │ core:build │
  │ 4 │ api:ci.full* │ api:build  │
  └───┴──────────────┴────────────┘
  * matched by a pattern; others are pulled in by requires
  ```

## Section Steps

Each section runs up to three steps. A step whose string is `""` is not run.

| Step | Result |
| --- | --- |
| `skip_if` | Exit 0: the section is `skip`, nothing else runs. Non-zero: go on. |
| `run` | Non-zero: `fail`. |
| `verify` | Non-zero: `fail`. |
| end | `done` if `run` or `verify` ran, else `skip`. |

- A failing `skip_if` only means "not done yet". Its output is not shown on failure.
- `run = ""` with `verify` set runs `verify` alone, as an assertion.
- Use `skip_if` to make expensive steps idempotent, e.g. `skip_if = "test -d node_modules"`.

## Statuses

| Status | Meaning |
| --- | --- |
| `done` | `run` or `verify` ran and every step passed. |
| `skip` | `skip_if` exited 0, or the section has nothing to run. |
| `fail` | A step exited non-zero, was killed, could not start, or its wrapper or log failed. |
| `blocked` | A required section failed or was blocked. The section did not run. |
| `interrupted` | A stop signal arrived while the section ran. |

`stew runs show` and `stew runs list` also use `unfinished` for a run that is still going or whose `stew` was
killed.

## Failure and Blocking

- A failure blocks only the sections that require it, directly or transitively. Other sections keep running.
- Sections already running when a section fails keep running.
- A blocked section prints `==> <project>: <section> ... blocked by <key>, <key>` only when a direct requirement
  failed. A section whose failed requirements are all themselves blocked prints nothing, but its summary cell still
  says `blocked`.
- `blocked by` lists direct requirements that failed or were blocked. Follow it hop by hop to the failed section.
- The run exits 1 if any section failed or was blocked.

## Command Environment

Every step runs as `sh -c <command>`:

- in the project's directory;
- with stdin `/dev/null`;
- with no controlling terminal, in its own session and process group, so opening `/dev/tty` fails at once;
- with output captured, never streamed live;
- with the inherited environment plus these variables, which replace any inherited values of the same name:

  | Variable | Value |
  | --- | --- |
  | `STEW_RUN_ID` | The run ID, as in `.stew/runs/<run-id>/` |
  | `STEW_ROOT` | Absolute workspace root |
  | `STEW_PROJECT` | Project name |
  | `STEW_SECTION` | Section name, e.g. `ci.quick` |
  | `STEW_TAG` | The key, e.g. `core:ci.quick` |

A command that waits for input, a password, or a confirmation cannot get it. Use non-interactive flags.

## Processes

- When a step's command exits, stew stops everything it left running in its process group: SIGTERM, then SIGKILL
  after 5 s. A step must not leave background processes behind. Start and stop a test server in the same step.
- A process that leaves the group before the command exits (`setsid`, a daemon) is not stopped.

## Wrappers

A wrapper is shell code that provides the toolchain. `{{STEW_STEP}}` marks where the command goes. stew replaces
it with the absolute path of an executable that runs the step's command.

- `workspace_wrapper` wraps every command in the workspace. A project's `project_wrapper` wraps that project's
  commands inside the workspace wrapper. An empty wrapper is dropped.
- Wrappers apply to every step of every section: `skip_if`, `run`, and `verify`.
- A wrapper runs in the project's directory and sees the `STEW_*` variables. Whatever it sets, the command still
  sees the exact `STEW_*` values.
- stew adds nothing to the wrapper. It keeps normal `sh` meaning: `tool ; {{STEW_STEP}}` runs the command even
  when `tool` fails. Use `&&`.
- The placeholder works bare, inside quotes, and in a string a tool parses again as shell code. The path starts
  with `/` and never needs quoting.

Shapes (not a list of supported tools):

| Shape | Wrapper |
| --- | --- |
| A tool that runs a command in its environment | `direnv exec . {{STEW_STEP}}` |
| A tool that takes a command string | `nix-shell --run {{STEW_STEP}}` |
| Source a script, then run | `. ./env.sh && {{STEW_STEP}}` |
| Variable expansion | `tool exec "$STEW_ROOT" {{STEW_STEP}}` |

stew checks that the wrapper runs the command exactly once and waits for it. The command's own exit status is the
step's result, whatever the wrapper exits. Otherwise the section fails with one of these causes:

| Cause | Typical wrapper |
| --- | --- |
| `wrapper did not run the command (exit N)` | `echo {{STEW_STEP}}`, `false && {{STEW_STEP}}`, a missing tool (exit 127), the placeholder in a comment |
| `wrapper ran the command N times (...)` | A loop |
| `wrapper exited before the command finished (...)` | `{{STEW_STEP}} &` |

- `{{STEW_STEP}} || true` does not hide a failure. The command's status still wins.
- A wrapper failure in `skip_if` fails the section. It is not "not done yet".
- Wrapper output goes into the step's logs like any other output.
- The executables live in a new `stew-*` directory in `$TMPDIR` (`/tmp` when unset), removed after the run.
  - `TMPDIR` must be a plain path of letters, digits, and `/._-`. Otherwise:
    `cannot create step directory: <path> needs shell quoting; set TMPDIR to a path of letters, digits, and /._-`.
  - It must allow executing files. On a `noexec` mount every wrapped step fails with
    `wrapper did not run the command (exit 126)`.

## Trust

A trust command makes a wrapper usable in a tree, e.g. `direnv allow .` or `mise trust`.

- Entries: the workspace (`workspace_trust`), then each project with a `project_trust`, by name. `""` means none.
- An entry is pending until `.stew/trust.json` records the same command for the same absolute root. A new
  worktree or clone, a moved tree, and a changed command make entries pending again.
- Which commands check trust:

  | Command | Runs |
  | --- | --- |
  | `stew run` and every alias, without `--dry-run` | pending entries |
  | `stew exec` | pending entries |
  | `stew add` | the added project's entry, if pending |
  | `stew trust` | every entry, pending or not |

  `--dry-run`, `list`, `remove`, `runs *`, `init`, and `git install` never check trust.
- With pending entries, stew asks only when stdin and stdout are both terminals. Otherwise it exits 1 and runs
  nothing:
  - `stew: untrusted: workspace, core (run: stew trust)` for `run`, the aliases, and `exec`;
  - `stew: untrusted: <name> (run: stew add --trusted)` for `add`, with the registry unchanged;
  - `stew: untrusted: ... (run: stew trust --yes)` for `trust`.
- `stew trust --yes` and `stew add --trusted` run the entries without asking. They are the consent.
- On a terminal, `y` or `yes` runs them. Anything else prints `stew: trust declined` and exits 1.
- Each entry runs as `sh -c <cmd>` without wrappers, in the root or the project directory, with stdin
  `/dev/null`. Output goes to stew's output. The environment gets `STEW_ROOT`, and `STEW_PROJECT` for a project.
- A failed entry prints `stew: trust <name>: exit N` and exits 1. Entries that passed before it stay recorded.
- stew cannot see a tool's own reasons to re-trust. If a pull changes `.envrc` and direnv blocks, the wrapper
  fails; run `stew trust` again (with consent).
- `stew trust` with no trust command defined prints `nothing to trust` and exits 0 (silent with `--yes`).

## Interrupts

- Ctrl-C (SIGINT): stew forwards it to every running command's group, waits for them to exit, and starts nothing
  new.
- SIGTERM or SIGHUP: stew forwards it, then sends SIGKILL after 5 s to each command that still runs.
- A second stop signal kills every running command's group at once.
- Each running section becomes `interrupted`. A section that ended before the signal keeps its status. stew prints
  the summary and exits 128 + the first signal's number: 130 (SIGINT), 143 (SIGTERM), 129 (SIGHUP).
