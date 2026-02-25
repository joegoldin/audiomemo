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
        vendorHash = "sha256-IFJmM1jacTeNk9qo1An/FGi/BdjesasSNAXTdj/LBIM=";
        whisperModel = pkgs.fetchurl {
          url = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin";
          hash = "sha256-YO1bw90U7qhWST0zQ0m0BXgt3K8AKNS130CINF+6Lv4=";
        };
        audiomemo = pkgs.buildGoModule {
          pname = "audiomemo";
          version = "0.1.0";
          src = ./.;
          inherit vendorHash;
          nativeBuildInputs = [ pkgs.makeWrapper pkgs.installShellFiles ];
          postInstall = ''
            ln -s $out/bin/audiomemo $out/bin/record
            ln -s $out/bin/audiomemo $out/bin/rec
            ln -s $out/bin/audiomemo $out/bin/transcribe
            wrapProgram $out/bin/audiomemo \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.ffmpeg pkgs.whisper-cpp ]}

            installShellCompletion --cmd audiomemo \
              --bash <($out/bin/audiomemo completion bash) \
              --fish <($out/bin/audiomemo completion fish) \
              --zsh  <($out/bin/audiomemo completion zsh)
          '';
        };
      in
      {
        packages.default = audiomemo;
        packages.audiomemo = audiomemo;

        checks.default = pkgs.buildGoModule {
          pname = "audiomemo-tests";
          version = "0.1.0";
          src = ./.;
          inherit vendorHash;
          nativeBuildInputs = [ pkgs.ffmpeg pkgs.whisper-cpp ];
          doCheck = true;
          preCheck = ''
            export HOME=/tmp/audiomemo-test-home
            mkdir -p $HOME/.local/share/whisper-cpp
            cp ${whisperModel} $HOME/.local/share/whisper-cpp/ggml-base.bin
          '';
          installPhase = ''
            touch $out
          '';
        };

        devShells.default = pkgs.mkShell {
          buildInputs = [ pkgs.go pkgs.gopls pkgs.ffmpeg pkgs.whisper-cpp ];
        };
      }
    ) // {
      overlays.default = final: prev: {
        audiomemo = self.packages.${final.system}.audiomemo;
      };
      homeManagerModules.default = import ./nix/home-manager.nix;
    };
}
