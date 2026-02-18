package tui

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joegoldin/audiotools/internal/config"
	"github.com/joegoldin/audiotools/internal/record"
)

// ---------------------------------------------------------------------------
// State machine
// ---------------------------------------------------------------------------

// DeviceManagerState represents the current mode of the device management TUI.
type DeviceManagerState int

const (
	DMBrowse         DeviceManagerState = iota
	DMAliasPrompt                       // typing an alias name
	DMAliasEdit                         // editing an alias (renaming the target device)
	DMAliasBrowse                       // browsing aliases for edit/delete
	DMGroupName                         // typing a group name
	DMGroupSelect                       // multi-selecting aliases for a group
	DMGroupBrowse                       // browsing groups for edit/delete
	DMConfirmDeleteA                    // confirming alias deletion from alias browse
	DMConfirmDeleteG                    // confirming group deletion
	DMTestRecording                     // recording a 3-second test clip
	DMTestPlayback                      // playing back the test clip
)

// ---------------------------------------------------------------------------
// Simple inline text input (avoids extra vendor dependency)
// ---------------------------------------------------------------------------

type simpleInput struct {
	value       string
	placeholder string
	charLimit   int
	cursorPos   int
}

func newSimpleInput(placeholder string) simpleInput {
	return simpleInput{
		placeholder: placeholder,
		charLimit:   32,
	}
}

func (si *simpleInput) Value() string { return si.value }

func (si *simpleInput) SetValue(v string) {
	si.value = v
	si.cursorPos = len(v)
}

func (si *simpleInput) HandleKey(keyStr string) {
	switch keyStr {
	case "backspace":
		if si.cursorPos > 0 {
			si.value = si.value[:si.cursorPos-1] + si.value[si.cursorPos:]
			si.cursorPos--
		}
	case "delete":
		if si.cursorPos < len(si.value) {
			si.value = si.value[:si.cursorPos] + si.value[si.cursorPos+1:]
		}
	case "left":
		if si.cursorPos > 0 {
			si.cursorPos--
		}
	case "right":
		if si.cursorPos < len(si.value) {
			si.cursorPos++
		}
	case "home", "ctrl+a":
		si.cursorPos = 0
	case "end", "ctrl+e":
		si.cursorPos = len(si.value)
	default:
		// Insert printable characters (single rune keys)
		if len(keyStr) == 1 && keyStr[0] >= 32 && keyStr[0] < 127 {
			if si.charLimit > 0 && len(si.value) >= si.charLimit {
				return
			}
			si.value = si.value[:si.cursorPos] + keyStr + si.value[si.cursorPos:]
			si.cursorPos++
		}
	}
}

func (si *simpleInput) View() string {
	if si.value == "" {
		return dmDimStyle.Render(si.placeholder) + dmCursorStyle.Render("_")
	}
	before := si.value[:si.cursorPos]
	after := si.value[si.cursorPos:]
	cursor := dmCursorStyle.Render("_")
	return before + cursor + after
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

type dmVUMsg float64                   // live VU level for selected device
type dmTestDoneMsg struct{ err error } // test recording/playback finished
type dmTickMsg time.Time               // periodic UI refresh
type devicesLoadedMsg []record.Device  // result of device enumeration

// ---------------------------------------------------------------------------
// DeviceManager model
// ---------------------------------------------------------------------------

// DeviceManager is the bubbletea model for the device management TUI.
type DeviceManager struct {
	state       DeviceManagerState
	devices     []record.Device
	config      *config.Config
	configPath  string
	cursor           int         // cursor position in device list
	aliasInput       simpleInput // for alias name input
	aliasEditInput   simpleInput // for editing alias target device
	aliasBrowseCursor int       // cursor position when browsing aliases
	groupInput  simpleInput // for group name input
	groupSelect      []bool  // multi-select for group aliases (indexed by sorted alias keys)
	groupCursor      int    // cursor position in the group multi-select
	groupBrowseCursor int   // cursor position when browsing groups
	message     string      // status / error message
	vuLevel     float64     // live VU preview level (dB)
	vuSmoothed  float64     // smoothed VU level (0..1)
	vuProc      *exec.Cmd   // ffmpeg VU preview process
	vuLevelCh   chan float64// channel streaming VU levels from ffmpeg goroutine
	vuCancel    chan struct{}// signal to stop VU goroutine
	testProc    *exec.Cmd   // test record/play process
	testFile    string      // path to temp test recording
	width       int
	height      int
}

// NewDeviceManager creates a DeviceManager model. The caller must provide the
// loaded config and its file path so edits can be persisted.
func NewDeviceManager(cfg *config.Config, configPath string) *DeviceManager {
	// Ensure maps are non-nil so we can write into them.
	if cfg.Devices == nil {
		cfg.Devices = map[string]string{}
	}
	if cfg.DeviceGroups == nil {
		cfg.DeviceGroups = map[string][]string{}
	}

	return &DeviceManager{
		state:      DMBrowse,
		config:     cfg,
		configPath: configPath,
		aliasInput:     newSimpleInput("alias name"),
		aliasEditInput: newSimpleInput("device name"),
		groupInput:     newSimpleInput("group name"),
	}
}

// RunDeviceManager is a convenience entry-point that creates a bubbletea
// program, runs the TUI, and returns any error.
func RunDeviceManager(cfg *config.Config, configPath string) error {
	dm := NewDeviceManager(cfg, configPath)
	p := tea.NewProgram(dm, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func (dm *DeviceManager) Init() tea.Cmd {
	return tea.Batch(
		dm.loadDevices,
		dmTickCmd(),
	)
}

func dmTickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return dmTickMsg(t)
	})
}

