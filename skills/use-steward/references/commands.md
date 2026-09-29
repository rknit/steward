# Commands

## Contents

- Conventions
- `stew init`
- `stew add`
- `stew remove`
- `stew list`
- `stew run` and the aliases
- `stew exec`
- `stew trust`
- `stew runs show`, `list`, `prune`
- `stew git install`
- `stew skills install`, `uninstall`
- Exit codes

## Conventions

- Projects are named by their `name`. Only `stew add` takes a path.
- Flags may come before or after names.
- Output goes to stdout. Configuration and usage errors go to stderr as `stew: <message>`.
- `stew <command> --help` prints the usage of any command.

## `stew init`

Creates `.stew/` in the current directory: `projects.toml` (`projects = []`), `config.toml` (both keys `""`), and
`.gitignore` (`runs/`, `trust.json`).

- Rejects an existing `.stew`. Does not look at ancestors.

## `stew add <path> [-a/--alias <name>] [--trusted]`

Registers a directory as a project.

- `<path>` is resolved against the current directory. It must be an existing directory inside the root and not
  already registered.
- An existing `<path>/stew.toml` is used unchanged: it must be valid, `--alias` must match its `name`, and every
  `requires` must name a registered project and a section it defines. Add projects in `requires` order.
- Otherwise stew writes the template (see `configuration.md`). The name is `--alias`, or the directory's name.
  An invalid name is rejected; pass `-a`.
- The name must be unused.
- If the project has a `project_trust`, it runs with consent. Without a terminal, pass `--trusted` after getting
  consent, or the add fails with `stew: untrusted: <name> (run: stew add --trusted)`.
- Prints `using existing <path>/stew.toml` when it applies, then `added <name> (<path>)`.
- A rejection exits 1 and changes nothing.

## `stew remove <name>... [--clean]`

Unregisters projects.

- Unknown name: `unknown project "<name>"`, exit 2.
- A project still required by one that stays: `stew: cannot remove core: needed by api, web`, exit 1. Remove the
  dependents in the same command, or edit their `requires` first.
- Prints `removed <name> (<path>)` per project.
- `--clean` also deletes each `stew.toml` and prints `deleted <path>/stew.toml`.
- A project whose `stew.toml` is missing cannot be removed by name, because the workspace does not load. Edit
  `.stew/projects.toml` by hand.

## `stew list [--porcelain]`

Prints registered projects sorted by name, with paths and dependencies derived from `requires`:

```text
┌─────────┬───────────┬──────────────┐
│ project │ path      │ dependencies │
├─────────┼───────────┼──────────────┤
│ api     │ svc/api   │ core         │
│ core    │ libs/core │ -            │
└─────────┴───────────┴──────────────┘
```

- `--porcelain`: one line per project, `<name>\t<path>\t<dep>,<dep>`, no header. Empty dependencies are an empty
  field.
- An empty workspace prints `no projects (add one with: stew add <path>)`, or nothing with `--porcelain`.

## `stew run <regex>... [--dry-run]` and the Aliases

```text
stew run <regex>... [--dry-run]
stew setup [project...] [--dry-run]
stew build [project...] [--dry-run]
stew ci [project...] [-l/--level <level>] [--dry-run]
stew setup-worktree [project...] [--dry-run]
```

- Each regex must match a whole `<project>:<section>` key. `stew ci` runs `ci.full` unless `-l` names a level.
- The selection, order, and failure rules are in `execution.md`. The output is in `runs.md`.
- Each invocation without `--dry-run` is one run, logged in `.stew/runs/<run-id>/`.
- Before running, it checks trust (see `execution.md`).
- `--dry-run` prints the order and exits 0. It skips the trust check, runs nothing, and writes no logs.
  Configuration and pattern errors still exit 2.

## `stew exec [project] <command>`

Runs one shell command through the wrappers, for work outside sections:

```sh
stew exec api 'go test ./pkg/x -run TestFoo'
stew exec 'which go'
```

- `<command>` is one argument, run as `sh -c <command>`. Quote it.
- It runs in the current directory, not in the project directory. To run in a project, `cd` to its path (from
  `stew list`) first: `cd services/api && stew exec api 'cargo clippy'`.
- The workspace wrapper always applies. With a project named, its project wrapper nests inside. Without one, only
  the workspace wrapper applies, even when the current directory is inside a project.
- Env: `STEW_ROOT`, and `STEW_PROJECT` with a project named. No run ID, section, or tag.
- It runs like a foreground job of an interactive shell: it gets stew's stdin, stdout, and stderr, and signals
  reach it. Output streams live.
- No run directory, log, or summary.
- Exits with the command's status, or 128 + N after signal N.
- A wrapper that does not run the command once prints the cause, e.g.
  `stew: wrapper did not run the command (exit 0)`, and exits non-zero.
- Checks trust first, like `stew run`.
- Unknown project: `unknown project "web"`, exit 2.

## `stew trust [--yes]`

Runs every trust command again, pending or not, and records them for this tree.

- Without `--yes`, it asks on a terminal. Without a terminal it exits 1:
  `stew: untrusted: workspace, core (run: stew trust --yes)`.
- `--yes` runs without asking. Use it only after the operator agreed to the listed commands.
- The record starts empty, so it ends up holding only what this invocation ran.
- No trust command in the workspace: prints `nothing to trust` (nothing with `--yes`), exit 0.

## `stew runs show <run-id> [<regex>...] [--porcelain] [--no-pager]`

Shows a run from its logs. `<run-id>` is an exact ID or `latest`. See `runs.md`.

## `stew runs list [--porcelain] [--no-pager]`

Lists runs, newest first. See `runs.md`.

## `stew runs prune [--keep-since <time>] [--keep-last-n <n>]`

Deletes runs that no keep rule keeps. See `runs.md`.

## `stew git install <hook>`

Installs `pre-commit`, `pre-push`, or `post-checkout`. See `git.md`.

## `stew skills install [<dir>]` and `stew skills uninstall [<dir>]`

Manage this skill, `use-steward`, which each `stew` binary carries for its own version.

- `<dir>` is a skills directory. The skill lives in `<dir>/use-steward/`.
- Without `<dir>`, it is `.agents/skills` in the workspace root, from anywhere in the workspace. Outside a
  workspace, it is `.agents/skills` in the current directory, where `stew init` would put the root. No `init`
  is needed.
- A named `<dir>` is resolved against the current directory and need not be inside a workspace.
- Neither command loads the workspace, so both work while the configuration is broken.
- `install` creates `<dir>` if needed and replaces an earlier `use-steward/`, including files an older version had.
  Prints `installed <path>`. Run it after upgrading `stew`.
- `uninstall` removes `<dir>/use-steward/` and nothing else. Prints `removed <path>`. Without the skill:
  `stew: <path>: not installed`, exit 1.

## Exit Codes

| Code | Meaning |
| --- | --- |
| 0 | Success. |
| 1 | A section failed or was blocked; `init`, `add`, `remove`, `git install`, or `skills` rejected; `runs show` found no matching section or an unreadable run; `runs list` or `runs prune` could not read or delete runs; trust declined, untrusted without a terminal, or a trust command failed. |
| 2 | Invalid usage, an invalid or unmatched pattern, an unknown project or run ID, or invalid workspace configuration. |
| any | `stew exec` exits with its command's status. |
| 130 | Interrupted by SIGINT. SIGTERM exits 143, SIGHUP exits 129. |
