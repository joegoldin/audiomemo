{
  lib,
  stdenv,
  fetchurl,
  autoPatchelfHook,
  makeWrapper,
  writeShellScript,
  coreutils,
  curl,
  vulkan-loader,
  mesa,
}:
let
  releases = {
    aarch64-darwin = {
      platform = "macos-aarch64-metal";
      hash = "sha256-XLArp8BPC1WFzlzenIML5fC337TIMIPFABCcOBtPLak=";
    };
    x86_64-darwin = {
      platform = "macos-x86_64-cpu";
      hash = "d52c318d8197511d4f7eee02bcf05511f7e91b300555a130b562e571695a0f39";
    };
    aarch64-linux = {
      platform = "linux-aarch64-vulkan";
      hash = "370f69391db9a8f1d112e1baaa7f1f738776bd88fd4f4e2d61f5d6489d061126";
    };
    x86_64-linux = {
      platform = "linux-x86_64-vulkan";
      hash = "c8d0ed5b9ac5fb6d5c460015ba38ea217415c44edc1e6d49120646781009aee6";
    };
  };
  release = releases.${stdenv.hostPlatform.system};
  models = import ./nemo-models.nix { inherit fetchurl; };
  # NeMo needs a writable cache for locks and verification markers. Seed links
  # instead of pointing MODEL_DIR at the read-only store or copying 1.5 GB.
  seedModels = writeShellScript "nemo-speech-seed-models" ''
    set -eu
    cache="''${NEMO_SPEECH_MODEL_DIR:-${
      if stdenv.hostPlatform.isDarwin then
        "$HOME/Library/Caches/NeMoSpeech/models"
      else
        "\${XDG_CACHE_HOME:-$HOME/.cache}/nemo-speech/models"
    }}"
    ${lib.concatMapStringsSep "\n" (model: ''
      directory="$cache/${model.repo}/${model.revision}"
      ${coreutils}/bin/mkdir -p "$directory"
      target="$directory/${model.filename}"
      if [ ! -e "$target" ]; then
        ${coreutils}/bin/ln -s ${model.file} "$target" || test -e "$target"
      fi
    '') models}
  '';
in
stdenv.mkDerivation (finalAttrs: {
  pname = "nemo-speech";
  version = "0.2.0";

  src = fetchurl {
    url = "https://github.com/NVIDIA/NeMo-Speech.cpp/releases/download/v${finalAttrs.version}/nemo-speech-${finalAttrs.version}-${release.platform}.tar.gz";
    sha256 = release.hash;
  };

  nativeBuildInputs = [ makeWrapper ] ++ lib.optional stdenv.hostPlatform.isLinux autoPatchelfHook;
  buildInputs = lib.optionals stdenv.hostPlatform.isLinux [
    stdenv.cc.cc.lib
    vulkan-loader
  ];
  dontBuild = true;
  # Preserve upstream's signed Mach-O binaries and loader-relative dylib paths.
  dontStrip = true;
  dontFixDarwinDylibNames = true;

  installPhase = ''
    runHook preInstall
    mkdir -p "$out"
    cp -R bin lib share "$out/"
    runHook postInstall
  '';

  postFixup = ''
    wrapProgram "$out/bin/nemo-speech" \
      --prefix PATH : ${lib.makeBinPath [ curl ]} \
      --run ${seedModels}
  '';

  doInstallCheck = true;
  installCheckPhase = ''
    runHook preInstallCheck
    export HOME="$TMPDIR/nemo-home"
    ${lib.optionalString stdenv.hostPlatform.isLinux ''
      # Build sandboxes have no GPU; exercise Vulkan with Mesa's software ICD.
      export VK_DRIVER_FILES="$(find ${mesa}/share/vulkan/icd.d -name '*lvp*' -print -quit)"
      test -n "$VK_DRIVER_FILES"
      # ggml excludes CPU Vulkan devices from automatic enumeration.
      export GGML_VK_VISIBLE_DEVICES=0
    ''}
    if ! "$out/bin/nemo-speech" doctor > doctor.txt 2>&1; then
      cat doctor.txt
      exit 1
    fi
    cat doctor.txt
    for feature in asr diarization http realtime_websocket; do
      grep -w "$feature" doctor.txt
    done
    for model in nemotron-en parakeet-tdt nemotron-3-diarization; do
      "$out/bin/nemo-speech" pull "$model"
    done
    runHook postInstallCheck
  '';

  meta = {
    description = "Native speech recognition, streaming and diarization runtime";
    homepage = "https://github.com/NVIDIA/NeMo-Speech.cpp";
    license = lib.licenses.asl20;
    platforms = builtins.attrNames releases;
    sourceProvenance = [ lib.sourceTypes.binaryNativeCode ];
    mainProgram = "nemo-speech";
  };
})
