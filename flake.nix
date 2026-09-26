{
  description = "Stack-agnostic monorepo orchestrator";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: {
        default = pkgs.buildGoModule {
          pname = "stew";
          version = self.shortRev or self.dirtyShortRev or "dev";
          src = self;
          vendorHash = "sha256-00+RUk8GvTbFmnMXqtl0HeG3m7inwfA1MYDsWhkJSKA=";
          doCheck = false;
          meta.mainProgram = "stew";
        };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.git
            (pkgs.writeShellScriptBin "stew" ''
              src=$PWD
              until [ "$(sed -n 1p "$src/go.mod" 2>/dev/null)" = "module github.com/rknit/steward" ]; do
                if [ "$src" = / ]; then
                  src="''${STEWARD_SRC:?stew: no steward checkout above $PWD and STEWARD_SRC is unset}"
                  break
                fi
                src=$(dirname "$src")
              done
              go build -C "$src" -buildvcs=false -o "$src/.direnv/bin/stew" ./cmd/stew || exit
              exec "$src/.direnv/bin/stew" "$@"
            '')
          ];
          shellHook = ''
            export STEWARD_SRC="$(git rev-parse --show-toplevel)"
          '';
        };
      });
    };
}
