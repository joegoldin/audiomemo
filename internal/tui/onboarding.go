package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joegoldin/audiotools/internal/config"
	"github.com/joegoldin/audiotools/internal/record"
)

// ---------------------------------------------------------------------------
// State machine
// ---------------------------------------------------------------------------

type onboardState int

const (
	OBLoading     onboardState = iota
	OBPickDevice
	OBAliasPrompt
	OBDone
)

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type onboardModel struct {
	state      onboardState
	devices    []record.Device // filtered to sources only
	cursor     int
	aliasInput simpleInput
	config     *config.Config
	configPath string
	completed  bool
	message    string
	width      int
	height     int
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RunOnboarding launches the first-time onboarding TUI. It returns whether
// onboarding completed successfully (a device was selected and saved).
func RunOnboarding(cfg *config.Config, configPath string) (completed bool, err error) {
	if cfg.Devices == nil {
		cfg.Devices = map[string]string{}
	}
	if cfg.DeviceGroups == nil {
		cfg.DeviceGroups = map[string][]string{}
	}

	m := &onboardModel{
		state:      OBLoading,
		config:     cfg,
		configPath: configPath,
		aliasInput: newSimpleInput("alias (optional)"),
	}

	p := tea.NewProgram(m, tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		return false, err
	}
	if fm, ok := finalModel.(*onboardModel); ok {
		return fm.completed, nil
	}
	return false, nil
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func (m *onboardModel) Init() tea.Cmd {
	return m.loadDevices
}

func (m *onboardModel) loadDevices() tea.Msg {
	devices, err := record.ListDevices()
	if err != nil {
		return devicesLoadedMsg(nil)
	}
	return devicesLoadedMsg(devices)
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m *onboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case devicesLoadedMsg:
		// Filter to source devices only (not monitors).
		var sources []record.Device
		for _, d := range msg {
			if !d.IsMonitor {
				sources = append(sources, d)
			}
		}
		m.devices = sources

		if len(m.devices) == 0 {
			m.message = "No input devices found."
			m.completed = false
			m.state = OBDone
			return m, tea.Quit
		}

		m.state = OBPickDevice
		return m, nil

	case obSavedMsg:
		if msg.err != nil {
			m.message = fmt.Sprintf("Error saving config: %v", msg.err)
		}
		return m, tea.Quit

	case tea.MouseMsg:
		if m.state == OBPickDevice {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				if m.cursor > 0 {
					m.cursor--
				}
			case tea.MouseButtonWheelDown:
				if m.cursor < len(m.devices)-1 {
					m.cursor++
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (m *onboardModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global quit.
	if msg.String() == "ctrl+c" {
		m.completed = false
		return m, tea.Quit
	}

	switch m.state {
	case OBPickDevice:
		return m.handlePickDeviceKey(msg)
	case OBAliasPrompt:
		return m.handleAliasPromptKey(msg)
	}

	return m, nil
}

func (m *onboardModel) handlePickDeviceKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.devices)-1 {
			m.cursor++
		}
	case "enter":
		m.state = OBAliasPrompt
		m.aliasInput.SetValue("")
		m.message = ""
	case "esc":
		m.completed = false
		m.state = OBDone
		return m, tea.Quit
	}
	return m, nil
}

func (m *onboardModel) handleAliasPromptKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "enter":
		return m, m.saveAndFinish(true)
	case "esc":
		// Skip alias but still save the device selection.
		return m, m.saveAndFinish(false)
	default:
		m.aliasInput.HandleKey(keyStr)
	}
	return m, nil
}

// saveAndFinish persists the device selection (and optional alias) to the
// config file and transitions to OBDone. Config mutation happens here in the
// Update goroutine (safe), only the disk I/O runs in the command.
func (m *onboardModel) saveAndFinish(useAlias bool) tea.Cmd {
	dev := m.devices[m.cursor]
	alias := strings.TrimSpace(m.aliasInput.Value())

	if useAlias && alias != "" {
		m.config.Devices[alias] = dev.Name
		m.config.Record.Device = alias
	} else {
		m.config.Record.Device = dev.Name
	}

	m.config.OnboardVersion = config.CurrentOnboardVersion
	m.completed = true
	m.state = OBDone

	cfg := m.config
	configPath := m.configPath
	return func() tea.Msg {
		var err error
		if configPath != "" {
			err = cfg.SaveTo(configPath)
		} else {
			err = cfg.Save()
		}
		return obSavedMsg{err: err}
	}
}

type obSavedMsg struct{ err error }

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m *onboardModel) View() string {
	switch m.state {
	case OBLoading:
		return m.viewLoading()
	case OBPickDevice:
		return m.viewPickDevice()
	case OBAliasPrompt:
		return m.viewAliasPrompt()
	case OBDone:
		return m.viewDone()
	}
	return ""
}

func (m *onboardModel) viewLoading() string {
	return "\n  " + dmDimStyle.Render("Scanning for audio devices...") + "\n"
}

func (m *onboardModel) viewPickDevice() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  " + dmTitleStyle.Render("Select your default input device") + "\n\n")

	for i, d := range m.devices {
		display := d.Description
		if display == "" {
			display = d.Name
		}

		if i == m.cursor {
			b.WriteString("  " + dmSelectedStyle.Render("> "+display) + "\n")
		} else {
			b.WriteString("    " + display + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString("  " + dmDimStyle.Render("[↑/↓] navigate  [enter] select  [esc] skip") + "\n")

	return b.String()
}

func (m *onboardModel) viewAliasPrompt() string {
	var b strings.Builder

	dev := m.devices[m.cursor]

	b.WriteString("\n")
	b.WriteString("  " + dmTitleStyle.Render("Name this device (optional)") + "\n\n")
	b.WriteString("  " + fmt.Sprintf("Device: %s", dmDimStyle.Render(dev.Name)) + "\n\n")
	b.WriteString("  " + fmt.Sprintf("Alias: %s", m.aliasInput.View()) + "\n\n")

	if m.message != "" {
		b.WriteString("  " + dmErrorStyle.Render(m.message) + "\n\n")
	}

	b.WriteString("  " + dmDimStyle.Render("[enter] save  [esc] skip alias") + "\n")

	return b.String()
}

func (m *onboardModel) viewDone() string {
	if m.message != "" {
		return "\n  " + m.message + "\n"
	}
	return ""
}
