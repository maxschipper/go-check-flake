{
  description = "";
  inputs.nixpkgs.url = "nixpkgs/nixos-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      supportedSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];

      forAllSystems =
        function: nixpkgs.lib.genAttrs supportedSystems (system: function nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            gopls
            gotools
            golangci-lint
            golangci-lint-langserver
          ];
        };
      });

      packages = forAllSystems (pkgs: {
        default = pkgs.buildGoModule {
          pname = "go-check-flake";
          version = "0.0.1";
          src = ./.;
          vendorHash = null;
        };
      });
    };
}
