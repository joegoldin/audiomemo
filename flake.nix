{
  description = "Audio recording and transcription CLI tools";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        audiotools = pkgs.buildGoModule {
          pname = "audiotools";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-IFJmM1jacTeNk9qo1An/FGi/BdjesasSNAXTdj/LBIM=";
          nativeBuildInputs = [ pkgs.makeWrapper pkgs.installShellFiles ];
          postInstall = ''
            ln -s $out/bin/audiotools $out/bin/record
            ln -s $out/bin/audiotools $out/bin/rec
            ln -s $out/bin/audiotools $out/bin/transcribe
            wrapProgram $out/bin/audiotools \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.ffmpeg ]}

            installShellCompletion --cmd audiotools \
              --bash <($out/bin/audiotools completion bash) \
              --fish <($out/bin/audiotools completion fish) \
              --zsh  <($out/bin/audiotools completion zsh)
          '';
        };
      in
      {
        packages.default = audiotools;
        packages.audiotools = audiotools;
        devShells.default = pkgs.mkShell {
          buildInputs = [ pkgs.go pkgs.gopls pkgs.ffmpeg ];
        };
      }
    ) // {
      overlays.default = final: prev: {
        audiotools = self.packages.${final.system}.audiotools;
      };
    };
}
