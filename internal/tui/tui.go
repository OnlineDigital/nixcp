// Package tui implements the interactive NixCP panel: a bubbletea-based
// tabbed interface over the same use-cases as the CLI. The TUI never
// implements domain logic itself; every mutation goes through the exact
// runService/mutatePHP/link/unlink paths that the CLI commands use, so the
// YAML desired state and the locked transactions remain the single source
// of truth.
package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Backend exposes the operations the TUI can trigger. The command package
// wires these to the existing use-case functions (runService, mutatePHP,
// runLink, runUnlink); the TUI itself stays free of cobra and output
// formatting so both front-ends share one behavior.
type Backend interface {
	// Snapshot returns the current desired state (read-only).
	Snapshot(ctx context.Context) (SnapshotData, error)
	// ServiceStatus reports actual systemd state for the allowlist.
	ServiceStatus(ctx context.Context, service string) (ServiceActual, error)
	// SiteHealth probes a site's FPM socket and local HTTP reachability.
	SiteHealth(ctx context.Context, domain, siteID string, enabled bool) (SiteHealthData, error)
	// PHPInstall installs a PHP version (idempotent) and returns the phase.
	PHPInstall(ctx context.Context, version string) (ActionResult, error)
	// PHPUninstall removes an unused PHP version.
	PHPUninstall(ctx context.Context, version string) (ActionResult, error)
	// PHPUseGlobal selects the global default version.
	PHPUseGlobal(ctx context.Context, version string) (ActionResult, error)
	// PHPExtInstall enables a curated extension.
	PHPExtInstall(ctx context.Context, ext string) (ActionResult, error)
	// ServiceAction runs install/start/stop/restart through a transaction.
	ServiceAction(ctx context.Context, service, action string) (ActionResult, error)
	// LinkSite creates a site; the request was already validated field by
	// field by the form overlay, so only semantic checks remain server-side.
	LinkSite(ctx context.Context, req LinkRequest) (LinkResult, error)
	// UnlinkSite removes a site's vhost/pool/manifest only.
	UnlinkSite(ctx context.Context, domain string) (ActionResult, error)
}

// SnapshotData is the read-only view of ~/.nixcp the TUI renders.
type SnapshotData struct {
	Configured    bool
	Schema        int
	Owner         string
	StateRoot     string
	RebuildMode   string
	RebuildTarget string
	PHPInstalled  []string
	PHPDefault    string
	Extensions    []string
	Services      []ServiceConfigData
	Sites         []SiteData
}

type ServiceConfigData struct {
	Name         string
	Installed    bool
	DesiredState string
}

type ServiceActual struct {
	Active  bool
	Enabled bool
	Health  string
}

type SiteData struct {
	ID           string
	Domain       string
	Enabled      bool
	PHP          string
	ProjectPath  string
	DocumentRoot string
	HandlerType  string
	HandlerName  string
	Database     string
}

type SiteHealthData struct {
	SocketOK   bool
	HTTPOK     bool
	HTTPStatus int
	Healthy    bool
	Describe   string
}

// LinkRequest is the validated form payload for the link overlay.
type LinkRequest struct {
	Domain     string
	PHP        string
	Template   string // laravel|wordpress|generic|custom
	CustomPath string
	MariaDB    string
	Path       string
	Root       string
}

// LinkResult is the successful link outcome (credentials included so the
// TUI can show them once, exactly like `ncp link` does).
type LinkResult struct {
	ID         string
	Domain     string
	Phase      string
	Database   string
	DBUser     string
	DBPassword string
}

// ActionResult is the common outcome of any mutation.
type ActionResult struct {
	Changed  bool
	Phase    string
	Warnings []string
}

// Action describes one in-flight mutation for the Activity tab.
type Action struct {
	Label     string // "install php 8.4"
	Start     time.Time
	Phase     string // latest transaction phase reported while running
	Done      bool
	Err       error // AppError-compatible when failed
	Cancelled bool
	Result    ActionResult

	// cancel aborts the in-flight backend call. The CLI transaction
	// unwinds via its rollback path, so the host never keeps partial state.
	cancel context.CancelFunc
}

// Cancel aborts the running action (no-op when already finished).
func (a *Action) Cancel() {
	if a != nil && a.cancel != nil {
		a.Cancelled = true
		a.cancel()
	}
}

// Run launches the TUI program. It refuses to start on a non-TTY so piped or
// scripted invocations can never hang on a UI that cannot render.
func Run(backend Backend) error {
	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		return fmt.Errorf("tui requires an interactive terminal")
	}
	p := tea.NewProgram(&program{model: newRootModel(backend)}, tea.WithAltScreen(), tea.WithContext(context.Background()))
	_, err := p.Run()
	return err
}

// program adapts rootModel to the tea.Model interface (Update must return
// tea.Model; the concrete type stays rootModel for direct testing).
type program struct{ model rootModel }

func (p *program) Init() tea.Cmd { return p.model.Init() }

func (p *program) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := p.model.Update(msg)
	p.model = m
	return p, cmd
}

func (p *program) View() string { return p.model.View() }

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// palette adapts to the terminal like internal/ui does (NO_COLOR respected
// via termenv default handling; lipgloss v1 renderer picks the profile).
var (
	termProfile = termenv.ColorProfile()
	theme       = newPalette()
)

type palette struct {
	brand, subtle, active, inactive lipgloss.Style
	ok, warn, err, info             lipgloss.Style
	box                             lipgloss.Style
	title                           lipgloss.Style
	helpBar                         lipgloss.Style
}

func newPalette() palette {
	if termProfile == termenv.Ascii {
		mono := lipgloss.NewStyle()
		return palette{
			brand: mono, subtle: mono, active: mono, inactive: mono,
			ok: mono, warn: mono, err: mono, info: mono,
			box: mono, title: mono, helpBar: mono,
		}
	}
	base := lipgloss.NewRenderer(os.Stdout)
	return palette{
		brand:    base.NewStyle().Foreground(lipgloss.Color("62")),
		subtle:   base.NewStyle().Foreground(lipgloss.Color("241")),
		active:   base.NewStyle().Foreground(lipgloss.Color("205")).Bold(true),
		inactive: base.NewStyle().Foreground(lipgloss.Color("241")),
		ok:       base.NewStyle().Foreground(lipgloss.Color("42")),
		warn:     base.NewStyle().Foreground(lipgloss.Color("214")),
		err:      base.NewStyle().Foreground(lipgloss.Color("196")),
		info:     base.NewStyle().Foreground(lipgloss.Color("39")),
		box:      base.NewStyle().Border(lipgloss.RoundedBorder()),
		title:    base.NewStyle().Bold(true),
		helpBar:  base.NewStyle().Foreground(lipgloss.Color("241")),
	}
}

// now is swappable for deterministic tests.
var now = time.Now
