---
name: use-steward
description: Use when a repository has a `.stew/` directory or `stew.toml` files, when a task says to set up, build, test, or run CI with `stew`, when a git hook or CI job runs `stew`, or when `stew` prints errors such as `untrusted:`, `matches no section`, `blocked by`, `not a stew workspace`, or `wrapper did not run the command`.
---

# Using steward (`stew`)

`stew` is a stack-agnostic monorepo orchestrator. Each registered project declares shell commands in named
**sections** of its `stew.toml`. `stew` runs the sections you select, plus the sections they require, one at a
time, in a fixed order, and records every run under `.stew/runs/`.

Use `stew` instead of calling a project's build or test tools by hand. It runs them in the right order, inside the
toolchain wrappers the workspace defines, and keeps the logs.

## Core Terms

| Term | Meaning |
| --- | --- |
| Workspace | Directory tree rooted at the nearest ancestor with `.stew/`. Every command walks up to find it. |
| Project | Directory registered in `.stew/projects.toml`, with a `stew.toml` that gives it a `name`. |
| Section | A table in `stew.toml` with a `run` key, e.g. `[build]` or `[ci.full]`. No name is special. |
| Key | `<project>:<section>`, e.g. `core:ci.full`. Used in `requires`, patterns, logs, and output. |
| `requires` | Keys that must succeed first (`:<section>` is this project's). Selecting a section pulls them in. |
| Wrapper | Shell code that provides the toolchain, e.g. `direnv exec . {{STEW_STEP}}`. Wraps every command. |
| Trust | One-time commands per tree that make a wrapper usable, e.g. `direnv allow .`. Need consent. |
| Run | One `stew run` (or alias) invocation. Logged in `.stew/runs/<run-id>/`. |

## Working in a stew Workspace

1. Look before running: `stew list` shows projects and dependencies. Read the `stew.toml` files for sections.
2. Preview what a command would run: `stew build --dry-run`, `stew run '<regex>' --dry-run`.
3. Run sections: `stew setup`, `stew build`, `stew ci`, `stew ci -l quick`, or `stew run '<regex>'`.
4. On failure, read the replayed output under the `fail` line. For the full logs of every section:
   `stew runs show latest --no-pager`.
5. For a one-off tool command outside sections, run it through the wrappers:
   `stew exec <project> '<command>'`.

## Trust Needs the Operator's Consent

A trust command runs code from the repository (for example `direnv allow .` approves `.envrc`). An agent has no
terminal, so `stew` cannot ask and stops with exit 1:

```text
stew: untrusted: workspace, core (run: stew trust)
```

When you see this:

1. Read the pending commands: `workspace_trust` in `.stew/config.toml` and `project_trust` in each `stew.toml`.
2. Show them to the operator and ask for consent.
3. After consent, run `stew trust --yes`, then retry the original command.

`stew trust` without `--yes` also fails without a terminal (`run: stew trust --yes`). `stew add` of a project with
a trust command needs `--trusted` after the same consent.

## Reading Results

- Success output is hidden. Plain output prints a `started` line and an end line per section. Only `fail` and
  `interrupted` replay output.
- A failed section shows `--- stew: <step>: <command>` markers, the command's output, then `(exit N)` or a cause.
- `blocked by <key>` means a required section failed or was blocked. Fix the named section, not the blocked one.
  Only sections that directly require a failed one print this line. The summary shows every `blocked` section.
- A summary table follows every run, then `total:` and `logs: .stew/runs/<run-id>`.
- Commands get stdin `/dev/null` and no terminal. A command that prompts fails or hangs; pass non-interactive flags.

| Exit | Meaning |
| --- | --- |
| 0 | Every selected section ended `done` or `skip`. |
| 1 | A section failed or was blocked, trust was missing or declined, or `add`/`remove`/`init` refused. |
| 2 | Bad usage, an unknown project or run, a pattern that matches no section, or an invalid configuration. |
| 130, 143, 129 | Interrupted by SIGINT, SIGTERM, or SIGHUP. |
| any | `stew exec` exits with its command's status. |

## Quick Reference

| Goal | Command |
| --- | --- |
| List projects and dependencies | `stew list` (`--porcelain` for tab-separated output) |
| Run one section type everywhere | `stew setup`, `stew build`, `stew ci` (`ci.full`), `stew ci -l <level>` |
| Run it for some projects only | `stew build api web`, `stew ci core -l quick` |
| Run any sections by key regex | `stew run 'core:.*' 'api:ci\..*'` (each regex matches a whole key) |
| Preview the order, run nothing | Add `--dry-run` to `run` or an alias |
| Run one command in the toolchain | `stew exec [project] '<command>'` |
| Full logs of the last run | `stew runs show latest --no-pager` |
| Logs of one section | `stew runs show latest 'core:build' --no-pager` |
| Section results for scripts | `stew runs show latest --porcelain` |
| Past runs | `stew runs list --no-pager` |
| Delete old runs | `stew runs prune --keep-last-n 20` or `--keep-since 7d` |
| Run trust commands (after consent) | `stew trust --yes` |
| Register or unregister a project | `stew add <path> [-a <name>]`, `stew remove <name>... [--clean]` |
| Set up a new worktree or clone | `stew setup-worktree` |
| Install a git hook | `stew git install pre-commit\|pre-push\|post-checkout` |
| Update this skill after upgrading `stew` | `stew skills install [<dir>]` (default `.agents/skills` in the root, else cwd) |

## Common Mistakes

| Mistake | Fix |
| --- | --- |
| Running `stew trust --yes` without asking | Show the trust commands and get consent first. |
| Running `go test`, `npm test`, etc. directly | Use a section, or `stew exec <project> '<cmd>'` so the wrapper applies. |
| Guessing why a section failed | Read `stew runs show latest --no-pager` or the section's `.log` file. |
| Fixing a `blocked` section | Follow `blocked by` to the section that failed and fix that. |
| `stew run build` exits 2 | A regex matches the whole key. Use `stew build` or `stew run '.*:build'`. |
| Staging `.stew/runs/` or `.stew/trust.json` | Both are per-tree and ignored by `.stew/.gitignore`. Never commit them. |
| A step starts a server and leaves it running | stew stops leftovers when the step ends. Start and stop it in one step. |
| `stew runs show` opens a pager | Pass `--no-pager` or `--porcelain`. |

## References

Read the file that matches the task:

- `references/commands.md`: every command, flag, output, and error.
- `references/configuration.md`: workspace layout, `.stew/config.toml`, `stew.toml`, sections, `requires`.
- `references/execution.md`: selection and order, the section steps, statuses, environment, wrappers, trust,
  interrupts.
- `references/runs.md`: live output, the summary, run logs, `stew runs show`, `list`, and `prune`.
- `references/git.md`: git hooks, new worktrees, fresh clones, and CI.
