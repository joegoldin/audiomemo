{
  description = "Audio recording and transcription CLI tools";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        nemo-speech = pkgs.callPackage ./nix/nemo-speech.nix { };
        vendorHash = "sha256-++rCNa9MkMYY5RDkHijb5GFc1Y/zkGMo1UmzdQGpOT8=";
        whisperModel = pkgs.fetchurl {
          url = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin";
          hash = "sha256-YO1bw90U7qhWST0zQ0m0BXgt3K8AKNS130CINF+6Lv4=";
        };
        toolPath = pkgs.lib.makeBinPath [
          pkgs.ffmpeg
          pkgs.whisper-cpp
          nemo-speech
        ];
        # The Nix build namespace can report / as owned by an unmapped UID.
        # Give test processes a private root rather than relaxing socket checks.
        linuxTestRoot = pkgs.writeShellScript "audiomemo-test-root" ''
          exec ${pkgs.bubblewrap}/bin/bwrap --unshare-user \
            --ro-bind /nix /nix --bind "$NIX_BUILD_TOP" "$NIX_BUILD_TOP" \
            --ro-bind /bin /bin --ro-bind /etc /etc --tmpfs /tmp \
            --proc /proc --dev-bind /dev /dev --chdir "$PWD" -- "$@"
        '';
        testFlags = pkgs.lib.optionals pkgs.stdenv.hostPlatform.isLinux [ "-exec=${linuxTestRoot}" ];
        plasmaHelper = pkgs.stdenv.mkDerivation {
          pname = "audiomemo-plasma";
          version = "0.2.0";
          src = ./desktop/linux;
          nativeBuildInputs = [
            pkgs.cmake
            pkgs.pkg-config
            pkgs.wayland-scanner
            pkgs.python3
            pkgs.kdePackages.wrapQtAppsHook
          ];
          buildInputs = [
            pkgs.wayland
            pkgs.wayland-protocols
            pkgs.kdePackages.qtbase
            pkgs.kdePackages.qtwayland
            pkgs.kdePackages.layer-shell-qt
            pkgs.kdePackages.kstatusnotifieritem
          ];
          cmakeFlags = [ "-DAUDIOMEMO_DESKTOP_TESTS=ON" ];
          doCheck = true;
          dontWrapQtApps = true;
          postFixup = ''wrapQtApp "$out/libexec/audiomemo-desktop-linux"'';
        };
        audiomemo = pkgs.buildGoModule {
          pname = "audiomemo";
          version = "0.2.0";
          src = ./.;
          inherit vendorHash;
          checkFlags = testFlags;
          ldflags = [
            "-X github.com/joegoldin/audiomemo/cmd.version=0.2.0"
          ]
          ++ pkgs.lib.optionals pkgs.stdenv.hostPlatform.isDarwin [ "-X main.desktopToolPath=${toolPath}" ];
          nativeBuildInputs = [
            pkgs.makeWrapper
            pkgs.installShellFiles
          ];
          postInstall =
            pkgs.lib.optionalString pkgs.stdenv.hostPlatform.isDarwin ''
              app="$out/Applications/AudioMemo.app/Contents"
              install -Dm755 "$out/bin/audiomemo" "$app/MacOS/audiomemo"
              install -m644 desktop/macos/Info.plist "$app/Info.plist"
            ''
            + pkgs.lib.optionalString pkgs.stdenv.hostPlatform.isLinux ''
              mkdir -p "$out/libexec"
              ln -s "${plasmaHelper}/libexec/audiomemo-desktop-linux" "$out/libexec/"
            ''
            + ''
              installManPage man/audiomemo.1
              ln -s $out/bin/audiomemo $out/bin/record
              ln -s $out/bin/audiomemo $out/bin/rect
              ln -s $out/bin/audiomemo $out/bin/recw
              ln -s $out/bin/audiomemo $out/bin/scribe
              ln -s $out/bin/audiomemo $out/bin/transcribe
              wrapProgram $out/bin/audiomemo \
                --prefix PATH : ${toolPath}

              installShellCompletion --cmd audiomemo \
                --bash <($out/bin/audiomemo completion bash) \
                --fish <($out/bin/audiomemo completion fish) \
                --zsh  <($out/bin/audiomemo completion zsh)
            '';
        };
      in
      {
        packages = {
          default = audiomemo;
          inherit audiomemo nemo-speech;
        };
        checks.nemo-speech = nemo-speech;

        checks.default = pkgs.buildGoModule {
          pname = "audiomemo-tests";
          version = "0.2.0";
          src = ./.;
          inherit vendorHash;
          checkFlags = testFlags;
          nativeBuildInputs = [
            pkgs.ffmpeg
            pkgs.whisper-cpp
            nemo-speech
          ];
          doCheck = true;
          AUDIOMEMO_NEMO_TEST_BINARY = "${nemo-speech}/bin/nemo-speech";
          AUDIOMEMO_NEMO_TEST_DEVICE = "cpu";
          preCheck = ''
            export HOME="$TMPDIR/audiomemo-test-home"
            mkdir -p "$HOME/.local/share/whisper-cpp"
            cp ${whisperModel} "$HOME/.local/share/whisper-cpp/ggml-base.bin"
          '';
          installPhase = ''
            touch $out
          '';
        };

        devShells.default = pkgs.mkShell {
          buildInputs = [
            nemo-speech
            pkgs.go
            pkgs.gopls
            pkgs.ffmpeg
            pkgs.whisper-cpp
          ];
        };
      }
    )
    // {
      overlays.default = final: prev: {
        audiomemo = self.packages.${final.system}.audiomemo;
      };
      homeManagerModules.default = import ./nix/home-manager.nix;
    };
}