// loadDevices fetches the device list from ffmpeg.
func (dm *DeviceManager) loadDevices() tea.Msg {
	devices, err := record.ListDevices()
	if err != nil {
		return dmTestDoneMsg{err: fmt.Errorf("listing devices: %w", err)}
	}
	return devicesLoadedMsg(devices)
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (dm *DeviceManager) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		dm.width = msg.Width
		dm.height = msg.Height
		return dm, nil

	case devicesLoadedMsg:
		dm.devices = []record.Device(msg)
		// Sort so sources come before monitors, matching the visual layout.
		sort.SliceStable(dm.devices, func(i, j int) bool {
			if dm.devices[i].IsMonitor != dm.devices[j].IsMonitor {
				return !dm.devices[i].IsMonitor
			}
			return false
		})
		if len(dm.devices) > 0 {
			return dm, dm.startVU()
		}
		return dm, nil

	case dmTickMsg:
		return dm, dmTickCmd()

	case dmVUMsg:
		dm.vuLevel = float64(msg)
		// Smooth: fast attack, slow decay
		level := dbToLevel(dm.vuLevel)
		diff := level - dm.vuSmoothed
		if diff > 0 {
			dm.vuSmoothed += diff * 0.5
		} else {
			dm.vuSmoothed += diff * 0.15
		}
		dm.vuSmoothed = math.Max(0, math.Min(1, dm.vuSmoothed))
		return dm, dm.listenVU()

	case dmTestDoneMsg:
		if msg.err != nil {
			dm.message = fmt.Sprintf("Error: %v", msg.err)
		}
		switch dm.state {
		case DMTestRecording:
			// Recording finished; play back
			dm.state = DMTestPlayback
			dm.message = "Playing back test..."
			return dm, dm.playTestClip()
		case DMTestPlayback:
			dm.state = DMBrowse
			dm.message = "Test complete."
			// Clean up temp file
			if dm.testFile != "" {
				os.Remove(dm.testFile)
				dm.testFile = ""
			}
			return dm, dm.startVU()
		}
		return dm, nil

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			return dm.handleKey(tea.KeyMsg{Type: tea.KeyUp})
		case tea.MouseButtonWheelDown:
			return dm.handleKey(tea.KeyMsg{Type: tea.KeyDown})
		}
		return dm, nil

	case tea.KeyMsg:
		return dm.handleKey(msg)
	}
	return dm, nil
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (dm *DeviceManager) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch dm.state {
	case DMBrowse:
		return dm.handleBrowseKey(msg)
	case DMAliasPrompt:
		return dm.handleAliasKey(msg)
	case DMGroupName:
		return dm.handleGroupNameKey(msg)
	case DMGroupSelect:
		return dm.handleGroupSelectKey(msg)
	case DMAliasBrowse:
		return dm.handleAliasBrowseKey(msg)
	case DMAliasEdit:
		return dm.handleAliasEditKey(msg)
	case DMGroupBrowse:
		return dm.handleGroupBrowseKey(msg)
	case DMConfirmDeleteA:
		return dm.handleConfirmDeleteAliasKey(msg)
	case DMConfirmDeleteG:
		return dm.handleConfirmDeleteGroupKey(msg)
	case DMTestRecording, DMTestPlayback:
		// Allow ctrl+c to abort test
		if msg.String() == "ctrl+c" {
			if dm.testProc != nil && dm.testProc.Process != nil {
				dm.testProc.Process.Kill()
			}
			dm.state = DMBrowse
			dm.message = "Test cancelled."
			if dm.testFile != "" {
				os.Remove(dm.testFile)
				dm.testFile = ""
			}
			return dm, dm.startVU()
		}
		return dm, nil
	}
	return dm, nil
}

