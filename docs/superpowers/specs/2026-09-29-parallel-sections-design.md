# Parallel Sections — Design

Date: 2026-09-29
Status: approved

## Goal

Run independent sections at the same time, by default, so a workspace run takes as long as its critical path
instead of the sum of its sections.

- Parallel by default, capped by a job limit: `-j N`, else `jobs` in `.stew/config.toml`, else the CPU count.
- A workspace can opt out entirely: `concurrency = "serial"` in `.stew/config.toml`.
- A project can run its sections one at a time, or each one alone: `concurrency = "serial" | "exclusive"`.
- A section can run alone: `exclusive = true`.

This spec changes `2026-09-25-steward-design.md`. The implementation folds these rules into it, so it stays the
current description of stew. Anything not mentioned here is unchanged.

## Terms

- **Job limit.** The most sections that run at once.
- **Running.** A section has started (its `SectionStart` event fired) and not yet ended.
- **Ready.** A section that has not started and whose every requirement has ended.
- **Exclusive section.** Runs with no other section running.
- **Serial project.** At most one of its sections runs at a time. Other projects' sections may overlap it.

## Configuration

### `.stew/config.toml`

```toml
workspace_wrapper = ""
workspace_trust = ""
# "parallel" runs independent sections at the same time; "serial" runs one at a time. Default "parallel".
# concurrency = "parallel"
# Most sections running at once in parallel mode. Default: the number of CPUs. stew run -j overrides it.
# jobs = 8
```

| Key           | Required | Rule                                                   |
| ------------- | -------- | ------------------------------------------------------ |
| `concurrency` | no       | `"parallel"` or `"serial"`. Absent means `"parallel"`. |
| `jobs`        | no       | Integer ≥ 1. Absent means the CPU count.               |

- `stew init` writes both keys commented out, as above. Existing configs stay valid.
- Errors, exit 2, like other config errors:
  - `<file>: concurrency: must be "parallel" or "serial"`
  - `<file>: jobs: must be at least 1`
  - A wrong TOML type (e.g. `jobs = "8"`) keeps the TOML decoder's error, prefixed with `<file>: `.

### `stew.toml`

```toml
name = "core"
project_wrapper = ""
project_trust = ""
concurrency = "serial"

[ci.full]
run = "go test -race ./..."
exclusive = true
```

| Key                       | Required | Rule                                                                       |
| ------------------------- | -------- | -------------------------------------------------------------------------- |
| `concurrency` (top level) | no       | `"parallel"`, `"serial"`, or `"exclusive"`. Absent means `"parallel"`.     |
| `exclusive` (section)     | no       | Boolean. Absent means inherited from the project.                          |

- `concurrency` joins `name`, `project_wrapper`, and `project_trust` as a top-level key. Every other top-level key
  is still a section or namespace.
- `exclusive` joins the section keys (`run`, `skip_if`, `verify`, `requires`). A table holding any section key is
  a section and must have `run`, so `[lint]` with only `exclusive = true` fails with `[lint]: missing key "run"`,
  and `[ci]` with `exclusive` plus `[ci.full]` fails with `[ci]: a section cannot contain sections`.
- Errors, exit 2, in the style of the existing checks:
  - `<file>: concurrency: want a string`
  - `<file>: concurrency: must be "parallel", "serial", or "exclusive"`
  - `<file>: [<section>]: exclusive: want a boolean`
- The `stew add` template gains, after `project_trust`:

  ```toml
  # "parallel", "serial" (one section of this project at a time), or "exclusive" (each section alone).
  # concurrency = "parallel"
  ```

  and `# exclusive = false` in the commented section example.

### Resolved Per Section

| Project `concurrency` | Section `exclusive` | Exclusive | Serial |
| --------------------- | ------------------- | --------- | ------ |
| `parallel`            | absent or `false`   | no        | no     |
| `parallel`            | `true`              | yes       | no     |
| `serial`              | absent or `false`   | no        | yes    |
| `serial`              | `true`              | yes       | yes    |
| `exclusive`           | absent or `true`    | yes       | yes    |
| `exclusive`           | `false`             | no        | yes    |

An exclusive section runs alone, so its Serial value only matters for the other sections of its project.

A section with nothing to run (`run` and `verify` both `""`) is never exclusive: it would hold back the run to do
nothing.

### Job Limit

| Workspace `concurrency` | Job limit                                           |
| ----------------------- | --------------------------------------------------- |
| `serial`                | 1. `jobs` and `-j` are ignored.                     |
| `parallel`              | `-j` if given, else `jobs`, else the CPU count      |

- `-j, --jobs <n>` is available on `stew run` and every alias (`setup`, `build`, `ci`, `setup-worktree`).
- `-j` below 1 exits 2: `invalid jobs 0: must be at least 1`.
- `--dry-run` accepts `-j` and ignores it.
- Serial mode ignores `-j` so a workspace that opted out stays opted out in CI and git hooks.
- The CPU count is `runtime.GOMAXPROCS(0)`, which follows cgroup CPU limits, read once in `main` and passed
  down in `process` (see Implementation).

## Scheduling

