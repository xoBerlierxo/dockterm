package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/xoberlierxo/dockterm/docker"
)

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("62")).
			MarginBottom(1)

	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("241"))

	styleSelected = lipgloss.NewStyle().
			Background(lipgloss.Color("62")).
			Foreground(lipgloss.Color("230")).
			Bold(true)

	styleNormal = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	styleCPUHigh = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).Bold(true) // red

	styleCPULow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")) // green

	styleMemHigh = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).Bold(true) // orange

	styleMemLow = lipgloss.NewStyle().
			Foreground(lipgloss.Color("82")) // green

	styleHelp = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginTop(1)
)

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	containers []docker.ContainerStat
	order      []string
	cursor     int
	width      int
	height     int
	statsChan  chan docker.ContainerStat
	client     *docker.DockerClient // add this
}

// ── Messages ──────────────────────────────────────────────────────────────────

// statMsg is a custom Bubbletea message carrying new stats
type statMsg docker.ContainerStat

// waitForStat is a Bubbletea command that listens on the channel
// and returns the next stat as a message
func waitForStat(ch chan docker.ContainerStat) tea.Cmd {
	return func() tea.Msg {
		return statMsg(<-ch)
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return waitForStat(m.statsChan)
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}

		case "down", "j":
			if m.cursor < len(m.containers)-1 {
				m.cursor++
			}

		case "x":
			if len(m.containers) == 0 {
				break
			}

			target := m.containers[m.cursor]

			go func() {
				m.client.StopContainer(target.ID)
			}()

			m.containers = append(m.containers[:m.cursor], m.containers[m.cursor+1:]...)

			if m.cursor >= len(m.containers) && m.cursor > 0 {
				m.cursor--
			}

		}

	case statMsg:
		stat := docker.ContainerStat(msg)

		// Check if we already have this container
		found := false
		for i, c := range m.containers {
			if c.ID == stat.ID {
				m.containers[i] = stat // update in place
				found = true
				break
			}
		}

		// New container - add it and track its order
		if !found {
			m.containers = append(m.containers, stat)
			m.order = append(m.order, stat.ID)
		}

		// Keep listening for the next stat
		return m, waitForStat(m.statsChan)

	}

	return m, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	if len(m.containers) == 0 {
		return "\n  Connecting to Docker...\n"
	}

	var sb strings.Builder

	// Title
	sb.WriteString(styleTitle.Render("  DockTerm — Live Container Monitor"))
	sb.WriteString("\n")

	// Header row
	header := fmt.Sprintf("  %-14s %-22s %-10s %-10s %-10s",
		"ID", "NAME", "STATE", "CPU%", "MEM%")
	sb.WriteString(styleHeader.Render(header))
	sb.WriteString("\n")
	sb.WriteString(styleHeader.Render("  " + strings.Repeat("─", 66)))
	sb.WriteString("\n")

	// Container rows
	for i, c := range m.containers {
		// Colorize CPU
		cpuStr := fmt.Sprintf("%.2f%%", c.CPUPct)
		if c.CPUPct > 80 {
			cpuStr = styleCPUHigh.Render(cpuStr)
		} else {
			cpuStr = styleCPULow.Render(cpuStr)
		}

		// Colorize Memory
		memStr := fmt.Sprintf("%.2f%%", c.MemPct)
		if c.MemPct > 80 {
			memStr = styleMemHigh.Render(memStr)
		} else {
			memStr = styleMemLow.Render(memStr)
		}

		// Build the row - plain text parts
		row := fmt.Sprintf("  %-14s %-22s %-10s %-10s %-10s",
			c.ID, c.Name, c.State, cpuStr, memStr)

		// Highlight selected row
		if i == m.cursor {
			row = styleSelected.Render(row)
		} else {
			row = styleNormal.Render(row)
		}

		sb.WriteString(row)
		sb.WriteString("\n")
	}

	// Help bar
	sb.WriteString(styleHelp.Render("\n  ↑/↓ navigate   x stop container   q quit"))

	return styleBorder.Render(sb.String())
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	// Connect to Docker
	client, err := docker.NewClient()
	if err != nil {
		log.Fatalf("Error connecting to Docker: %v", err)
	}
	defer client.Close()

	// Get running containers
	containers, err := client.GetContainers()
	if err != nil {
		log.Fatalf("Error fetching containers: %v", err)
	}

	if len(containers) == 0 {
		fmt.Println("No running containers found. Start some with 'docker run -d nginx'")
		return
	}

	// Create the stats channel
	statsChan := make(chan docker.ContainerStat, 10)
	ctx := context.Background()

	// Spawn one goroutine per container
	for _, c := range containers {
		go client.GetContainerStats(ctx, c, statsChan)
	}

	// Build initial model
	m := model{
		statsChan: statsChan,
		client:    client, // add this
	}

	// Start Bubbletea
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running TUI: %v", err)
	}
}