func (dm *DeviceManager) handleBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		dm.stopVU()
		return dm, tea.Quit

	case "up", "k":
		if dm.cursor > 0 {
			dm.cursor--
			return dm, dm.startVU()
		}

	case "down", "j":
		if dm.cursor < len(dm.devices)-1 {
			dm.cursor++
			return dm, dm.startVU()
		}

	case "a":
		if len(dm.devices) == 0 {
			dm.message = "No devices loaded."
			return dm, nil
		}
		dev := dm.devices[dm.cursor]
		existingAlias := dm.aliasForDevice(dev.Name)
		if existingAlias != "" {
			// Device already has an alias — open alias browser with it selected for editing
			aliases := dm.sortedAliases()
			dm.aliasBrowseCursor = 0
			for i, a := range aliases {
				if a == existingAlias {
					dm.aliasBrowseCursor = i
					break
				}
			}
			dm.aliasEditInput.SetValue(dm.config.Devices[existingAlias])
			dm.state = DMAliasEdit
			dm.message = fmt.Sprintf("Editing alias '%s'", existingAlias)
			return dm, nil
		}
		dm.state = DMAliasPrompt
		dm.aliasInput.SetValue("")
		dm.message = ""
		return dm, nil

	case "g":
		dm.state = DMGroupName
		dm.groupInput.SetValue("")
		dm.message = ""
		return dm, nil

	case "A":
		aliases := dm.sortedAliases()
		if len(aliases) == 0 {
			dm.message = "No aliases defined yet."
			return dm, nil
		}
		dm.state = DMAliasBrowse
		dm.aliasBrowseCursor = 0
		dm.message = ""
		return dm, nil

	case "G":
		groups := dm.sortedGroupNames()
		if len(groups) == 0 {
			dm.message = "No groups defined yet."
			return dm, nil
		}
		dm.state = DMGroupBrowse
		dm.groupBrowseCursor = 0
		dm.message = ""
		return dm, nil

	case "d":
		if len(dm.devices) == 0 {
			dm.message = "No devices loaded."
			return dm, nil
		}
		dev := dm.devices[dm.cursor]
		alias := dm.aliasForDevice(dev.Name)
		if alias != "" {
			dm.config.Record.Device = alias
		} else {
			dm.config.Record.Device = dev.Name
		}
		if err := dm.saveConfig(); err != nil {
			dm.message = fmt.Sprintf("Save error: %v", err)
		} else {
			dm.message = fmt.Sprintf("Default set to: %s", dm.config.Record.Device)
		}

	case "t":
		if len(dm.devices) == 0 {
			dm.message = "No devices loaded."
			return dm, nil
		}
		dm.stopVU()
		dm.state = DMTestRecording
		dm.message = "Recording 3-second test..."
		return dm, dm.recordTestClip()

	}
	return dm, nil
}

func (dm *DeviceManager) handleAliasKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "esc":
		dm.state = DMBrowse
		dm.message = ""
		return dm, nil
	case "enter":
		name := strings.TrimSpace(dm.aliasInput.Value())
		if name == "" {
			dm.message = "Alias cannot be empty."
			return dm, nil
		}
		if strings.ContainsAny(name, " \t") {
			dm.message = "Alias cannot contain spaces."
			return dm, nil
		}
		// Collision check: existing aliases
		if _, ok := dm.config.Devices[name]; ok {
			dm.message = fmt.Sprintf("Alias '%s' already exists.", name)
			return dm, nil
		}
		// Collision check: group names
		if _, ok := dm.config.DeviceGroups[name]; ok {
			dm.message = fmt.Sprintf("'%s' conflicts with an existing group name.", name)
			return dm, nil
		}
		dev := dm.devices[dm.cursor]
		dm.config.Devices[name] = dev.Name
		if err := dm.saveConfig(); err != nil {
			dm.message = fmt.Sprintf("Save error: %v", err)
		} else {
			dm.message = fmt.Sprintf("Alias '%s' -> %s", name, dev.Name)
		}
		dm.state = DMBrowse
		return dm, nil
	default:
		dm.aliasInput.HandleKey(keyStr)
	}
	return dm, nil
}

func (dm *DeviceManager) handleGroupNameKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "esc":
		dm.state = DMBrowse
		dm.message = ""
		return dm, nil
	case "enter":
		name := strings.TrimSpace(dm.groupInput.Value())
		if name == "" {
			dm.message = "Group name cannot be empty."
			return dm, nil
		}
		if strings.ContainsAny(name, " \t") {
			dm.message = "Group name cannot contain spaces."
			return dm, nil
		}
		// Collision check: alias names
		if _, ok := dm.config.Devices[name]; ok {
			dm.message = fmt.Sprintf("'%s' conflicts with an existing alias.", name)
			return dm, nil
		}
		aliases := dm.sortedAliases()
		if len(aliases) == 0 {
			dm.message = "No aliases defined yet. Create aliases first."
			dm.state = DMBrowse
			return dm, nil
		}
		dm.groupSelect = make([]bool, len(aliases))
		dm.groupCursor = 0
		// Pre-select existing members if editing
		if existing, ok := dm.config.DeviceGroups[name]; ok {
			memberSet := map[string]bool{}
			for _, a := range existing {
				memberSet[a] = true
			}
			for i, a := range aliases {
				dm.groupSelect[i] = memberSet[a]
			}
		}
		dm.state = DMGroupSelect
		dm.message = fmt.Sprintf("Group '%s': space to toggle, enter to save", name)
		return dm, nil
	default:
		dm.groupInput.HandleKey(keyStr)
	}
	return dm, nil
}

