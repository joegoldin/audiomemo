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
            default_backend = "deepgram";
            deepgram = {
              api_key_file = config.age.secrets.deepgram_api_key.path;
              model = "nova-3";
              smart_format = true;
            };
          };
        }
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];

    xdg.configFile."audiomemo/config.toml" = {
      source = configFile;
    };
  };
}
