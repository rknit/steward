# Git Hooks, Worktrees, and CI

## Contents

- `stew git install`
- `pre-commit` and `pre-push`
- `post-checkout` and new worktrees
- A fresh clone
- CI

## `stew git install <hook>`

`<hook>` is `pre-commit`, `pre-push`, or `post-checkout`. Any other name is an error that lists these.

- Run it anywhere inside the workspace. It does not validate the workspace.
- It writes the hook into the repository's hooks directory, which respects `core.hooksPath`. All worktrees of a
  repository share it, so one install covers every worktree.
- An existing hook file is rejected, including one another tool installed. There is no overwrite flag.
- Prints `installed <path>`.
- The hook lives outside the committed tree, unless `core.hooksPath` points into it. Each clone then installs its
  own hooks.
- The hook runs the `stew` found on `PATH`, from the workspace root, even when the root is below the git top level.

## `pre-commit` and `pre-push`

The hook runs `stew ci --level pre-commit` or `stew ci --level pre-push`.

- A failing or blocked section fails the hook, so git refuses the commit or push. Read the output, or
  `stew runs show latest --no-pager`, and fix the cause. Do not bypass the hook with `--no-verify` unless the
  operator asks.
- If no project defines `ci.pre-commit` (or `ci.pre-push`), the hook exits 2.
- The hook checks the working tree, not the staged snapshot. Unstaged edits can change the result.
- A common setup makes the hook level an empty section that requires the real checks:

  ```toml
  [ci.pre-commit]
  run = ""
  requires = ["api:ci.quick"]
  ```

- The hook runs non-interactively. Pending trust fails it with `untrusted:`; run `stew trust` (with consent) first.

## `post-checkout` and New Worktrees

The `post-checkout` hook sets up each new worktree:

1. It acts only when git reports no previous HEAD: a new worktree (`git worktree add`), or a clone whose template
   installs the hook. `git checkout`, `git switch`, and file checkouts exit 0 at once.
2. A commit without `.stew/` prints `stew: no workspace in <dir>, skipping worktree setup` on stderr and exits 0.
3. Otherwise it runs `stew trust --yes`, then `stew setup-worktree` in the new worktree.

- Installing this hook is the consent to trust new worktrees. It runs the checked-out commit's trust commands and
  `worktree.setup` sections without asking.
- `git worktree add` exits with the hook's status and keeps the worktree either way. A failed trust command or
  section gives exit 1. No project defining `worktree.setup` gives exit 2.
- A repository that needs only trust defines `worktree.setup` with `run = ""`.
- After fixing a failure, rerun `stew setup-worktree` in the worktree.
- `git worktree add --no-checkout` runs no hook. Run `stew setup-worktree` after checking out.

Example `worktree.setup`:

```toml
[worktree.setup]
run = "cp .env.example .env"
requires = ["api:setup"]
```

## A Fresh Clone

No hook runs in a fresh clone, and `.stew/trust.json` does not exist, so every trust entry is pending.
To set it up as the hook would, after the operator agrees to the trust commands:
`stew trust --yes && stew setup-worktree`.

## CI

- stew has no terminal in CI, so trust must be given up front: run `stew trust --yes` before the first `stew`
  command that runs sections. The repository's own CI configuration is the consent.
- Run a level: `stew ci` (`ci.full`) or `stew ci -l <level>`.
- Exit 1 fails the job on a failed or blocked section. Exit 2 means a configuration or usage error.
- A cancelled job sends SIGTERM: stew stops the running command and exits 143.
- Keep `.stew/runs/` as a job artifact to inspect the logs later.
