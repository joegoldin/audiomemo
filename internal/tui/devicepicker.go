package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/joegilkes/audiotools/internal/record"
)

type DevicePicker struct {
	devices  []record.Device
	cursor   int
	selected int
}

func NewDevicePicker() *DevicePicker {
	return &DevicePicker{}
}

func (p *DevicePicker) SetDevices(devices []record.Device) {
	p.devices = devices
	p.cursor = 0
}

func (p *DevicePicker) View() string {
	title := lipgloss.NewStyle().Bold(true).Render("Select input device:")
	s := title + "\n\n"
	for i, d := range p.devices {
		cursor := "  "
		if i == p.cursor {
			cursor = "> "
		}
		name := d.Description
		if d.IsDefault {
			name += " (default)"
		}
		s += cursor + name + "\n"
	}
	s += "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Render("↑/↓ select  enter confirm  esc cancel")
	return s
}
