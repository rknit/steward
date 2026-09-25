# AGENTS.md

## Roles

- The operator is the human who requests work and makes workflow, commit, and
  push decisions.
- The coding agent is the AI system carrying out the operator's request.

## Workflow

The operator must explicitly choose one of the available workflows. If no
workflow is specified, the coding agent must ask before making changes.

Only the operator may choose the workflow. The coding agent and its skills must
not choose one on the operator's behalf.

### Work in main

The coding agent works directly in the current main checkout. It must not make
a new branch or worktree.

Rules:

- No commits or pushes without explicit instruction.
- Commit and push approval must be explicitly granted every time.
- Single audience: no compatibility shims, deprecation ceremony, or dead code.

### Worktree

Rules:

- Commit freely within the worktree. These commits need not be standalone.
- No pushes without explicit instruction.
- No commit in main without explicit instruction.
- Commit and push approval must be explicitly granted every time.
- Single audience: no compatibility shims, deprecation ceremony, or dead code.

Workflow:

1. Enter a worktree.
2. Set up everything required by the task.
3. Do the work there.
4. Squash the commits into standalone commits (see Standalone Commits).
   Dropping them one at a time from latest to oldest must leave every step
   standalone. The coding agent decides the grouping.
5. Leave the worktree so git can reach main.
6. Cherry-pick the commits onto main.

## Planning Horizon

Make decisions that hold up long term, not ones that only unblock the current
task.

- Choose data models, interfaces, and file layouts that will not need rework
  when the next likely features land.
- Do not build features, options, or abstractions for needs nobody has stated.
- If you are unsure how far ahead to plan, ask the operator before settling on
  a design.

## Git Commit Convention

Follow [Conventional Commits](https://www.conventionalcommits.org/).

Single-line format only:

```text
<type>: <short description>
```

Types:

- `feat` — new functionality
- `fix` — bug fix
- `refactor` — restructuring without behavior change
- `chore` — build, config, CI/CD
- `docs` — documentation work

Rules:

- Lowercase description.
- No period at the end.
- One line only.
- No body.

## Standalone Commits

Commits on a branch or in a worktree may be of any kind: work in progress,
partial, or broken.

Every commit that lands on main must be standalone:

- The tree builds and all tests pass at that commit.
- Each feature it touches is fully implemented and usable.
- No stubs, placeholder branches, unused scaffolding, or code that only a later
  commit makes reachable or correct.

If a feature is too large for one commit, split it into smaller features that
each work end to end on their own. Do not split it into layers or steps.

## Code Comments

Default to no comments across all kinds of scripts. Code should be
self-documenting through names and structure.

Rules:

- No comments restating what the code does.
- Comment only when the information is genuinely surprising.
- Prefer fixing the code with better names and smaller units over explaining it.
- No narrative comments, progress notes, or task/issue references.
