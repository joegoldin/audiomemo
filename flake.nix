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

        checks.default = pkgs.stdenv.mkDerivation {
          name = "audiotools-tests";
          src = ./.;
          nativeBuildInputs = [ pkgs.go pkgs.ffmpeg pkgs.whisper-cpp ];
          # Pre-populate go module cache from the vendor dir
          GO111MODULE = "on";
          GOFLAGS = "-mod=vendor";
          # whisper-cpp model for integration tests
          HOME = "/tmp/audiotools-test-home";
          buildPhase = ''
            export GOCACHE=$TMPDIR/go-cache
            export GOPATH=$TMPDIR/go

            # Set up whisper-cpp model for integration tests
            mkdir -p /tmp/audiotools-test-home/.local/share/whisper-cpp
            cp ${pkgs.fetchurl {
              url = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin";
              hash = "sha256-IN0Gm8bVzPbflJtqIVbuQmsqbiJFOL+kTCAksFmhdSU=";
            }} /tmp/audiotools-test-home/.local/share/whisper-cpp/ggml-base.bin

            go test -v -count=1 -timeout 300s ./...
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