func (dm *DeviceManager) handleGroupSelectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	aliases := dm.sortedAliases()
	switch msg.String() {
	case "esc":
		dm.state = DMBrowse
		dm.message = ""
		return dm, nil
	case "up", "k":
		if dm.groupCursor > 0 {
			dm.groupCursor--
		}
	case "down", "j":
		if dm.groupCursor < len(aliases)-1 {
			dm.groupCursor++
		}
	case " ":
		if dm.groupCursor < len(dm.groupSelect) {
			dm.groupSelect[dm.groupCursor] = !dm.groupSelect[dm.groupCursor]
		}
	case "enter":
		name := strings.TrimSpace(dm.groupInput.Value())
		var selected []string
		for i, a := range aliases {
			if i < len(dm.groupSelect) && dm.groupSelect[i] {
				selected = append(selected, a)
			}
		}
		if len(selected) == 0 {
			dm.message = "No aliases selected; group not created."
			dm.state = DMBrowse
			return dm, nil
		}
		dm.config.DeviceGroups[name] = selected
		if err := dm.saveConfig(); err != nil {
			dm.message = fmt.Sprintf("Save error: %v", err)
		} else {
			dm.message = fmt.Sprintf("Group '%s' -> [%s]", name, strings.Join(selected, ", "))
		}
		dm.state = DMBrowse
	}
	return dm, nil
}


func (dm *DeviceManager) handleAliasBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	aliases := dm.sortedAliases()
	switch msg.String() {
	case "esc", "q":
		dm.state = DMBrowse
		dm.message = ""
		return dm, nil
	case "up", "k":
		if dm.aliasBrowseCursor > 0 {
			dm.aliasBrowseCursor--
		}
	case "down", "j":
		if dm.aliasBrowseCursor < len(aliases)-1 {
			dm.aliasBrowseCursor++
		}
	case "e", "enter":
		if dm.aliasBrowseCursor < len(aliases) {
			name := aliases[dm.aliasBrowseCursor]
			dm.aliasEditInput.SetValue(dm.config.Devices[name])
			dm.state = DMAliasEdit
			dm.message = fmt.Sprintf("Editing alias '%s'", name)
		}
	case "x", "d":
		if dm.aliasBrowseCursor < len(aliases) {
			name := aliases[dm.aliasBrowseCursor]
			dm.state = DMConfirmDeleteA
			dm.message = fmt.Sprintf("Delete alias '%s'? [y/n]", name)
		}
	}
	return dm, nil
}

func (dm *DeviceManager) handleAliasEditKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()
	switch keyStr {
	case "esc":
		dm.state = DMAliasBrowse
		dm.message = ""
		return dm, nil
	case "enter":
		newDevice := strings.TrimSpace(dm.aliasEditInput.Value())
		if newDevice == "" {
			dm.message = "Device name cannot be empty."
			return dm, nil
		}
		aliases := dm.sortedAliases()
		if dm.aliasBrowseCursor < len(aliases) {
			name := aliases[dm.aliasBrowseCursor]
			dm.config.Devices[name] = newDevice
			if err := dm.saveConfig(); err != nil {
				dm.message = fmt.Sprintf("Save error: %v", err)
			} else {
				dm.message = fmt.Sprintf("Updated alias '%s' -> %s", name, newDevice)
			}
		}
		dm.state = DMAliasBrowse
		return dm, nil
	default:
		dm.aliasEditInput.HandleKey(keyStr)
	}
	return dm, nil
}

func (dm *DeviceManager) handleConfirmDeleteAliasKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		aliases := dm.sortedAliases()
		if dm.aliasBrowseCursor < len(aliases) {
			name := aliases[dm.aliasBrowseCursor]
			delete(dm.config.Devices, name)
			// Remove from all groups
			for gName, members := range dm.config.DeviceGroups {
				filtered := members[:0]
				for _, m := range members {
					if m != name {
						filtered = append(filtered, m)
					}
				}
				if len(filtered) == 0 {
					delete(dm.config.DeviceGroups, gName)
				} else {
					dm.config.DeviceGroups[gName] = filtered
				}
			}
			if dm.config.Record.Device == name {
				dm.config.Record.Device = ""
			}
			if err := dm.saveConfig(); err != nil {
				dm.message = fmt.Sprintf("Save error: %v", err)
			} else {
				dm.message = fmt.Sprintf("Deleted alias '%s'.", name)
			}
			remaining := dm.sortedAliases()
			if dm.aliasBrowseCursor >= len(remaining) && dm.aliasBrowseCursor > 0 {
				dm.aliasBrowseCursor--
			}
			if len(remaining) == 0 {
				dm.state = DMBrowse
				return dm, nil
			}
		}
		dm.state = DMAliasBrowse
	case "n", "N", "esc":
		dm.state = DMAliasBrowse
		dm.message = ""
	}
	return dm, nil
}

