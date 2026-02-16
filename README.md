# go-check-flake

## Usage
```
go-check-flake [flags] [flake_input]
```
`flake_input` defaults to nixpkgs and is ignored when `-a` or `-A` is used.

### Flags:
- `-A`	Check all inputs but ONLY show outdated ones
- `-a`	Check all inputs and show all results

## Installation
You can use it directly without installation:
```sh
nix run github:maxschipper/go-check-flake -- -a
```

Or install it:

+ add the flake to your inputs:
  ```nix
  {
    inputs = {
      go-check-flake = {
        url = "github:maxschipper/go-check-flake";
        inputs.nixpkgs.follows = "nixpkgs";
      };
    };
  }
  ```

+ install the provided package somewhere in your config:
    ```nix
    enviroment.systemPackages = [
      inputs.go-check-flake.packages.x86_64-linux.default
    ]
    ```

