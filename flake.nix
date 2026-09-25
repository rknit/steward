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
              root="''${STEW_ROOT:?stew: STEW_ROOT is unset; enter the dev shell from the repo}"
              go build -C "$root" -o "$root/.direnv/bin/stew" ./cmd/stew || exit
              exec "$root/.direnv/bin/stew" "$@"
            '')
          ];
          shellHook = ''
            export STEW_ROOT="$(git rev-parse --show-toplevel)"
          '';
        };
      });
    };
}
