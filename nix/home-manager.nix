{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.audiomemo;
  tomlFormat = pkgs.formats.toml { };
  configFile = tomlFormat.generate "audiomemo-config.toml" cfg.settings;
in
{
  options.programs.audiomemo = {
    enable = lib.mkEnableOption "audiomemo audio recording and transcription CLI";

    package = lib.mkPackageOption pkgs "audiomemo" { };

    settings = lib.mkOption {
      type = tomlFormat.type;
      default = { };
      description = ''
        Configuration written to {file}`~/.config/audiomemo/config.toml`.
        See <https://github.com/joegoldin/audiomemo> for available options.

        API keys can be provided via `api_key_file` to read from a file at
        runtime (e.g. an agenix or sops secret path).
      '';
      example = lib.literalExpression ''
        {
          onboard_version = 1;
          record = {
            format = "ogg";
            sample_rate = 48000;
            channels = 1;
            output_dir = "~/Recordings";
          };
          transcribe = {
            default_backend = "elevenlabs";
            elevenlabs = {
              api_key_file = config.age.secrets.elevenlabs_api_key.path;
              model = "scribe_v2";
              diarize = true;
            };
          };
        }
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];

    # The audiomemo TUI persists alias / group / default edits back to
    # config.toml, so it must be a writable file rather than a read-only
    # nix-store symlink. Declaratively overwrite it from the generated
    # settings on every activation (removing any prior file or store symlink
    # first). TUI edits are transient — `settings` is the source of truth.
    home.activation.audiomemoConfig = lib.hm.dag.entryAfter [ "writeBoundary" ] ''
      target="$HOME/.config/audiomemo/config.toml"
      mkdir -p "$(dirname "$target")"
      rm -f "$target"
      install -m 0644 ${configFile} "$target"
    '';
  };
}