The plan order is unchanged: topological (Kahn), smallest project name first, then smallest section name. It is
now the **start priority**.

Once at the start, and whenever a section ends, the scheduler scans the sections that have not started, in plan
order, and stops at the first rule that says stop:

1. No stop signal has arrived. After one, the scan does nothing.
2. The job limit is not reached, and no exclusive section is running. Otherwise the scan stops.
3. A section that is not ready is passed over.
4. A ready section with a requirement that failed or was blocked becomes `blocked` at once, with the same
   recording, pruning, and `blocked by` line as today. Its dependents come later in plan order, so they are
   blocked when a scan reaches them.
5. A ready exclusive section starts when nothing is running. Either way, the scan stops: no section after it in
   plan order starts before it. Only sections before it in plan order can still start; there are finitely many,
   so it cannot starve.
6. A ready serial section is passed over while another section of its project is running.
7. Otherwise the section starts.

Then the scheduler waits for the next section to end.

Consequences:

- With a job limit of 1, every section starts in plan order, and blocked sections are reported where they would
  have run. Order, blocking, and results are identical to stew before this change. Only the plain output format
  differs (see Output).
- A section with nothing to run (`run`, `skip_if`, and `verify` all `""`) counts as running while it starts and
  ends. It takes a slot for no measurable time.
- Scheduling is deterministic for a given sequence of end events. The start order across runs may differ, since
  sections end at different times.

## Failure and Blocking

Unchanged in meaning:

- A failed section blocks only the sections that require it, directly or transitively.
- Sections already running are not stopped. Independent sections keep starting.
- A `blocked by` line prints when the scheduler marks the section blocked, not at a fixed place in the plan.
- The exit code is 1 when any section failed or was blocked.

## Interrupt

- First stop signal (SIGINT, SIGTERM, SIGHUP): nothing new starts. stew forwards the signal to **every** running
  section's process group, with today's per-signal rules: SIGINT waits; SIGTERM and SIGHUP send SIGKILL after 5 s.
- stew waits for every running section to end, then records and reports each one as it ends.
- Second stop signal: SIGKILL to every running group at once.
- Each section that was running reports `interrupted`, with its content area. A section that ended on its own
  before the signal reached it keeps its real status.
- Sections that never started stay `-` in the summary. Blocked sections found before the signal keep `blocked`.
- stew prints the summary and exits 128 + the first signal's number, as today.

## Output

### Plain (Not a Terminal)

Each section prints two lines: one when it starts, one when it ends.

```
==> core: setup ... started
==> web: setup ... started
==> web: setup ... fail (0.4s)
--- stew: run: npm ci
npm ERR! missing package.json
(exit 1)
==> web: build ... blocked by web:setup
==> core: setup ... done (1.2s)
==> core: build ... started
==> core: build ... done (3.1s)
```

- Start line: `==> <project>: <section> ... started`.
- End line: `==> <project>: <section> ... <status>`, plus ` (<duration>)` as today, then the content area for
  `fail` and `interrupted`.
- Blocked sections print one line, as today, with no start line.
- The format is the same in serial mode.
- A section's end line and content area are written in one piece, so output of different sections never
  interleaves.

### Terminal

The screen has two parts: finished lines, which scroll up, and a **footer** below them with one line per running
section, in start order. There are no `started` lines.

```
==> core: setup ... done (1.2s)
==> web: setup ... fail (0.4s)
--- stew: run: npm ci
npm ERR! missing package.json
(exit 1)
==> web: build ... blocked by web:setup
==> core: build ..
==> api: setup ...
```

The last two lines are the footer.

- Footer line: `==> <project>: <section> <dots>`. All footer lines share one animation: `.` → `..` → `...`, one
  frame every 300 ms.
- A section that starts adds its line to the bottom of the footer.
- A section that ends leaves the footer. Its end line and content area print above the footer, then the footer
  is redrawn. A section's line is never printed twice: when the top footer line ends, it looks like an in-place
  update; when a lower one ends first, its line moves above the running ones.
- A blocked line prints above the footer the same way.
- Redraw: `\r`, cursor up by the footer height minus one (`\x1b[<n>A`, omitted when 0), clear to end of screen
  (`\x1b[J`), finished text if any, then the footer. The cursor stays at the end of the last footer line, which
  has no trailing newline.
- Footer lines are cut to the terminal width minus one column, so none wraps and the cursor math holds. When the
  width is unknown, lines are not cut.
- When the footer would be taller than the terminal height minus one, it shows the first lines that fit, then
  `... <n> more running` as its last line.
- The size is read at each redraw with `TIOCGWINSZ` on stdout.
- Finished text is never cut.

### Summary

Unchanged. Rows are projects in plan order.

## Run Logs

- Each section keeps its own `.stdout`, `.stderr`, and `.log` files. Parallel sections share no file.
- `run.json` `sections` lists records in the order sections end (or are blocked), the same order as the live
  output. `stew runs show` lists them in that order.
- The manifest is written by one goroutine; saves never race.

## Step Directory

