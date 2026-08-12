package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

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

func (t Theme) Gradient(value string) string {
	if !t.enabled || utf8.RuneCountInString(value) < 2 {
		return value
	}
	runes := []rune(value)
	var b strings.Builder
	for i, char := range runes {
		fraction := float64(i) / float64(len(runes)-1)
		color := gradientColor(fraction)
		b.WriteString(t.render.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(string(char)))
	}
	return b.String()
}

// The brand gradient passes through orange, pink and violet.
func gradientColor(position float64) string {
	type rgb struct{ r, g, b float64 }
	stops := []rgb{{255, 138, 61}, {255, 77, 141}, {155, 109, 255}}
	segment := position * float64(len(stops)-1)
	index := int(segment)
	if index >= len(stops)-1 {
		index = len(stops) - 2
		segment = float64(len(stops) - 1)
	}
	local := segment - float64(index)
	from, to := stops[index], stops[index+1]
	lerp := func(a, b float64) int { return int(a + (b-a)*local) }
	return fmt.Sprintf("#%02X%02X%02X", lerp(from.r, to.r), lerp(from.g, to.g), lerp(from.b, to.b))
}

func isTerminal(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(file.Fd())
}
