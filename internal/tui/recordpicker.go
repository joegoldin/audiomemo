package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joegoldin/audiomemo/internal/config"
	"github.com/joegoldin/audiomemo/internal/record"
)

// ---------------------------------------------------------------------------
// State machine
// ---------------------------------------------------------------------------

type recordPickerState int

const (
	RPLoading recordPickerState = iota
	RPPick
	RPDone
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type rpItem struct {
	label   string   // display name (alias name, group name, or device description)
	kind    string   // "alias", "group", "device"
	devices []string // resolved raw device name(s)
}

// RecordPickerResult holds the outcome of the record picker TUI.
type RecordPickerResult struct {
	Devices     []string // resolved raw device names to record
	DeviceLabel string   // human-readable label for the TUI
	Skipped     bool     // user pressed esc
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type recordPickerModel struct {
	state    recordPickerState
	items    []rpItem     // ordered: aliases, groups, devices
	selected map[int]bool // multi-select tracking
	cursor   int
	config   *config.Config
	result   RecordPickerResult
	width    int
	height   int
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RunRecordPicker launches the device picker TUI for the record command.
func RunRecordPicker(cfg *config.Config) (RecordPickerResult, error) {
	m := &recordPickerModel{
		state:    RPLoading,
		config:   cfg,
		selected: map[int]bool{},
	}

	p := tea.NewProgram(m, tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		return RecordPickerResult{Skipped: true}, err
	}
	if fm, ok := finalModel.(*recordPickerModel); ok {
		return fm.result, nil
	}
	return RecordPickerResult{Skipped: true}, nil
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------

func (m *recordPickerModel) Init() tea.Cmd {
	return m.loadDevices
}

func (m *recordPickerModel) loadDevices() tea.Msg {
	devices, err := record.ListDevices()
	if err != nil {
		return devicesLoadedMsg(nil)
	}
	return devicesLoadedMsg(devices)
}

// ---------------------------------------------------------------------------
// Item list construction
// ---------------------------------------------------------------------------

func (m *recordPickerModel) buildItems(devices []record.Device) {
	m.items = nil

	// Set of raw device names covered by aliases.
	aliased := map[string]bool{}
	for _, raw := range m.config.Devices {
		aliased[raw] = true
	}

	def := m.config.Record.Device // configured default (alias, group, or raw name)

	// 1. Default — always first if configured.
	if def != "" {
		if _, isAlias := m.config.Devices[def]; isAlias {
			m.items = append(m.items, rpItem{
				label:   def,
				kind:    "default",
				devices: []string{m.config.Devices[def]},
			})
		} else if members, isGroup := m.config.DeviceGroups[def]; isGroup {
			var resolved []string
			for _, alias := range members {
				if raw, ok := m.config.Devices[alias]; ok {
					resolved = append(resolved, raw)
				}
			}
			if len(resolved) > 0 {
				m.items = append(m.items, rpItem{
					label:   def,
					kind:    "default",
					devices: resolved,
				})
			}
		} else {
			// Raw device name as default.
			m.items = append(m.items, rpItem{
				label:   def,
				kind:    "default",
				devices: []string{def},
			})
		}
	}

	// 2. Groups — sorted alphabetically.
	groupNames := make([]string, 0, len(m.config.DeviceGroups))
	for g := range m.config.DeviceGroups {
		groupNames = append(groupNames, g)
	}
	sort.Strings(groupNames)
	for _, gName := range groupNames {
		if gName == def {
			continue // already listed as default
		}
		members := m.config.DeviceGroups[gName]
		var resolved []string
		for _, alias := range members {
			if raw, ok := m.config.Devices[alias]; ok {
				resolved = append(resolved, raw)
			}
		}
		if len(resolved) > 0 {
			m.items = append(m.items, rpItem{
				label:   gName,
				kind:    "group",
				devices: resolved,
			})
		}
	}

	// 3. Aliases — sorted alphabetically, skip the default.
	aliases := make([]string, 0, len(m.config.Devices))
	for a := range m.config.Devices {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		if alias == def {
			continue // already listed as default
		}
		raw := m.config.Devices[alias]
		m.items = append(m.items, rpItem{
			label:   alias,
			kind:    "alias",
			devices: []string{raw},
		})
	}

	// 4. Raw source devices not covered by an alias, sorted by description.
	var rawDevs []record.Device
	for _, d := range devices {
		if !d.IsMonitor && !aliased[d.Name] {
			rawDevs = append(rawDevs, d)
		}
	}
	sort.Slice(rawDevs, func(i, j int) bool {
		di, dj := rawDevs[i].Description, rawDevs[j].Description
		if di == "" {
			di = rawDevs[i].Name
		}
		if dj == "" {
			dj = rawDevs[j].Name
		}
		return di < dj
	})
	for _, d := range rawDevs {
		label := d.Description
		if label == "" {
			label = d.Name
		}
		if label == def {
			continue // already listed as default
		}
		m.items = append(m.items, rpItem{
			label:   label,
			kind:    "device",
			devices: []string{d.Name},
		})
	}
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m *recordPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case devicesLoadedMsg:
		m.buildItems([]record.Device(msg))

		if len(m.items) == 0 {
			m.result.Skipped = true
			m.state = RPDone
			return m, tea.Quit
		}

		m.state = RPPick
		return m, nil

	case tea.MouseMsg:
		if m.state == RPPick {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				if m.cursor > 0 {
					m.cursor--
				}
			case tea.MouseButtonWheelDown:
				if m.cursor < len(m.items)-1 {
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

func (m *recordPickerModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.result.Skipped = true
		return m, tea.Quit
	}

	if m.state != RPPick {
		return m, nil
	}

	keyStr := msg.String()

	switch keyStr {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case "esc", "q":
		m.result.Skipped = true
		m.state = RPDone
		return m, tea.Quit
	case " ":
		m.toggleSelect(m.cursor)
	case "enter":
		m.finishSelection()
		m.state = RPDone
		return m, tea.Quit
	default:
		// Hotkey: 1-9 then 0 maps to items 0-9
		if idx, ok := hotkeyIndex(keyStr); ok && idx < len(m.items) {
			m.finishSingle(idx)
			m.state = RPDone
			return m, tea.Quit
		}
	}
	return m, nil
}

// toggleSelect toggles multi-select on the given item index. When a group
// (or default-that-is-a-group) is toggled on, its member aliases are also
// selected. When toggled off, the members are deselected. Conversely, when
// an alias belonging to a selected group is deselected, the group is too.
func (m *recordPickerModel) toggleSelect(idx int) {
	if idx >= len(m.items) {
		return
	}
	item := m.items[idx]
	selecting := !m.selected[idx]

	if selecting {
		m.selected[idx] = true
	} else {
		delete(m.selected, idx)
	}

	// If this item is a group (or default pointing to a group), cascade to aliases.
	groupMembers := m.groupMembersFor(item)
	if len(groupMembers) > 0 {
		for i, it := range m.items {
			if (it.kind == "alias" || it.kind == "default") && i != idx {
				for _, member := range groupMembers {
					if it.label == member {
						if selecting {
							m.selected[i] = true
						} else {
							delete(m.selected, i)
						}
					}
				}
			}
		}
		return
	}

	// If this is an alias being deselected, deselect any group that contains it.
	if !selecting && (item.kind == "alias" || item.kind == "default") {
		for i, it := range m.items {
			gm := m.groupMembersFor(it)
			for _, member := range gm {
				if member == item.label {
					delete(m.selected, i)
					break
				}
			}
		}
	}
}

// groupMembersFor returns the group member alias names if the item represents
// a group (directly or as the default), or nil otherwise.
func (m *recordPickerModel) groupMembersFor(item rpItem) []string {
	if members, ok := m.config.DeviceGroups[item.label]; ok {
		return members
	}
	return nil
}

// hotkeyIndex converts a hotkey character to an item index.
// "1"->0, "2"->1, ..., "9"->8, "0"->9.
func hotkeyIndex(key string) (int, bool) {
	if len(key) != 1 {
		return 0, false
	}
	ch := key[0]
	if ch >= '1' && ch <= '9' {
		return int(ch - '1'), true
	}
	if ch == '0' {
		return 9, true
	}
	return 0, false
}

// hotkeyLabel returns the hotkey string for an item index, or "" if none.
func hotkeyLabel(idx int) string {
	if idx < 9 {
		return fmt.Sprintf("%d", idx+1)
	}
	if idx == 9 {
		return "0"
	}
	return ""
}

// finishSingle records a single item as the result.
func (m *recordPickerModel) finishSingle(idx int) {
	item := m.items[idx]
	m.result.Devices = dedup(item.devices)
	m.result.DeviceLabel = item.label
	if item.kind == "group" {
		m.result.DeviceLabel = fmt.Sprintf("%s (%s)", item.label, strings.Join(aliasNames(m.config.DeviceGroups[item.label]), " + "))
	}
}

// finishSelection records all selected items (or cursor item if none selected).
func (m *recordPickerModel) finishSelection() {
	if len(m.selected) == 0 {
		m.finishSingle(m.cursor)
		return
	}

	var allDevices []string
	var labels []string
	for idx := range m.selected {
		if idx < len(m.items) {
			item := m.items[idx]
			allDevices = append(allDevices, item.devices...)
			labels = append(labels, item.label)
		}
	}
	m.result.Devices = dedup(allDevices)
	sort.Strings(labels)
	m.result.DeviceLabel = strings.Join(labels, " + ")
}

// dedup removes duplicate strings preserving order.
func dedup(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// aliasNames returns the slice as-is (used for group member labels).
func aliasNames(members []string) []string {
	return members
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m *recordPickerModel) View() string {
	switch m.state {
	case RPLoading:
		return "\n  " + dmDimStyle.Render("Scanning for audio devices...") + "\n"
	case RPPick:
		return m.viewPick()
	case RPDone:
		return ""
	}
	return ""
}

func (m *recordPickerModel) viewPick() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString("  " + dmTitleStyle.Render("Quick Record") + "\n")

	// Group items by kind for section headers.
	type section struct {
		title string
		kind  string
	}
	sections := []section{
		{"DEFAULT", "default"},
		{"GROUPS", "group"},
		{"ALIASES", "alias"},
		{"DEVICES", "device"},
	}

	globalIdx := 0
	for _, sec := range sections {
		// Collect items for this section.
		var sectionItems []int
		for i, item := range m.items {
			if item.kind == sec.kind {
				sectionItems = append(sectionItems, i)
			}
		}
		if len(sectionItems) == 0 {
			continue
		}

		b.WriteString("\n  " + dmDimStyle.Render(sec.title) + "\n")

		for _, idx := range sectionItems {
			item := m.items[idx]

			// Cursor marker
			cursor := "  "
			if idx == m.cursor {
				cursor = dmSelectedStyle.Render("> ")
			}

			// Hotkey number
			hk := hotkeyLabel(globalIdx)
			hkStr := "   "
			if hk != "" {
				hkStr = dmAccentStyle.Render(hk) + "  "
			}

			// Multi-select marker
			check := "   "
			if m.selected[idx] {
				check = dmSelectedStyle.Render("[x]")
			}

			// Label
			label := item.label
			nameStyle := lipgloss.NewStyle()
			if idx == m.cursor {
				nameStyle = dmSelectedStyle
			}

			// Right-side detail
			detail := ""
			switch item.kind {
			case "alias":
				// Show raw device name dimmed
				if raw, ok := m.config.Devices[item.label]; ok {
					desc := deviceDescription(raw)
					if desc != "" {
						detail = dmDimStyle.Render(desc)
					} else {
						detail = dmDimStyle.Render(raw)
					}
				}
			case "group":
				// Show member aliases
				if members, ok := m.config.DeviceGroups[item.label]; ok {
					detail = dmDimStyle.Render(strings.Join(members, " + "))
				}
			}

			line := cursor + hkStr + check + " " + nameStyle.Render(label)
			if detail != "" {
				line += "  " + detail
			}
			b.WriteString(line + "\n")

			globalIdx++
		}
	}

	b.WriteString("\n")

	// Help line
	maxHK := globalIdx
	if maxHK > 10 {
		maxHK = 10
	}
	hkRange := "1"
	if maxHK > 1 {
		hkRange = fmt.Sprintf("1-%s", hotkeyLabel(maxHK-1))
	}
	b.WriteString("  " + dmDimStyle.Render(fmt.Sprintf("[%s] record  [space] multi-select  [enter] record selected  [esc] cancel", hkRange)) + "\n")

	return b.String()
}

// deviceDescription is a placeholder — the picker doesn't load full device
// info, so we just return empty. The raw name is shown instead.
func deviceDescription(_ string) string {
	return ""
}
