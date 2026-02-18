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
        audiotools = pkgs.buildGoModule {
          pname = "audiotools";
          version = "0.1.0";
          src = ./.;
          inherit vendorHash;
          nativeBuildInputs = [ pkgs.makeWrapper pkgs.installShellFiles ];
          postInstall = ''
            ln -s $out/bin/audiotools $out/bin/record
            ln -s $out/bin/audiotools $out/bin/rec
            ln -s $out/bin/audiotools $out/bin/transcribe
            wrapProgram $out/bin/audiotools \
              --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.ffmpeg pkgs.whisper-cpp ]}

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

        checks.default = pkgs.buildGoModule {
          pname = "audiotools-tests";
          version = "0.1.0";
          src = ./.;
          inherit vendorHash;
          nativeBuildInputs = [ pkgs.ffmpeg pkgs.whisper-cpp ];
          doCheck = true;
          preCheck = ''
            export HOME=/tmp/audiotools-test-home
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
        audiotools = self.packages.${final.system}.audiotools;
      };
    };
}
