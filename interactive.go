package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Config struct {
	HiddenPorts map[string]bool `json:"hidden_ports"` // key: "port-pid"
}

func getConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".portmon.json")
}

func loadConfig() *Config {
	config := &Config{
		HiddenPorts: make(map[string]bool),
	}

	data, err := os.ReadFile(getConfigPath())
	if err != nil {
		return config
	}

	json.Unmarshal(data, config)
	return config
}

func (c *Config) save() error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(getConfigPath(), data, 0644)
}

type model struct {
	ports    []PortInfo
	cursor   int
	config   *Config
	message  string
	showAll  bool
}

func initialModel(ports []PortInfo) model {
	return model{
		ports:   ports,
		cursor:  0,
		config:  loadConfig(),
		showAll: false,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			visiblePorts := m.getVisiblePorts()
			if m.cursor < len(visiblePorts)-1 {
				m.cursor++
			}

		case "h":
			// Hide selected port
			visiblePorts := m.getVisiblePorts()
			if len(visiblePorts) > 0 && m.cursor < len(visiblePorts) {
				port := visiblePorts[m.cursor]
				key := fmt.Sprintf("%d-%s", port.Port, port.PID)
				m.config.HiddenPorts[key] = true
				m.config.save()
				m.message = fmt.Sprintf("Hidden port %d (PID %s)", port.Port, port.PID)

				// Adjust cursor if needed
				if m.cursor >= len(m.getVisiblePorts()) && m.cursor > 0 {
					m.cursor--
				}
			}

		case "u":
			// Unhide all
			m.config.HiddenPorts = make(map[string]bool)
			m.config.save()
			m.message = "Unhidden all ports"
			m.cursor = 0

		case "a":
			// Toggle show all ports
			m.showAll = !m.showAll
			if m.showAll {
				m.message = "Showing ALL ports"
			} else {
				m.message = "Showing filtered ports (3000+, 4000+, 8000+)"
			}
			m.cursor = 0

		case "K":
			// Kill process (capital K for safety)
			visiblePorts := m.getVisiblePorts()
			if len(visiblePorts) > 0 && m.cursor < len(visiblePorts) {
				port := visiblePorts[m.cursor]
				cmd := exec.Command("kill", port.PID)
				err := cmd.Run()
				if err != nil {
					m.message = fmt.Sprintf("Failed to kill PID %s: %v", port.PID, err)
				} else {
					m.message = fmt.Sprintf("Killed process %s (PID %s)", port.Command, port.PID)
					// Remove from list
					m.ports = removePort(m.ports, port)
					if m.cursor >= len(m.getVisiblePorts()) && m.cursor > 0 {
						m.cursor--
					}
				}
			}

		case "o":
			// Open port URL in browser
			visiblePorts := m.getVisiblePorts()
			if len(visiblePorts) > 0 && m.cursor < len(visiblePorts) {
				port := visiblePorts[m.cursor]
				url := fmt.Sprintf("http://localhost:%d", port.Port)

				// Use 'open' command on macOS
				cmd := exec.Command("open", url)
				err := cmd.Run()
				if err != nil {
					m.message = fmt.Sprintf("Failed to open %s: %v", url, err)
				} else {
					m.message = fmt.Sprintf("Opened %s in browser", url)
				}
			}
		}
	}

	return m, nil
}

func removePort(ports []PortInfo, toRemove PortInfo) []PortInfo {
	result := []PortInfo{}
	for _, p := range ports {
		if p.Port != toRemove.Port || p.PID != toRemove.PID {
			result = append(result, p)
		}
	}
	return result
}

func (m model) getVisiblePorts() []PortInfo {
	var visible []PortInfo
	for _, port := range m.ports {
		key := fmt.Sprintf("%d-%s", port.Port, port.PID)
		if !m.config.HiddenPorts[key] {
			// Filter by range if not showing all
			if m.showAll {
				visible = append(visible, port)
			} else {
				// Only show 3000+, 4000+, 8000+
				if (port.Port >= 3000 && port.Port < 4000) ||
					(port.Port >= 4000 && port.Port < 5000) ||
					(port.Port >= 8000 && port.Port < 9000) {
					visible = append(visible, port)
				}
			}
		}
	}
	return visible
}

func (m model) View() string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("cyan")).
		MarginBottom(1)

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("cyan"))

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("240")).
		Foreground(lipgloss.Color("white"))

	messageStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("yellow")).
		MarginTop(1)

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("244")).
		MarginTop(1)

	var s strings.Builder

	title := "PORT MONITOR - Interactive Mode"
	if m.showAll {
		title += " [ALL PORTS]"
	}
	s.WriteString(titleStyle.Render(title))
	s.WriteString("\n\n")

	// Header
	header := headerStyle.Render(fmt.Sprintf("%-6s %-16s %-8s %-8s %-18s %s",
		"PORT", "COMMAND", "PID", "UPTIME", "ADDRESS", "PATH"))
	s.WriteString(header)
	s.WriteString("\n")
	s.WriteString(strings.Repeat("─", 110))
	s.WriteString("\n")

	// Rows
	visiblePorts := m.getVisiblePorts()
	if len(visiblePorts) == 0 {
		s.WriteString("No ports to display\n")
	} else {
		for i, port := range visiblePorts {
			pathDisplay := shortenPath(port.Path)
			if pathDisplay == "N/A" {
				pathDisplay = "-"
			}

			line := fmt.Sprintf("%-6d %-16s %-8s %-8s %-18s %s",
				port.Port,
				truncate(port.Command, 16),
				truncate(port.PID, 8),
				truncate(port.Uptime, 8),
				truncate(port.Address, 18),
				truncate(pathDisplay, 50))

			if i == m.cursor {
				line = selectedStyle.Render(line)
			}
			s.WriteString(line)
			s.WriteString("\n")
		}
	}

	// Message
	if m.message != "" {
		s.WriteString("\n")
		s.WriteString(messageStyle.Render(m.message))
		s.WriteString("\n")
	}

	// Help
	s.WriteString("\n")
	help := helpStyle.Render(
		"↑/k: up • ↓/j: down • o: open in browser • h: hide • u: unhide all • K: kill • a: toggle all • q: quit")
	s.WriteString(help)

	return s.String()
}

func runInteractive(ports []PortInfo) error {
	p := tea.NewProgram(initialModel(ports))
	_, err := p.Run()
	return err
}
