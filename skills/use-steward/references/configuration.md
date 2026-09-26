# Configuration

## Contents

- Workspace layout
- `.stew/config.toml`
- `.stew/projects.toml`
- `stew.toml`
- Section names
- `requires`
- Wrapper strings
- Validation
- The `stew add` template

## Workspace Layout

```text
<root>/
  .stew/
    .gitignore      # committed; holds runs/ and trust.json
    config.toml     # committed; workspace wrapper and trust command
    projects.toml   # committed; registered project paths
    trust.json      # ignored; this tree's trust record
    runs/           # ignored; one directory per run
  libs/core/stew.toml
  services/api/stew.toml
```

- The root is the nearest ancestor of the current directory that contains `.stew/`. Every command except
  `stew init` looks for it. None found: `not a stew workspace`.
- Nested workspaces are allowed, as with git. The nearest `.stew/` wins.
- Projects exist only after `stew add`. There is no auto-discovery.
- Hand-edit `config.toml` and each `stew.toml`. Change the registry with `stew add` and `stew remove`.

## `.stew/config.toml`

```toml
# Wraps every command stew runs. {{STEW_STEP}} marks where the command goes. "" means none.
workspace_wrapper = "direnv exec . {{STEW_STEP}}"
# Makes the wrapper usable in a new tree, e.g. "direnv allow .". Runs once per tree, with consent. "" means none.
workspace_trust = "direnv allow ."
```

| Key | Required | Rule |
| --- | --- | --- |
| `workspace_wrapper` | yes | String. `""` means no wrapper. Otherwise see Wrapper Strings. |
| `workspace_trust` | yes | String. `""` means no trust command. |

- A missing file is an error. Unknown keys are rejected.

## `.stew/projects.toml`

```toml
projects = ["libs/core", "services/api"]
```

- Root-relative, `/`-separated project paths, sorted and unique. `"."` registers the root itself.
- Paths must be relative, must not contain `..`, and must stay inside the root.
- Names are not stored here. A project's name comes only from its `stew.toml`.

## `stew.toml`

```toml
name = "api"
project_wrapper = ""
project_trust = ""

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

| Key | Required | Rule |
| --- | --- | --- |
| `name` | yes | Unique in the workspace. Matches `[a-z0-9][a-z0-9._-]*`. Never contains `:`. |
| `project_wrapper` | yes | String. `""` means none. Nested inside the workspace wrapper. |
| `project_trust` | yes | String. `""` means none. |
| any other table | no | A section, or a namespace of sections. A project may have none. |

Keys inside a section:

| Key | Required | Rule |
| --- | --- | --- |
| `run` | yes | String. `""` allowed. |
| `skip_if` | no | String. Exit 0 skips the section. Absent and `""` both mean none. |
| `verify` | no | String. Runs after `run`; must exit 0. Absent and `""` both mean none. |
| `requires` | no | List of keys that must succeed first. Absent means `[]`. |

- There is no `dependencies` key. `stew list` derives project dependencies from `requires`.
- A section with `run = ""` and only `requires` groups other sections. It runs nothing and ends `skip` once its
  requirements pass. `ci.pre-commit` above is one.
- Multi-line commands work: use a TOML multi-line string. Each step runs as one `sh -c` script.

## Section Names

- A dotted table is a dotted name: `[ci.full]` is section `ci.full`.
- A table is a section when it holds `run`, `skip_if`, `verify`, or `requires`. Otherwise it is a namespace and
  holds only tables.
- A table cannot be both. `[ci]` with `run`, plus `[ci.full]`: `[ci]: a section cannot contain sections`.
- An empty table defines no section.
- Each segment matches `[a-z0-9][a-z0-9_-]*`. A quoted key such as `["ci.full"]` is rejected.
- Conventional names used by the aliases and hooks: `setup`, `build`, `ci.<level>` (`ci.full` is the `stew ci`
  default), `ci.pre-commit`, `ci.pre-push`, and `worktree.setup`. None is required.

## `requires`

- Each entry is a key: `"<project>:<section>"`. A section of the same project still names its project:
  `"api:setup"`.
- Load checks, with the error each gives:

  | Problem | Error |
  | --- | --- |
  | Not one `:` | `<file>: [build]: requires "core": want <project>:<section>` |
  | Unknown project | `... requires "core:build": unknown project "core"` |
  | Unknown section | `... requires "core:build": core has no section "build"` |
  | Names itself | `... requires "api:build": section requires itself` |
  | Listed twice | `... duplicate requires "core:build"` |
  | Cycle | `cycle: api:build -> core:build -> api:build` |

- Blocking follows `requires` edges only. Sections of one project do not depend on each other unless one requires
  another.

## Wrapper Strings

Applies to `workspace_wrapper` and `project_wrapper`.

- `{{STEW_STEP}}` must appear exactly once. Error:
  `<file>: <key>: must contain {{STEW_STEP}} exactly once (found N)`.
- The wrapper must pass a shell syntax check (`sh -n`). The error names the file, the key, and the shell's
  message.
- See `execution.md` for how wrappers run and what makes a wrapper fail at runtime.

## Validation

Every command except `init`, `git install`, and `runs *` loads and validates the whole workspace first. Any error
stops the command before anything runs, with exit 2 and a message that names the file:

- `projects.toml` parses; paths are valid and unique; every path has a `stew.toml`.
- `config.toml` parses; both keys present; wrapper checks pass.
- Every `stew.toml` parses with no unknown keys (`<file>: [build]: unknown key "foo"`), no missing required keys
  (`<file>: [build]: missing key "run"`), valid section names, and valid wrapper and `requires` entries.
- Every `name` is valid and unique.
- The section graph has no cycle.

`stew runs show`, `list`, and `prune` read only `.stew/runs/`, so they work while a `stew.toml` is broken.

## The `stew add` Template

For a directory without `stew.toml`, `stew add` writes:

```toml
name = "<name>"
# Wraps every command of this project, inside the workspace wrapper.
# {{STEW_STEP}} marks where the command goes. "" means none.
project_wrapper = ""
# Makes the project wrapper usable in a new tree, e.g. "mise trust". Runs once per tree, with consent.
# "" means none.
project_trust = ""

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

It defines no section. Add sections by hand.
