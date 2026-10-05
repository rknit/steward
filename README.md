# steward

`stew` is a stack-agnostic monorepo orchestrator. It runs setup, build, and CI sections for
explicitly registered projects, in dependency order, with per-run logs.

## Install

```sh
go install github.com/rknit/steward/cmd/stew@latest
```

Or with Nix:

```sh
nix profile install github:rknit/steward
```

## Quick start

```sh
stew init                     # create .stew/ at the workspace root
stew add app                  # register ./app and create app/stew.toml
```

Add a section to `app/stew.toml`:

```toml
[build]
run = "go build ./..."
```

Then run it:

```sh
stew build                    # run every project's build section
stew git install pre-commit   # run the ci.pre-commit level before each commit
```

`stew --help` lists every command. `stew skills install` teaches AI agents to use `stew`.

## License

[Apache-2.0](LICENSE)
