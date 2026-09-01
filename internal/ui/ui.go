// Package ui centralizes the interactive UX contract: huh prompts are used
// only in a real TTY, only when a genuine choice is missing, and never under
// --json or --no-input. lipgloss styling is disabled for non-TTY, NO_COLOR
// and --json so machine output stays byte-stable.
package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/muesli/termenv"
)

// Mode captures the CLI flags that gate interactivity.
type Mode struct {
	JSON    bool
	NoInput bool
	Yes     bool
}

// Interactive reports whether prompting is allowed right now: a TTY on stdin,
// no --json, and no --no-input.
func (m Mode) Interactive() bool {
	return !m.JSON && !m.NoInput && isTTY(os.Stdin)
}

// Confirmable reports whether a confirmation prompt may be shown: prompting
// is allowed and the user has not passed --yes.
func (m Mode) Confirmable() bool { return m.Interactive() && !m.Yes }

func isTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(f.Fd())
}

// StdinIsTTY reports whether stdin is attached to a terminal. Used by the
// TUI launcher to decide whether the interactive panel can render.
func StdinIsTTY() bool { return isTTY(os.Stdin) }

// StdoutIsTTY reports whether stdout is attached to a terminal.
func StdoutIsTTY() bool { return isTTY(os.Stdout) }

// SelectOption is one entry of a single-choice prompt.
type SelectOption struct {
	Value string
	Label string
	Desc  string
}

// Select prompts the user to choose one value when a genuine choice is
// missing. It must only be called when Mode.Interactive() is true; other
// call sites should fall back to their documented error, never to a silent
// default. Returns the chosen value.
func Select(title, description string, options []SelectOption) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("no options available")
	}
	opts := make([]huh.Option[string], 0, len(options))
	for _, o := range options {
		label := o.Label
		if o.Desc != "" {
			label = o.Label + " — " + o.Desc
		}
		opts = append(opts, huh.NewOption[string](label, o.Value))
	}
	var value string
	form := huh.NewForm(
		huh.NewGroup(huh.NewSelect[string]().
			Title(title).
			Description(description).
			Options(opts...).
			Value(&value)),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	return value, nil
}

// Confirm asks a yes/no question. Must only be called when
// Mode.Confirmable() is true; callers gate --yes and non-TTY themselves.
func Confirm(title string) (bool, error) {
	var value bool
	form := huh.NewForm(
		huh.NewGroup(huh.NewConfirm().
			Title(title).
			Value(&value)),
	)
	if err := form.Run(); err != nil {
		return false, err
	}
	return value, nil
}

// styler lazily builds a lipgloss renderer that respects NO_COLOR and
// non-terminal output. nil until first use.
var styler *lipgloss.Renderer

func renderer() *lipgloss.Renderer {
	if styler == nil {
		styler = lipgloss.NewRenderer(os.Stdout)
		if os.Getenv("NO_COLOR") != "" || !isTTY(os.Stdout) {
			styler.SetColorProfile(termenv.Ascii)
		}
	}
	return styler
}

// Heading renders a section heading, styled only in a TTY without NO_COLOR.
func Heading(s string) string {
	if isPlain() {
		return s
	}
	return renderer().NewStyle().Bold(true).Underline(true).Render(s)
}

// OKLine renders a success line, styled only in a TTY without NO_COLOR.
func OKLine(s string) string {
	if isPlain() {
		return s
	}
	return renderer().NewStyle().Bold(true).Foreground(lipgloss.Color("42")).Render(s)
}

// WarnLine renders a warning line, styled only in a TTY without NO_COLOR.
func WarnLine(s string) string {
	if isPlain() {
		return s
	}
	return renderer().NewStyle().Bold(true).Foreground(lipgloss.Color("214")).Render(s)
}

// FailLine renders a failure line, styled only in a TTY without NO_COLOR.
func FailLine(s string) string {
	if isPlain() {
		return s
	}
	return renderer().NewStyle().Bold(true).Foreground(lipgloss.Color("196")).Render(s)
}

func isPlain() bool {
	return os.Getenv("NO_COLOR") != "" || !isTTY(os.Stdout)
}

// Bullets renders a simple bulleted list without any styling escapes.
func Bullets(items []string) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString("  - ")
		b.WriteString(item)
		b.WriteByte('\n')
	}
	return b.String()
}