func (dm *DeviceManager) handleGroupBrowseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	groups := dm.sortedGroupNames()
	switch msg.String() {
	case "esc", "q":
		dm.state = DMBrowse
		dm.message = ""
		return dm, nil
	case "up", "k":
		if dm.groupBrowseCursor > 0 {
			dm.groupBrowseCursor--
		}
	case "down", "j":
		if dm.groupBrowseCursor < len(groups)-1 {
			dm.groupBrowseCursor++
		}
	case "e", "enter":
		if dm.groupBrowseCursor < len(groups) {
			name := groups[dm.groupBrowseCursor]
			dm.groupInput.SetValue(name)
			aliases := dm.sortedAliases()
			if len(aliases) == 0 {
				dm.message = "No aliases defined yet. Create aliases first."
				dm.state = DMBrowse
				return dm, nil
			}
			dm.groupSelect = make([]bool, len(aliases))
			dm.groupCursor = 0
			if existing, ok := dm.config.DeviceGroups[name]; ok {
				memberSet := map[string]bool{}
				for _, a := range existing {
					memberSet[a] = true
				}
				for i, a := range aliases {
					dm.groupSelect[i] = memberSet[a]
				}
			}
			dm.state = DMGroupSelect
			dm.message = fmt.Sprintf("Editing group '%s': space to toggle, enter to save", name)
		}
	case "x", "d":
		if dm.groupBrowseCursor < len(groups) {
			name := groups[dm.groupBrowseCursor]
			dm.state = DMConfirmDeleteG
			dm.message = fmt.Sprintf("Delete group '%s'? [y/n]", name)
		}
	}
	return dm, nil
}

func (dm *DeviceManager) handleConfirmDeleteGroupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		groups := dm.sortedGroupNames()
		if dm.groupBrowseCursor < len(groups) {
			name := groups[dm.groupBrowseCursor]
			delete(dm.config.DeviceGroups, name)
			// Clear default if it referenced this group
			if dm.config.Record.Device == name {
				dm.config.Record.Device = ""
			}
			if err := dm.saveConfig(); err != nil {
				dm.message = fmt.Sprintf("Save error: %v", err)
			} else {
				dm.message = fmt.Sprintf("Deleted group '%s'.", name)
			}
			// Adjust cursor if it's past the end
			remaining := dm.sortedGroupNames()
			if dm.groupBrowseCursor >= len(remaining) && dm.groupBrowseCursor > 0 {
				dm.groupBrowseCursor--
			}
			if len(remaining) == 0 {
				dm.state = DMBrowse
				return dm, nil
			}
		}
		dm.state = DMGroupBrowse
	case "n", "N", "esc":
		dm.state = DMGroupBrowse
		dm.message = ""
	}
	return dm, nil
}

// ---------------------------------------------------------------------------
// VU preview
// ---------------------------------------------------------------------------

var vuRMSPattern = regexp.MustCompile(`lavfi\.astats\.Overall\.RMS_level=(-?[\d.]+|inf|-inf)`)

// startVU launches an ffmpeg subprocess that streams RMS levels for the
// currently selected device. Levels are sent on dm.vuLevelCh which is
// drained by listenVU commands. Returns the initial listenVU command.
func (dm *DeviceManager) startVU() tea.Cmd {
	dm.stopVU()
	if len(dm.devices) == 0 {
		return nil
	}
	dev := dm.devices[dm.cursor]
	cancel := make(chan struct{})
	dm.vuCancel = cancel
	levelCh := make(chan float64, 10)
	dm.vuLevelCh = levelCh

	// Launch ffmpeg in a background goroutine; it writes to levelCh.
	go func() {
		defer close(levelCh)
		inputFmt := record.InputFormat()
		cmd := exec.Command("ffmpeg",
			"-f", inputFmt,
			"-i", dev.Name,
			"-af", "asetnsamples=n=480,astats=metadata=1:reset=1,ametadata=print:file=/dev/stderr",
			"-f", "null", "-",
		)
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return
		}
		if err := cmd.Start(); err != nil {
			return
		}
		dm.vuProc = cmd

		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			select {
			case <-cancel:
				cmd.Process.Kill()
				cmd.Wait()
				return
			default:
			}
			line := scanner.Text()
			if m := vuRMSPattern.FindStringSubmatch(line); len(m) > 1 {
				if val, err := strconv.ParseFloat(m[1], 64); err == nil {
					select {
					case levelCh <- val:
					default: // drop if consumer is slow
					}
				}
			}
		}
		cmd.Wait()
	}()

	return dm.listenVU()
}

// listenVU returns a tea.Cmd that waits for the next VU level value.
func (dm *DeviceManager) listenVU() tea.Cmd {
	ch := dm.vuLevelCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		val, ok := <-ch
		if !ok {
			return nil
		}
		return dmVUMsg(val)
	}
}

func (dm *DeviceManager) stopVU() {
	if dm.vuCancel != nil {
		close(dm.vuCancel)
		dm.vuCancel = nil
	}
	if dm.vuProc != nil && dm.vuProc.Process != nil {
		dm.vuProc.Process.Kill()
		dm.vuProc.Wait()
		dm.vuProc = nil
	}
	dm.vuLevelCh = nil
	dm.vuLevel = -100
	dm.vuSmoothed = 0
}

// ---------------------------------------------------------------------------
// Test record / playback
// ---------------------------------------------------------------------------

