# Runs, Output, and Logs

## Contents

- Live output
- Summary
- Run logs
- `stew runs show`
- `stew runs list`
- `stew runs prune`
- Paging

## Live Output

One line per section: `==> <project>: <section> ... <status>`, plus ` (<duration>)` for `done`, `skip`, and `fail`.

```text
==> core: setup ... done (0.0s)
==> core: build ... fail (0.4s)
--- stew: run: sh ./build.sh
compiling core
error: config.ini not found
(exit 2)
==> api: build ... blocked by core:build
```

- `done` and `skip` print only the line. Their output is in the logs.
- `fail` and `interrupted` replay the section's output below the line:
  - `--- stew: <step>: <command>` before each step's output. `<step>` is `skip_if`, `run`, or `verify`.
  - Every step except a failed `skip_if` is replayed. stdout and stderr are interleaved; the order across the two
    streams is approximate.
  - Last line: `(exit N)`, `(signal <name>)`, `(cannot start: <error>)`, `(log error: <error>)`, or a wrapper cause
    such as `(wrapper did not run the command (exit 0))`.
- Durations: `0.4s`, `10.5s` under a minute; `1m3s`, `1h5m12s` from a minute up.
- On a terminal the running line animates. Otherwise stew writes the start of the line when the section starts and
  the rest when it ends, with no escape codes.

## Summary

After every run:

```text
┌─────────┬───────┬─────────┬─────────┐
│ project │ setup │ build   │ ci.full │
├─────────┼───────┼─────────┼─────────┤
│ core    │ done  │ fail    │ -       │
│ api     │ -     │ blocked │ blocked │
└─────────┴───────┴─────────┴─────────┘
total: 0.4s
logs: .stew/runs/20260926T213255Z-8ac4
```

- Rows are projects, ordered by each project's first section in execution order. Columns are section names,
  ordered by first appearance in execution order. `-` means not selected.
- `logs:` names the run directory, relative to the root.

## Run Logs

```text
.stew/runs/<run-id>/
  run.json              # stew's record of the run; read it through `stew runs show`
  core:setup.log        # stdout and stderr combined, with step markers
  core:setup.stdout
  core:setup.stderr
  ...
```

- A run ID is `<UTC time>-<4 hex>`, e.g. `20260925T043601Z-3f9a`. IDs sort by start time.
- Each section that ran a command gets `<key>.log`, `<key>.stdout`, and `<key>.stderr`. Each step appends a
  `--- stew: <step>: <command>` marker to all three, then its output.
- A section that ran no command (`blocked`, or `skip` with nothing to run) has no log files.
- Runs are never deleted automatically. `.stew/runs/` is git-ignored.
- `grep` works directly on the files: `grep -n error .stew/runs/<run-id>/core:build.log`.

## `stew runs show <run-id> [<regex>...] [--porcelain] [--no-pager]`

- `<run-id>` is an exact ID or `latest`.
- Each regex matches a whole `<project>:<section>` key; several are OR-ed. None selects every section.
- It reads only `.stew/runs/`, so it works while the configuration is broken.

Default output: a header, one line per wrapper, then each section's line followed by its whole `.log`, including
`done` and `skip` sections:

```text
run 20260926T213309Z-c222: stew setup core
wrapper: env FROM_WRAPPER=1 {{STEW_STEP}}
wrapper core: echo pre; {{STEW_STEP}}
==> core: setup ... done (0.0s)
--- stew: run: echo FROM_WRAPPER=$FROM_WRAPPER
pre
FROM_WRAPPER=1
┌─────────┬───────┐
│ project │ setup │
...
```

- With regexes, the summary is left out, and every matched blocked section prints its `blocked by` line.
- A run still going, or whose stew was killed, shows the last started section as `unfinished` with its partial
  log, and `total: unfinished`. stew cannot tell those two cases apart.
- `--porcelain`: one line per matched section, never paged:

  ```text
  <project>\t<section>\t<status>\t<duration-ms or ->\t<log path or ->
  ```

  Example: `core	build	fail	8	.stew/runs/20260926T213255Z-8ac4/core:build.log`. Log paths are root-relative.

| Error | Exit |
| --- | --- |
| `unknown run "<id>"` (also `latest` with no runs) | 2 |
| Invalid regex | 2 |
| `no section matches` | 1 |
| `run <id> has no run.json` | 1 |
| `read run <id>: <error>` | 1 |

## `stew runs list [--porcelain] [--no-pager]`

```text
┌─────────────────────┬───────────────────────┬────────┬───────┬─────────┐
│ started             │ run                   │ result │ total │ command │
├─────────────────────┼───────────────────────┼────────┼───────┼─────────┤
│ 2026-09-27 04:32:55 │ 20260926T213255Z-8ac4 │ fail   │ 0.4s  │ stew ci │
└─────────────────────┴───────────────────────┴────────┴───────┴─────────┘
```

- Newest first. `started` is in local time.
- `result`, first match wins: `unreadable` (no valid record), `unfinished` (running or killed), `interrupted`,
  `fail` (a section failed or was blocked), `ok`.
- No runs prints `no runs`.
- `--porcelain`: `<started RFC 3339>\t<run-id>\t<result>\t<total-ms or ->\t<command>`, never paged.

## `stew runs prune [--keep-since <time>] [--keep-last-n <n>]`

- Keeps a run when any given rule keeps it; deletes the rest. At least one rule is required (exit 2 otherwise).
- `--keep-last-n <n>`: the `n` newest runs. `n` is 0 or more.
- `--keep-since <time>`: runs started at or after `<time>`:
  - a duration ago, whole numbers with `w`, `d`, `h`, `m`, `s`: `36h`, `7d`, `1w2d`;
  - a local date `2026-09-01`, or `"2026-09-01 15:04:05"`;
  - an RFC 3339 time: `2026-09-01T15:04:05+07:00`.
- A run in progress is never deleted. It prints `kept <id>: running`.
- Each deleted run prints `pruned <id>`, oldest first.
- A failed delete prints `stew: prune <id>: <error>`, continues, and exits 1.

## Paging

- `runs show` and `runs list` page only when stdout is a terminal.
- The pager is the first non-empty of `STEW_PAGER`, `PAGER`, then `less`. `LESS=FRX` is set when `LESS` is unset.
- `--no-pager`, `--porcelain`, `STEW_PAGER=cat`, or `PAGER=cat` prints directly.