The file name of a wrapped step becomes `<n>-<project>-<section>.<ext>`, where `<n>` is the section's 0-based
index in the plan. Before, two keys could map to one name (`a-b:c`, `a:b-c`), which was harmless only because
steps ran one at a time. The index makes every name unique within a run.

## Unchanged

- The section algorithm (`skip_if`, `run`, `verify`), statuses, content area, and log file markers.
- Command environment, `STEW_*` variables, process groups, leftover cleanup, and wrappers.
- `stew exec`, `stew trust`, `stew runs *`, `stew list`, and git hooks.
- `--dry-run` output: the `#` column is the start priority.

## Implementation

### `internal/workspace`

- `config.go`: `Config` gains `Concurrency string` (`"parallel"` or `"serial"`) and `Jobs int` (0 means unset).
  `ConfigTemplate` gains both keys, commented out.
- `project.go`: `Project` gains `Concurrency string`. `Section` gains `Exclusive *bool`. `sectionKeys` gains
  `exclusive`. The `stew add` template changes as above.

### `cmd/stew`

- `plan.go`: `buildPlan` sets `runner.Section.Exclusive` and `Serial` from the table in Resolved Per Section.
- `main.go`: `process` gains `cpus int`. `main` sets it to `runtime.GOMAXPROCS(0)`. The in-process `stewCmd` in
  `main_test.go` sets 1, so no test depends on the machine and existing scripts keep plan order; scripts that
  test parallel runs pass `-j`.
- `run.go`: adds `-j/--jobs` to `run` and the aliases, computes the job limit from `proc.cpus` and the config, and
  sets `Runner.Jobs`.
  `newReporter` gives the TTY reporter a size function backed by `unix.IoctlGetWinsize` (`golang.org/x/sys`
  becomes a direct dependency).

### `internal/runner`

- `Section` gains `Exclusive bool` and `Serial bool`. `Runner` gains `Jobs int`, which must be ≥ 1.
- `Run` becomes an event loop that owns all state: ready set, running set, ended statuses, and results. Each
  started section runs `section` in its own goroutine and sends its outcome on a channel. The goroutine measures
  `Duration` itself with `Now`, so time a finished section waits for the loop does not count. The loop alone calls
  `Report` and `Record`, so neither needs to be safe for concurrent use.
- The step key gets the plan index prefix.
- `Shell` needs no change. Closing `Force` already reaches every running command. Each command leads its own
  session and group, and leftover reaping calls `Wait4(-pgid)` on that group only, so it never reaps another
  running section's command.

### `internal/report`

- `Plain.SectionStart` writes the start line. `Plain.SectionEnd` writes the whole end line.
- `TTY` is rewritten around the footer. Its constructor takes the ticker and a
  `size func() (width, height int, ok bool)`, both injected by tests.

### Docs

- `skills/use-steward/SKILL.md`: sections run in parallel, not "one at a time".
- `references/execution.md`: Order, a new Concurrency section, Failure and Blocking, Interrupts.
- `references/configuration.md`: the new keys.
- `references/commands.md`: `-j`.
- `references/runs.md`: plain `started` lines, the terminal footer, and `run.json` order.
- `2026-09-25-steward-design.md`: fold in this spec.

## Testing

All tests follow `docs/testing.md`.

- Runner, with a gated fake executor that blocks each command until the test releases it and announces each
  start on a channel:
  - with limit N, N sections run and the next starts only after one is released;
  - an exclusive section runs alone, and no section after it in plan order starts before it;
  - two sections of a serial project never overlap, while another project's section overlaps both;
  - with limit 1, the start order equals the plan order;
  - a failure blocks only its dependents, while a running sibling ends `done`;
  - an interrupt starts nothing new and ends every running section `interrupted`; a section released before the
    interrupt keeps `done`;
  - step keys are unique for `a-b:c` and `a:b-c`.
- Existing runner tests run with `Jobs: 1` and keep their assertions. The current fake executor becomes safe for
  concurrent use.
- Report: exact bytes for plain start and end lines, and for the TTY footer: add, end at top, end below top, fail
  replay, blocked line, width cut, and height cap.
- Workspace: parsing, defaults, and exact error messages.
- Plan: the resolution table, row by row.
- Job limit: serial config ignores `-j`; `-j` beats `jobs`; `jobs` beats `proc.cpus`.
- Scripts (`cmd/stew/testdata/script/parallel.txtar`):
  - two independent sections each wait, with a bounded poll, for a file the other creates, so they pass only
    when they overlap; the script passes `-j 2` rather than relying on the default;
  - `-j 1` and `concurrency = "serial"` run the same selection in plan order;
  - `-j 0` exits 2 with its message;
  - output lines, exit codes, and `run.json` order are asserted exactly.
- Existing scripts that assert plain output gain the `started` lines.

## Commits

Each commit is standalone: it builds, passes `ci.quick` and `ci.full`, and ships a working feature.

1. `feat: print a started line for each section in plain output`
2. `feat: show running sections in a terminal footer`
3. `feat: run independent sections in parallel up to a job limit` (loop, step key, `-j`, `jobs`, workspace
   `concurrency`, docs)
4. `feat: add exclusive sections and serial or exclusive projects`
