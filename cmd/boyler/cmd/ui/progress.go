package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

type tickMsg time.Time

type layer struct {
	status   string
	progress float64
	current  int64
	total    int64
}

type Model struct {
	events  <-chan tea.Msg
	order   []string
	layers  map[string]*layer
	done    bool
	err     error
	image   string
	theme   Theme
	width   int
	frame   int
	started time.Time
}

func New(events <-chan tea.Msg, theme Theme) Model {
	return Model{
		events:  events,
		layers:  make(map[string]*layer),
		theme:   theme,
		width:   theme.Width(),
		started: time.Now(),
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

func tick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(value time.Time) tea.Msg { return tickMsg(value) })
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(waitForMsg(m.events), tick())
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

	case tickMsg:
		m.frame++
		return m, tick()

	case tea.WindowSizeMsg:
		m.width = msg.Width

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.err = fmt.Errorf("pull canceled")
			return m, tea.Quit
		}
	}
	return m, nil
}

func renderBar(theme Theme, progress float64, width int) string {
	if width < 8 {
		width = 8
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 1 {
		progress = 1
	}
	filled := int(progress * float64(width))
	full, empty := theme.Symbol("█", "="), theme.Symbol("░", "-")
	return theme.Success(strings.Repeat(full, filled)) + theme.Muted(strings.Repeat(empty, width-filled))
}

func (m Model) View() string {
	var b strings.Builder
	barWidth := m.width - 57
	if barWidth < 12 {
		barWidth = 12
	}
	if barWidth > 36 {
		barWidth = 36
	}

	for _, id := range m.order {
		l := m.layers[id]
		shortID := id
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		complete := l.progress >= 1 || strings.Contains(strings.ToLower(l.status), "complete")
		cached := strings.EqualFold(l.status, "Already exists")
		percent := int(l.progress*100 + .5)
		status := fmt.Sprintf("%3d%%", percent)
		if cached {
			status = m.theme.Success("already exists")
		} else if complete {
			status = m.theme.Success("complete")
		} else {
			frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			if m.theme.Symbol("unicode", "ascii") == "ascii" {
				frames = []string{"|", "/", "-", "\\"}
			}
			status = m.theme.Brand(frames[m.frame%len(frames)]) + " " + status
		}
		fmt.Fprintf(&b, "  %s  %s  %s / %s  %s\n",
			m.theme.Muted(shortID), renderBar(m.theme, l.progress, barWidth),
			humanSize(l.current), humanSize(l.total), status)
	}
	if m.done {
		duration := time.Since(m.started).Round(100 * time.Millisecond)
		fmt.Fprintf(&b, "\n%s %s\n",
			m.theme.Success(m.theme.Symbol("✓", "+")),
			m.theme.Success(fmt.Sprintf("Image downloaded in %s", duration)))
		fmt.Fprintf(&b, "  %s\n", m.theme.Muted("docker.io/"+displayImage(m.image)))
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

func (m Model) Err() error { return m.err }

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
