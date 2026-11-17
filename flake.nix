{
  description = "Read-only, anonymous MCP server for doctolib.fr";

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
          pname = "doctolib-mcp";
          version = self.shortRev or self.dirtyShortRev or "dirty";
          src = self;
          vendorHash = "sha256-K8illBSpXyRfqoN88kpC662Jt9rL2D4gv+hIsxZQ9bg=";
          # Keep the User-Agent's Chrome version in step with nixpkgs' chromium.
          ldflags = [
            "-s"
            "-w"
            "-X github.com/tio-plato/doctolib-mcp/internal/doctolib.defaultChromeMajor=${pkgs.lib.versions.major pkgs.chromium.version}"
          ];
          meta = {
            description = "Read-only, anonymous MCP server for doctolib.fr";
            homepage = "https://github.com/tio-plato/doctolib-mcp";
            mainProgram = "doctolib-mcp";
          };
        };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = [
            pkgs.go
            pkgs.gopls
            pkgs.golangci-lint
          ];
        };
      });

      checks = forAllSystems (
        pkgs:
        let
          package = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
        in
        {
          inherit package;
          # Reuses the package's vendored modules, so the lint runs offline in the sandbox.
          lint = package.overrideAttrs (old: {
            pname = "doctolib-mcp-lint";
            nativeBuildInputs = old.nativeBuildInputs ++ [ pkgs.golangci-lint ];
            buildPhase = ''
              runHook preBuild
              export HOME=$TMPDIR GOLANGCI_LINT_CACHE=$TMPDIR/golangci-lint
              golangci-lint run ./...
              runHook postBuild
            '';
            doCheck = false;
            installPhase = ''
              runHook preInstall
              touch $out
              runHook postInstall
            '';
          });
        }
      );
    };
}
