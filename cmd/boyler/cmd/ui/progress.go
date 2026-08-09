package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ProgressMsg struct {
	ID       string
	Status   string
	Progress float64
	Current  int64
	Total    int64
}

type DoneMsg struct {
	Image string
	Err   error
}

type layer struct {
	status   string
	progress float64
	current  int64
	total    int64
}

type Model struct {
	events <-chan tea.Msg
	order  []string
	layers map[string]*layer
	done   bool
	err    error
	image  string
}

func New(events <-chan tea.Msg) Model {
	return Model{
		events: events,
		layers: make(map[string]*layer),
	}
}

func waitForMsg(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-events
		if !ok {
			return DoneMsg{}
		}
		return msg
	}
}

func (m Model) Init() tea.Cmd {
	return waitForMsg(m.events)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ProgressMsg:
		l, ok := m.layers[msg.ID]
		if !ok {
			l = &layer{}
			m.layers[msg.ID] = l
			m.order = append(m.order, msg.ID)
		}
		l.status = msg.Status
		l.progress = msg.Progress
		l.current = msg.Current
		l.total = msg.Total
		return m, waitForMsg(m.events)

	case DoneMsg:
		m.done = msg.Err == nil
		m.err = msg.Err
		m.image = msg.Image
		return m, tea.Quit

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

const barWidth = 30

var (
	barStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	doneStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
)

func renderBar(progress float64) string {
	filled := int(progress * barWidth)
	if filled < 0 {
		filled = 0
	}
	if filled > barWidth {
		filled = barWidth
	}
	if filled == barWidth {
		return barStyle.Render(strings.Repeat("=", barWidth))
	}
	completed := strings.Repeat("=", filled)
	marker := ""
	if filled > 0 {
		marker = ">"
	}
	remaining := strings.Repeat(" ", barWidth-filled-len(marker))
	return barStyle.Render(completed+marker) + remaining
}

func (m Model) View() string {
	var b strings.Builder
	for _, id := range m.order {
		l := m.layers[id]
		if l.status == "Pull complete" {
			b.WriteString(doneStyle.Render(fmt.Sprintf("%s: Pull complete", id)) + "\n")
			continue
		}
		b.WriteString(fmt.Sprintf("%s: %-11s [%s] %s/%s\n",
			id, l.status, renderBar(l.progress), humanSize(l.current), humanSize(l.total)))
	}
	if m.done {
		b.WriteString("\n" + doneStyle.Render(fmt.Sprintf("Status: Downloaded newer image for %s", displayImage(m.image))) + "\n")
	}
	return b.String()
}

func displayImage(value string) string {
	value = strings.TrimPrefix(value, "docker.io/")
	lastSlash := strings.LastIndex(value, "/")
	if strings.LastIndex(value, ":") <= lastSlash {
		return value + ":latest"
	}
	return value
}

func (m Model) Err() error {
	return m.err
}

func humanSize(value int64) string {
	if value < 0 {
		return "?"
	}
	const unit = 1000
	if value < unit {
		return fmt.Sprintf("%dB", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f%cB", float64(value)/float64(div), "kMGT"[exp])
}
