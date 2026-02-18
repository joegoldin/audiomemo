package cmd

import (
	"fmt"
	"strings"

	"github.com/joegilkes/audiotools/internal/config"
	"github.com/joegilkes/audiotools/internal/record"
	"github.com/joegilkes/audiotools/internal/tui"
	"github.com/spf13/cobra"
)

var dConfig string

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Manage audio devices",
	Long:  "List devices, create aliases, manage groups, and set defaults.\n\nRun without a subcommand to open the interactive device manager.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadDeviceConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		return tui.RunDeviceManager(cfg, dConfig)
	},
}

var deviceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available audio devices",
	RunE:  runDeviceList,
}

var deviceAliasCmd = &cobra.Command{
	Use:   "alias <name> <device>",
	Short: "Create a device alias",
	Args:  cobra.ExactArgs(2),
	RunE:  runDeviceAlias,
}

var deviceGroupCmd = &cobra.Command{
	Use:   "group <name> <alias1,alias2,...>",
	Short: "Create a device group",
	Args:  cobra.ExactArgs(2),
	RunE:  runDeviceGroup,
}

var deviceDefaultCmd = &cobra.Command{
	Use:   "default <name>",
	Short: "Set default recording device",
	Args:  cobra.ExactArgs(1),
	RunE:  runDeviceDefault,
}

func init() {
	deviceCmd.PersistentFlags().StringVar(&dConfig, "config", "", "config file path")

	deviceCmd.AddCommand(deviceListCmd)
	deviceCmd.AddCommand(deviceAliasCmd)
	deviceCmd.AddCommand(deviceGroupCmd)
	deviceCmd.AddCommand(deviceDefaultCmd)
}

func loadDeviceConfig() (*config.Config, error) {
	if dConfig != "" {
		return config.LoadFrom(dConfig)
	}
	return config.Load()
}

func runDeviceList(cmd *cobra.Command, args []string) error {
	cfg, err := loadDeviceConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	devices, err := record.ListDevices()
	if err != nil {
		return fmt.Errorf("failed to list devices: %w", err)
	}

	// Build reverse map: raw device name -> alias name
	aliasLookup := make(map[string]string)
	for alias, raw := range cfg.Devices {
		aliasLookup[raw] = alias
	}

	// Separate sources and monitors
	var sources, monitors []record.Device
	for _, d := range devices {
		if d.IsMonitor {
			monitors = append(monitors, d)
		} else {
			sources = append(sources, d)
		}
	}

	// Print sources
	fmt.Println("SOURCES")
	for _, d := range sources {
		prefix := "    "
		if d.IsDefault {
			prefix = "  > "
		}
		tag := ""
		if alias, ok := aliasLookup[d.Name]; ok {
			tag = fmt.Sprintf("  [%s]", alias)
		}
		fmt.Printf("%s%-40s%s\n", prefix, d.Description, tag)
	}

	// Print monitors
	fmt.Println()
	fmt.Println("MONITORS")
	for _, d := range monitors {
		prefix := "    "
		if d.IsDefault {
			prefix = "  > "
		}
		tag := ""
		if alias, ok := aliasLookup[d.Name]; ok {
			tag = fmt.Sprintf("  [%s]", alias)
		}
		fmt.Printf("%s%-40s%s\n", prefix, d.Name, tag)
	}

	// Print aliases
	if len(cfg.Devices) > 0 {
		fmt.Println()
		fmt.Println("ALIASES")
		for alias, raw := range cfg.Devices {
			fmt.Printf("  %s -> %s\n", alias, raw)
		}
	}

	// Print groups
	if len(cfg.DeviceGroups) > 0 {
		fmt.Println()
		fmt.Println("GROUPS")
		for name, aliases := range cfg.DeviceGroups {
			fmt.Printf("  %s -> %s\n", name, strings.Join(aliases, ", "))
		}
	}

	// Print default
	if cfg.Record.Device != "" {
		fmt.Println()
		fmt.Printf("DEFAULT: %s\n", cfg.Record.Device)
	}

	return nil
}

func runDeviceAlias(cmd *cobra.Command, args []string) error {
	name := args[0]
	deviceName := args[1]

	// Validate: no spaces in alias name
	if strings.ContainsAny(name, " \t") {
		return fmt.Errorf("alias name %q must not contain spaces", name)
	}

	cfg, err := loadDeviceConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Validate: no collision with group names
	if _, exists := cfg.DeviceGroups[name]; exists {
		return fmt.Errorf("name %q is already used as a device group", name)
	}

	if cfg.Devices == nil {
		cfg.Devices = make(map[string]string)
	}
	cfg.Devices[name] = deviceName

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Alias created: %s -> %s\n", name, deviceName)
	return nil
}

func runDeviceGroup(cmd *cobra.Command, args []string) error {
	name := args[0]
	aliasCSV := args[1]

	cfg, err := loadDeviceConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	aliases := strings.Split(aliasCSV, ",")
	for i, a := range aliases {
		aliases[i] = strings.TrimSpace(a)
	}

	// Validate all aliases exist
	for _, alias := range aliases {
		if _, ok := cfg.Devices[alias]; !ok {
			return fmt.Errorf("alias %q not found in config; create it first with 'device alias'", alias)
		}
	}

	if cfg.DeviceGroups == nil {
		cfg.DeviceGroups = make(map[string][]string)
	}
	cfg.DeviceGroups[name] = aliases

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Group created: %s -> %s\n", name, strings.Join(aliases, ", "))
	return nil
}

func runDeviceDefault(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := loadDeviceConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	cfg.Record.Device = name

	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Default device set: %s\n", name)
	return nil
}