func (dm *DeviceManager) recordTestClip() tea.Cmd {
	if len(dm.devices) == 0 {
		return nil
	}
	dev := dm.devices[dm.cursor]
	return func() tea.Msg {
		tmpFile, err := os.CreateTemp("", "audiotools-test-*.wav")
		if err != nil {
			return dmTestDoneMsg{err: err}
		}
		tmpFile.Close()
		dm.testFile = tmpFile.Name()

		inputFmt := record.InputFormat()
		cmd := exec.Command("ffmpeg",
			"-f", inputFmt,
			"-i", dev.Name,
			"-t", "3",
			"-c:a", "pcm_s16le",
			"-ar", "48000",
			"-ac", "1",
			"-y", dm.testFile,
		)
		dm.testProc = cmd
		if err := cmd.Run(); err != nil {
			return dmTestDoneMsg{err: fmt.Errorf("test recording: %w", err)}
		}
		return dmTestDoneMsg{}
	}
}

func (dm *DeviceManager) playTestClip() tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("ffplay", "-nodisp", "-autoexit", dm.testFile)
		dm.testProc = cmd
		if err := cmd.Run(); err != nil {
			return dmTestDoneMsg{err: fmt.Errorf("test playback: %w", err)}
		}
		return dmTestDoneMsg{}
	}
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

var (
	dmBorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#555555")).
			Padding(0, 1)
	dmTitleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#c084fc"))
	dmSelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#22c55e"))
	dmAliasTag      = lipgloss.NewStyle().Foreground(lipgloss.Color("#60a5fa"))
	dmDimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	dmAccentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b"))
	dmErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	dmVUFilled      = lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	dmVUEmpty       = lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	dmCursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#c084fc")).Bold(true)
)

