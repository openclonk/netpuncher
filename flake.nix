{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  inputs.flake-utils.url = "github:numtide/flake-utils";
  inputs.gomod2nix.url = "github:nix-community/gomod2nix";
  inputs.gomod2nix.inputs.nixpkgs.follows = "nixpkgs";
  inputs.gomod2nix.inputs.flake-utils.follows = "flake-utils";

  outputs = { self, nixpkgs, flake-utils, gomod2nix }:
    (flake-utils.lib.eachDefaultSystem
      (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          # https://github.com/nix-community/gomod2nix/pull/168
          go = pkgs.go_1_22;
        in
        {
          packages.default = pkgs.callPackage ./. {
            inherit (gomod2nix.legacyPackages.${system}) buildGoApplication;
            go = go;
          };
          devShells.default = pkgs.callPackage ./shell.nix {
            inherit (gomod2nix.legacyPackages.${system}) mkGoEnv gomod2nix;
            go = go;
          };
        })
    );
}
