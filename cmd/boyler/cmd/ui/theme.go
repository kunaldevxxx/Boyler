package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"
)

// Theme owns all terminal presentation decisions. Data-oriented output should
// bypass it so JSON, templates and piped output remain stable.
type Theme struct {
	writer   io.Writer
	render   *lipgloss.Renderer
	enabled  bool
	terminal bool
	unicode  bool
}

func ValidColorMode(mode string) bool {
	switch mode {
	case ColorAuto, ColorAlways, ColorNever:
		return true
	default:
		return false
	}
}

func NewTheme(writer io.Writer, mode string) Theme {
	enabled := mode == ColorAlways
	if mode == ColorAuto {
		enabled = isTerminal(writer) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	}

	renderer := lipgloss.NewRenderer(writer)
	if mode == ColorAlways {
		renderer.SetColorProfile(termenv.TrueColor)
	}
	if !enabled {
		renderer.SetColorProfile(termenv.Ascii)
	}

	return Theme{
		writer:   writer,
		render:   renderer,
		enabled:  enabled,
		terminal: isTerminal(writer),
		unicode:  os.Getenv("TERM") != "dumb",
	}
}

func (t Theme) Enabled() bool  { return t.enabled }
func (t Theme) Terminal() bool { return t.terminal }

func (t Theme) Width() int {
	if file, ok := t.writer.(*os.File); ok {
		if width, _, err := term.GetSize(file.Fd()); err == nil && width > 0 {
			return width
		}
	}
	return 80
}

func (t Theme) Symbol(unicode, ascii string) string {
	if t.unicode {
		return unicode
	}
	return ascii
}

func (t Theme) Brand(value string) string   { return t.style(value, "#FF4D8D", true) }
func (t Theme) Success(value string) string { return t.style(value, "#35D07F", true) }
func (t Theme) Warning(value string) string { return t.style(value, "#F2B84B", true) }
func (t Theme) Error(value string) string   { return t.style(value, "#FF5C5C", true) }
func (t Theme) Muted(value string) string   { return t.style(value, "#7C8493", false) }
func (t Theme) Heading(value string) string { return t.style(value, "#FF8A3D", true) }
func (t Theme) Label(value string) string   { return t.style(value, "#7C8493", true) }

func (t Theme) style(value, color string, bold bool) string {
	if !t.enabled {
		return value
	}
	return t.render.NewStyle().Foreground(lipgloss.Color(color)).Bold(bold).Render(value)
}

func isTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}
