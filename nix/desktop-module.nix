{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.programs.audiomemo;
  mac = pkgs.stdenv.hostPlatform.isDarwin;
  bin = "${cfg.package}/bin/audiomemo";
  app = "${cfg.package}/Applications/AudioMemo.app/Contents/MacOS/audiomemo";
in
{
  options.programs.audiomemo.desktop = {
    enable = lib.mkEnableOption "the graphical AudioMemo user daemon";
    autoStart = lib.mkEnableOption "AudioMemo at graphical login";
  };
  config = lib.mkIf cfg.enable (
    lib.mkMerge [
      {
        assertions = [
          {
            assertion = !cfg.desktop.autoStart || cfg.desktop.enable;
            message = "AudioMemo desktop.autoStart requires desktop.enable.";
          }
        ];
      }
      (lib.mkIf (cfg.desktop.enable && !mac) {
        systemd.user.services.audiomemo = {
          Unit = {
            Description = "AudioMemo desktop dictation and meetings";
            PartOf = [ "graphical-session.target" ];
            After = [ "graphical-session-pre.target" ];
          };
          Service = {
            ExecStart = "${bin} daemon";
            Restart = "on-failure";
            RestartSec = 5;
            UMask = "0077";
            TimeoutStopSec = 20;
          };
          Install.WantedBy = lib.optional cfg.desktop.autoStart "graphical-session.target";
        };
      })
      (lib.mkIf (cfg.desktop.enable && mac && cfg.desktop.autoStart) {
        launchd.agents.audiomemo = {
          enable = true;
          config = {
            Label = "com.joegoldin.audiomemo";
            ProgramArguments = [
              app
              "daemon"
            ];
            LimitLoadToSessionType = "Aqua";
            RunAtLoad = true;
            KeepAlive.SuccessfulExit = false;
            ThrottleInterval = 5;
            Umask = 63;
          };
        };
      })
    ]
  );
}