func (dm *DeviceManager) View() string {
	// Handle overlay states that take the whole screen
	switch dm.state {
	case DMAliasPrompt:
		return dm.viewAliasPrompt()
	case DMAliasBrowse, DMAliasEdit, DMConfirmDeleteA:
		return dm.viewAliasBrowse()
	case DMGroupName:
		return dm.viewGroupNamePrompt()
	case DMGroupSelect:
		return dm.viewGroupSelect()
	case DMGroupBrowse, DMConfirmDeleteG:
		return dm.viewGroupBrowse()
	}

	// Main layout: left panel (devices) + right panel (config)
	leftPanel := dm.viewDeviceList()
	rightPanel := dm.viewConfigPanel()

	// Determine widths
	totalWidth := dm.width
	if totalWidth < 40 {
		totalWidth = 80
	}
	leftWidth := totalWidth*3/5 - 4
	rightWidth := totalWidth - leftWidth - 6
	if leftWidth < 20 {
		leftWidth = 20
	}
	if rightWidth < 15 {
		rightWidth = 15
	}

	left := dmBorderStyle.Width(leftWidth).Render(leftPanel)
	right := dmBorderStyle.Width(rightWidth).Render(rightPanel)
	top := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	// VU bar — content width is Width minus horizontal padding (1 each side)
	vuContentWidth := totalWidth - 4 - 2
	vuBar := dm.viewVUBar(vuContentWidth)
	vuBox := dmBorderStyle.Width(totalWidth - 4).Render(vuBar)

	// Status / keys
	var statusLine string
	if dm.message != "" {
		if strings.HasPrefix(dm.message, "Error") {
			statusLine = dmErrorStyle.Render("  " + dm.message)
		} else {
			statusLine = dmAccentStyle.Render("  " + dm.message)
		}
	}

	keys := dm.viewKeys()
	keysBox := dmBorderStyle.Width(totalWidth - 4).Render(keys)

	parts := []string{top, vuBox}
	if statusLine != "" {
		parts = append(parts, statusLine)
	}
	parts = append(parts, keysBox)

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (dm *DeviceManager) viewDeviceList() string {
	var b strings.Builder

	// Split devices into sources and monitors
	var sources, monitors []record.Device
	for _, d := range dm.devices {
		if d.IsMonitor {
			monitors = append(monitors, d)
		} else {
			sources = append(sources, d)
		}
	}

	b.WriteString(dmTitleStyle.Render("SOURCES") + "\n")
	if len(sources) == 0 {
		b.WriteString(dmDimStyle.Render("  (none)") + "\n")
	}
	for _, d := range sources {
		idx := dm.deviceIndex(d.Name)
		b.WriteString(dm.renderDeviceLine(d, idx))
	}

	b.WriteString("\n" + dmTitleStyle.Render("MONITORS") + "\n")
	if len(monitors) == 0 {
		b.WriteString(dmDimStyle.Render("  (none)") + "\n")
	}
	for _, d := range monitors {
		idx := dm.deviceIndex(d.Name)
		b.WriteString(dm.renderDeviceLine(d, idx))
	}

	return b.String()
}

func (dm *DeviceManager) renderDeviceLine(d record.Device, idx int) string {
	cursor := "  "
	nameStyle := lipgloss.NewStyle()
	if idx == dm.cursor {
		cursor = dmSelectedStyle.Render("> ")
		nameStyle = dmSelectedStyle
	}
	display := d.Description
	if display == "" {
		display = d.Name
	}
	// Truncate if too long
	if len(display) > 35 {
		display = display[:32] + "..."
	}
	line := cursor + nameStyle.Render(display)
	alias := dm.aliasForDevice(d.Name)
	if alias != "" {
		line += " " + dmAliasTag.Render("["+alias+"]")
	}
	return line + "\n"
}

func (dm *DeviceManager) viewConfigPanel() string {
	var b strings.Builder

	b.WriteString(dmTitleStyle.Render("ALIASES") + "\n")
	aliases := dm.sortedAliases()
	if len(aliases) == 0 {
		b.WriteString(dmDimStyle.Render("  (none)") + "\n")
	}
	for _, alias := range aliases {
		raw := dm.config.Devices[alias]
		display := raw
		if len(display) > 20 {
			display = display[:17] + "..."
		}
		b.WriteString(fmt.Sprintf("  %s -> %s\n",
			dmAliasTag.Render(alias),
			dmDimStyle.Render(display),
		))
	}

	b.WriteString("\n" + dmTitleStyle.Render("GROUPS") + "\n")
	groupNames := dm.sortedGroupNames()
	if len(groupNames) == 0 {
		b.WriteString(dmDimStyle.Render("  (none)") + "\n")
	}
	for _, gName := range groupNames {
		members := dm.config.DeviceGroups[gName]
		b.WriteString(fmt.Sprintf("  %s -> %s\n",
			dmAccentStyle.Render(gName),
			dmDimStyle.Render(strings.Join(members, ", ")),
		))
	}

	b.WriteString("\n" + dmTitleStyle.Render("DEFAULT") + "\n")
	def := dm.config.Record.Device
	if def == "" {
		def = "(not set)"
	}
	b.WriteString("  " + dmAccentStyle.Render(def) + "\n")

	return b.String()
}

func (dm *DeviceManager) viewVUBar(width int) string {
	if width < 10 {
		width = 40
	}

	dbStr := formatDB(dm.vuSmoothed)
	dbVisual := lipgloss.Width(dbStr)

	devName := ""
	if dm.cursor < len(dm.devices) {
		d := dm.devices[dm.cursor]
		devName = d.Description
		if devName == "" {
			devName = d.Name
		}
	}

	// Layout: bar + " " + dbStr [+ "  " + devName]
	// Calculate bar width from remaining space
	overhead := 1 + dbVisual // " " + dbStr
	if devName != "" {
		devVisual := lipgloss.Width(devName)
		maxDev := width / 3
		if devVisual > maxDev {
			// Truncate by runes to stay within maxDev columns
			runes := []rune(devName)
			for lipgloss.Width(string(runes)) > maxDev-3 && len(runes) > 0 {
				runes = runes[:len(runes)-1]
			}
			devName = string(runes) + "..."
		}
		overhead += 2 + lipgloss.Width(devName) // "  " + devName
	}
	barWidth := width - overhead
	if barWidth < 5 {
		barWidth = 5
	}

	filled := int(dm.vuSmoothed * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled

	bar := dmVUFilled.Render(strings.Repeat("\u2588", filled)) +
		dmVUEmpty.Render(strings.Repeat("\u2591", empty))

	if devName != "" {
		return fmt.Sprintf("%s %s  %s", bar, dmDimStyle.Render(dbStr), dmDimStyle.Render(devName))
	}
	return fmt.Sprintf("%s %s", bar, dmDimStyle.Render(dbStr))
}

func (dm *DeviceManager) viewKeys() string {
	switch dm.state {
	case DMTestRecording:
		return dmDimStyle.Render("Recording test... [ctrl+c] cancel")
	case DMTestPlayback:
		return dmDimStyle.Render("Playing back... [ctrl+c] cancel")
	default:
		return dmDimStyle.Render("[a]lias  [A]liases  [g]roup  [G]roups  [d]efault  [t]est  [q]uit")
	}
}

func (dm *DeviceManager) viewAliasPrompt() string {
	var b strings.Builder
	b.WriteString(dmTitleStyle.Render("Create Alias") + "\n\n")
	if dm.cursor < len(dm.devices) {
		d := dm.devices[dm.cursor]
		b.WriteString(fmt.Sprintf("  Device: %s\n\n", dmDimStyle.Render(d.Name)))
	}
	b.WriteString("  Alias name: " + dm.aliasInput.View() + "\n\n")
	if dm.message != "" {
		b.WriteString("  " + dmErrorStyle.Render(dm.message) + "\n\n")
	}
	b.WriteString(dmDimStyle.Render("  [enter] save  [esc] cancel"))
	return b.String()
}

func (dm *DeviceManager) viewGroupNamePrompt() string {
	var b strings.Builder
	b.WriteString(dmTitleStyle.Render("Create Group") + "\n\n")
	b.WriteString("  Group name: " + dm.groupInput.View() + "\n\n")
	if dm.message != "" {
		b.WriteString("  " + dmErrorStyle.Render(dm.message) + "\n\n")
	}
	b.WriteString(dmDimStyle.Render("  [enter] next  [esc] cancel"))
	return b.String()
}

func (dm *DeviceManager) viewGroupSelect() string {
	var b strings.Builder
	b.WriteString(dmTitleStyle.Render("Select aliases for group") + "\n\n")
	aliases := dm.sortedAliases()
	for i, a := range aliases {
		cursor := "  "
		if i == dm.groupCursor {
			cursor = dmSelectedStyle.Render("> ")
		}
		check := "[ ]"
		if i < len(dm.groupSelect) && dm.groupSelect[i] {
			check = dmSelectedStyle.Render("[x]")
		}
		b.WriteString(fmt.Sprintf("%s%s %s\n", cursor, check, a))
	}
	b.WriteString("\n")
	if dm.message != "" {
		b.WriteString("  " + dmAccentStyle.Render(dm.message) + "\n\n")
	}
	b.WriteString(dmDimStyle.Render("  [space] toggle  [enter] save  [esc] cancel"))
	return b.String()
}

func (dm *DeviceManager) viewAliasBrowse() string {
	var b strings.Builder
	b.WriteString(dmTitleStyle.Render("Aliases") + "\n\n")
	aliases := dm.sortedAliases()
	for i, name := range aliases {
		cursor := "  "
		if i == dm.aliasBrowseCursor {
			cursor = dmSelectedStyle.Render("> ")
		}
		raw := dm.config.Devices[name]
		b.WriteString(fmt.Sprintf("%s%s -> %s\n", cursor, dmAliasTag.Render(name), dmDimStyle.Render(raw)))
	}
	b.WriteString("\n")
	if dm.state == DMAliasEdit {
		aliases := dm.sortedAliases()
		if dm.aliasBrowseCursor < len(aliases) {
			b.WriteString(fmt.Sprintf("  Device: %s\n\n", dm.aliasEditInput.View()))
		}
	}
	if dm.message != "" {
		if strings.HasPrefix(dm.message, "Delete") || strings.HasPrefix(dm.message, "Error") || strings.HasPrefix(dm.message, "Save error") {
			b.WriteString("  " + dmErrorStyle.Render(dm.message) + "\n\n")
		} else {
			b.WriteString("  " + dmAccentStyle.Render(dm.message) + "\n\n")
		}
	}
	switch dm.state {
	case DMAliasEdit:
		b.WriteString(dmDimStyle.Render("  [enter] save  [esc] cancel"))
	case DMConfirmDeleteA:
		b.WriteString(dmDimStyle.Render("  [y]es  [n]o"))
	default:
		b.WriteString(dmDimStyle.Render("  [e]dit  [x]delete  [esc] back"))
	}
	return b.String()
}

func (dm *DeviceManager) viewGroupBrowse() string {
	var b strings.Builder
	b.WriteString(dmTitleStyle.Render("Groups") + "\n\n")
	groups := dm.sortedGroupNames()
	for i, name := range groups {
		cursor := "  "
		if i == dm.groupBrowseCursor {
			cursor = dmSelectedStyle.Render("> ")
		}
		members := dm.config.DeviceGroups[name]
		b.WriteString(fmt.Sprintf("%s%s -> %s\n", cursor, dmAccentStyle.Render(name), dmDimStyle.Render(strings.Join(members, ", "))))
	}
	b.WriteString("\n")
	if dm.message != "" {
		if strings.HasPrefix(dm.message, "Delete") || strings.HasPrefix(dm.message, "Error") {
			b.WriteString("  " + dmErrorStyle.Render(dm.message) + "\n\n")
		} else {
			b.WriteString("  " + dmAccentStyle.Render(dm.message) + "\n\n")
		}
	}
	b.WriteString(dmDimStyle.Render("  [e]dit  [x]delete  [esc] back"))
	return b.String()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// aliasForDevice returns the alias name for a given raw device name, or "".
func (dm *DeviceManager) aliasForDevice(rawName string) string {
	for alias, raw := range dm.config.Devices {
		if raw == rawName {
			return alias
		}
	}
	return ""
}

// sortedAliases returns alias names sorted alphabetically.
func (dm *DeviceManager) sortedAliases() []string {
	aliases := make([]string, 0, len(dm.config.Devices))
	for a := range dm.config.Devices {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	return aliases
}

// sortedGroupNames returns group names sorted alphabetically.
func (dm *DeviceManager) sortedGroupNames() []string {
	names := make([]string, 0, len(dm.config.DeviceGroups))
	for n := range dm.config.DeviceGroups {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// deviceIndex returns the index of a device by Name in the flat device list.
func (dm *DeviceManager) deviceIndex(name string) int {
	for i, d := range dm.devices {
		if d.Name == name {
			return i
		}
	}
	return -1
}

// saveConfig persists the config to disk.
func (dm *DeviceManager) saveConfig() error {
	if dm.configPath != "" {
		return dm.config.SaveTo(dm.configPath)
	}
	return dm.config.Save()
}
