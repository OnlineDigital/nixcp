package command

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/spf13/cobra"
)

type runtimeTarget string

const (
	runtimeSchedule runtimeTarget = "schedule"
	runtimeQueue    runtimeTarget = "queue"
	runtimeHorizon  runtimeTarget = "horizon"
	runtimeVite     runtimeTarget = "vite"
	runtimeReverb   runtimeTarget = "reverb"
	runtimeOctane   runtimeTarget = "octane"
	runtimePulse    runtimeTarget = "pulse"
)

func parseRuntimeTarget(raw string) (runtimeTarget, error) {
	switch runtimeTarget(raw) {
	case runtimeSchedule, runtimeQueue, runtimeHorizon, runtimeVite, runtimeReverb, runtimeOctane, runtimePulse:
		return runtimeTarget(raw), nil
	default:
		return "", fmt.Errorf("unsupported runtime service %q", raw)
	}
}

func (t runtimeTarget) unit(slug string) string {
	return "nixcp-" + slug + "-" + string(t) + ".service"
}

var runtimeSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// runtimeProjectSlug creates a stable systemd-safe project name. Runtime
// processes are project-scoped, so their unit must be too: two Laravel sites
// can safely run different queues, Vite servers, or Reverb instances.
func runtimeProjectSlug(project string) string {
	slug := strings.Trim(runtimeSlugChars.ReplaceAllString(strings.ToLower(filepath.Base(filepath.Clean(project))), "-"), "-")
	if slug == "" {
		return "project"
	}
	return slug
}

// runtimeServiceSlug uses the linked site's stable ID when the current
// directory belongs to a site. This makes runtime units easy to identify and
// keeps their names stable if the project directory is renamed. Unlinked
// projects retain the safe directory-name fallback.
func runtimeServiceSlug(runtime Runtime, project string) string {
	// StateHome is only a test override. In production the state lives under
	// the current user's ~/.nixcp, so use the same store resolver as PHP/site
	// commands rather than treating an empty StateHome as the working directory.
	store, err := phpStore(runtime)
	if err != nil {
		return runtimeProjectSlug(project)
	}
	snap, err := store.Load()
	if err != nil {
		return runtimeProjectSlug(project)
	}
	project = filepath.Clean(project)
	for _, site := range snap.Sites {
		if filepath.Clean(site.ProjectPath) == project {
			return site.ID
		}
	}
	return runtimeProjectSlug(project)
}

func newEnableCommand(runtime Runtime) *cobra.Command {
	return runtimeCommand("enable <schedule|queue|horizon|vite|reverb|octane|pulse> [flags...]", "Enable a Laravel runtime process", func(c *cobra.Command, args []string) error {
		target, err := parseRuntimeTarget(args[0])
		if err != nil {
			return apperrors.New("unsupported_runtime_service", err.Error(), "Choose schedule, queue, horizon, vite, reverb, octane, or pulse", apperrors.ExitCodeUsage)
		}
		return enableRuntime(c, runtime, target, args[1:])
	})
}

func newDisableCommand(runtime Runtime) *cobra.Command {
	return runtimeCommand("disable <schedule|queue|horizon|vite|reverb|octane|pulse>", "Disable a Laravel runtime process", func(c *cobra.Command, args []string) error {
		target, err := parseRuntimeTarget(args[0])
		if err != nil {
			return apperrors.New("unsupported_runtime_service", err.Error(), "Choose schedule, queue, horizon, vite, reverb, octane, or pulse", apperrors.ExitCodeUsage)
		}
		if len(args) != 1 {
			return apperrors.New("invalid_runtime_arguments", "disable does not accept runtime flags", "Run: ncp disable "+string(target), apperrors.ExitCodeUsage)
		}
		return disableRuntime(c, runtime, target)
	})
}

func newRestartCommand(runtime Runtime) *cobra.Command {
	return runtimeCommand("restart <queue|horizon|vite|reverb|octane|pulse|php|mariadb|valkey|nginx>", "Restart a managed runtime or system service", func(c *cobra.Command, args []string) error {
		if len(args) != 1 {
			return apperrors.New("invalid_restart_arguments", "restart accepts exactly one target", "Choose queue, horizon, vite, reverb, octane, pulse, php, mariadb, valkey, or nginx", apperrors.ExitCodeUsage)
		}
		if target, err := parseRuntimeTarget(args[0]); err == nil && target != runtimeSchedule {
			project, projectErr := runtimeProjectDir()
			if projectErr != nil {
				return projectErr
			}
			if err := runtimeSystemctl(c, runtime, "--user", "restart", target.unit(runtimeServiceSlug(runtime, project))); err != nil {
				return err
			}
			return emitRuntimeResult(c, "restart", target, true)
		}
		var unit string
		switch args[0] {
		case "php":
			unit = "phpfpm.service"
		case "mariadb":
			unit = "mysql.service"
		case "valkey":
			unit = "redis-nixcp.service"
		case "nginx":
			unit = "nginx.service"
		default:
			return apperrors.New("unsupported_restart_target", "unsupported restart target "+args[0], "Choose queue, horizon, vite, reverb, octane, pulse, php, mariadb, valkey, or nginx", apperrors.ExitCodeUsage)
		}
		res, err := runtime.Runner.Run(c.Context(), &execx.Command{Name: "sudo", Args: []string{"--", "systemctl", "restart", unit}})
		if err != nil || res.ExitCode != 0 {
			return apperrors.New("system_service_restart_failed", strings.TrimSpace(res.Stderr), "Ensure sudo can restart NixCP services", apperrors.ExitCodeRuntime)
		}
		return emitRuntimeResult(c, "restart", runtimeTarget(args[0]), true)
	})
}

func runtimeCommand(use, short string, run func(*cobra.Command, []string) error) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: cobra.MinimumNArgs(1), DisableFlagParsing: true, RunE: func(c *cobra.Command, args []string) error {
		return run(c, passthroughArgs(c, args))
	}}
}

func runtimeHome(runtime Runtime) (string, error) {
	if runtime.StateHome != "" {
		return runtime.StateHome, nil
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.HomeDir, nil
}

func runtimeProjectDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(cwd)
}

func enableRuntime(cmd *cobra.Command, runtime Runtime, target runtimeTarget, flags []string) error {
	project, err := runtimeProjectDir()
	if err != nil {
		return err
	}
	if target == runtimeSchedule {
		return updateSchedule(cmd, runtime, project, true)
	}
	if target == runtimeVite {
		return enableVite(cmd, runtime, project, flags)
	}
	home, err := runtimeHome(runtime)
	if err != nil {
		return err
	}
	unit := target.unit(runtimeServiceSlug(runtime, project))
	if err := enableRuntimeUnit(cmd, runtime, home, unit, renderRuntimeUnit(target, project, flags)); err != nil {
		return err
	}
	return emitRuntimeResult(cmd, "enable", target, true)
}

func enableRuntimeUnit(cmd *cobra.Command, runtime Runtime, home, unit, content string) error {
	path := filepath.Join(home, ".config", "systemd", "user", unit)
	if err := writeGenerated(path, []byte(content)); err != nil {
		return apperrors.New("runtime_unit_write_failed", err.Error(), "Check that your user systemd directory is writable", apperrors.ExitCodeRuntime)
	}
	if err := runtimeSystemctl(cmd, runtime, "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := runtimeSystemctl(cmd, runtime, "--user", "enable", "--now", unit); err != nil {
		return err
	}
	return nil
}

// enableVite reserves a distinct loopback port, persists it in the linked site
// manifest (which renders the matching Nginx proxy location), then starts the
// user unit with the same port. Vite must be enabled from a linked project:
// without a site there is no safe virtual host to modify.
func enableVite(cmd *cobra.Command, runtime Runtime, project string, flags []string) error {
	store, err := siteStore(runtime)
	if err != nil {
		return err
	}
	snap, err := store.Load()
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	idx := -1
	for i := range snap.Sites {
		if filepath.Clean(snap.Sites[i].ProjectPath) == filepath.Clean(project) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return apperrors.New("vite_site_not_found", "Vite requires the current project to be linked", "Run: ncp link <domain> --template laravel", apperrors.ExitCodePrecond)
	}
	site := snap.Sites[idx]
	if !site.Enabled {
		return apperrors.New("vite_site_disabled", "Vite requires an enabled linked site", "Enable or relink the site before enabling Vite", apperrors.ExitCodePrecond)
	}
	if site.Vite == nil {
		port, err := reserveVitePort(snap)
		if err != nil {
			return apperrors.New("vite_port_unavailable", err.Error(), "Stop a local development server and try again", apperrors.ExitCodeRuntime)
		}
		snap.Sites[idx].Vite = &state.ViteConfig{Port: port}
		snap.Canonicalize()
		if err := snap.Validate(); err != nil {
			return apperrors.New("invalid_vite", err.Error(), "Check the linked site configuration", apperrors.ExitCodePrecond)
		}
		// Nginx is switched before the user unit starts, so requests never route
		// to an arbitrary port. Suppress the generic site result; this command
		// reports one coherent Vite result after systemd has accepted the unit.
		if err := applySite(cmd, runtime, store, snap, nil, "vite", snap.Sites[idx], true); err != nil {
			return err
		}
		site = snap.Sites[idx]
	}
	home, err := runtimeHome(runtime)
	if err != nil {
		return err
	}
	unit := runtimeVite.unit(site.ID)
	if err := enableRuntimeUnit(cmd, runtime, home, unit, renderViteRuntimeUnit(project, flags, site.Vite.Port)); err != nil {
		return err
	}
	return emitRuntimeResult(cmd, "enable", runtimeVite, true)
}

func reserveVitePort(snap state.Snapshot) (int, error) {
	used := map[int]bool{}
	for _, site := range snap.Sites {
		if site.Vite != nil {
			used[site.Vite.Port] = true
		}
	}
	for range 64 {
		n, err := rand.Int(rand.Reader, big.NewInt(20000))
		if err != nil {
			return 0, err
		}
		port := 30000 + int(n.Int64())
		if used[port] {
			continue
		}
		listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		_ = listener.Close()
		return port, nil
	}
	return 0, fmt.Errorf("could not find an available loopback port")
}

func disableRuntime(cmd *cobra.Command, runtime Runtime, target runtimeTarget) error {
	if target == runtimeSchedule {
		return updateSchedule(cmd, runtime, "", false)
	}
	if target == runtimeVite {
		return disableVite(cmd, runtime)
	}
	home, err := runtimeHome(runtime)
	if err != nil {
		return err
	}
	project, err := runtimeProjectDir()
	if err != nil {
		return err
	}
	unit := target.unit(runtimeServiceSlug(runtime, project))
	if err := disableRuntimeUnit(cmd, runtime, home, unit); err != nil {
		return err
	}
	return emitRuntimeResult(cmd, "disable", target, true)
}

func disableRuntimeUnit(cmd *cobra.Command, runtime Runtime, home, unit string) error {
	// Stop/disable before deleting the definition, so a running process cannot
	// survive an otherwise successful removal.
	if err := runtimeSystemctl(cmd, runtime, "--user", "disable", "--now", unit); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(home, ".config", "systemd", "user", unit)); err != nil && !os.IsNotExist(err) {
		return apperrors.New("runtime_unit_remove_failed", err.Error(), "Check that your user systemd directory is writable", apperrors.ExitCodeRuntime)
	}
	if err := runtimeSystemctl(cmd, runtime, "--user", "daemon-reload"); err != nil {
		return err
	}
	return nil
}

func disableVite(cmd *cobra.Command, runtime Runtime) error {
	project, err := runtimeProjectDir()
	if err != nil {
		return err
	}
	store, err := siteStore(runtime)
	if err != nil {
		return err
	}
	snap, err := store.Load()
	if err != nil {
		return apperrors.New("not_configured", "NixCP is not initialized", "Run: ncp install", apperrors.ExitCodePrecond)
	}
	idx := -1
	for i := range snap.Sites {
		if filepath.Clean(snap.Sites[i].ProjectPath) == filepath.Clean(project) {
			idx = i
			break
		}
	}
	if idx == -1 || snap.Sites[idx].Vite == nil {
		return apperrors.New("vite_not_enabled", "Vite is not enabled for the current linked site", "Run: ncp enable vite", apperrors.ExitCodePrecond)
	}
	home, err := runtimeHome(runtime)
	if err != nil {
		return err
	}
	if err := disableRuntimeUnit(cmd, runtime, home, runtimeVite.unit(snap.Sites[idx].ID)); err != nil {
		return err
	}
	site := snap.Sites[idx]
	snap.Sites[idx].Vite = nil
	if err := applySite(cmd, runtime, store, snap, nil, "vite", site, true); err != nil {
		return err
	}
	return emitRuntimeResult(cmd, "disable", runtimeVite, true)
}

func runtimeSystemctl(cmd *cobra.Command, runtime Runtime, args ...string) error {
	res, err := runtime.Runner.Run(cmd.Context(), &execx.Command{Name: "systemctl", Args: args})
	if err == nil && res.ExitCode == 0 {
		return nil
	}
	message := strings.TrimSpace(res.Stderr)
	if message == "" {
		message = "systemctl " + strings.Join(args, " ") + " failed"
	}
	return apperrors.New("runtime_systemd_failed", message, "Ensure your user systemd manager is running", apperrors.ExitCodeRuntime)
}

func renderRuntimeUnit(target runtimeTarget, project string, flags []string) string {
	argv := []string{runtimeNCPBinary(), "php", "artisan"}
	switch target {
	case runtimeQueue:
		argv = append(argv, "queue:work")
	case runtimeHorizon:
		argv = append(argv, "horizon")
	case runtimeVite:
		argv = []string{runtimeNPMBinary(), "run", "dev"}
	case runtimeReverb:
		argv = append(argv, "reverb:start")
	case runtimeOctane:
		argv = append(argv, "octane:start")
	case runtimePulse:
		argv = append(argv, "pulse:check")
	}
	// Every user-systemd runtime accepts its tool's native argv. The cron
	// scheduler remains intentionally fixed.
	argv = append(argv, flags...)
	return "[Unit]\nDescription=NixCP Laravel " + string(target) + "\nAfter=network.target\n\n[Service]\nType=simple\nWorkingDirectory=" + systemdPath(project) + "\nEnvironment=PATH=" + systemdPath(runtimePath()) + "\nExecStart=" + systemdArgs(argv) + "\nRestart=on-failure\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n"
}

func renderViteRuntimeUnit(project string, flags []string, port int) string {
	// `npm run` only forwards arguments after `--`. Host and port are enforced
	// last so Vite remains local-only and cannot desynchronise from Nginx.
	argv := []string{runtimeNPMBinary(), "run", "dev", "--"}
	argv = append(argv, flags...)
	argv = append(argv, "--host=127.0.0.1", fmt.Sprintf("--port=%d", port))
	return "[Unit]\nDescription=NixCP Laravel vite\nAfter=network.target\n\n[Service]\nType=simple\nWorkingDirectory=" + systemdPath(project) + "\nEnvironment=PATH=" + systemdPath(runtimePath()) + "\nEnvironment=PORT=" + fmt.Sprintf("%d", port) + "\nExecStart=" + systemdArgs(argv) + "\nRestart=on-failure\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n"
}

// User systemd and cron run with a deliberately sparse environment and do not
// load the interactive shell's Nix profile. Resolve NixCP/npm from the current
// profile explicitly and provide that profile in PATH for child tools.
func runtimeProfileBin() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".nix-profile", "bin")
}

func runtimePath() string {
	profile := runtimeProfileBin()
	if profile == "" {
		return os.Getenv("PATH")
	}
	return profile + ":" + os.Getenv("PATH")
}

func runtimeBinary(name string) string {
	profileBinary := filepath.Join(runtimeProfileBin(), name)
	if _, err := os.Stat(profileBinary); err == nil {
		return profileBinary
	}
	if found, err := exec.LookPath(name); err == nil {
		return found
	}
	return name
}

func runtimeNCPBinary() string { return runtimeBinary("ncp") }
func runtimeNPMBinary() string { return runtimeBinary("npm") }

func systemdArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = systemdArg(a)
	}
	return strings.Join(out, " ")
}
func systemdArg(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

// WorkingDirectory is a systemd path setting, not an ExecStart argv field.
// Quoting it produces an invalid path (including the quote characters); escape
// the few characters systemd's unit parser treats specially instead.
func systemdPath(value string) string {
	return strings.NewReplacer(`\\`, `\\\\`, " ", `\\x20`, "\t", `\\x09`, "\n", `\\x0a`).Replace(value)
}

const scheduleBegin = "# BEGIN NIXCP SCHEDULE"
const scheduleEnd = "# END NIXCP SCHEDULE"

func updateSchedule(cmd *cobra.Command, runtime Runtime, project string, enabled bool) error {
	res, err := runtime.Runner.Run(cmd.Context(), &execx.Command{Name: "crontab", Args: []string{"-l"}})
	current := res.Stdout
	missingTable := err != nil && res.ExitCode == 1
	if err != nil && !missingTable {
		return apperrors.New("schedule_read_failed", strings.TrimSpace(res.Stderr), "Check that crontab is available", apperrors.ExitCodeRuntime)
	}
	if !enabled && missingTable {
		return emitRuntimeResult(cmd, "disable", runtimeSchedule, false)
	}
	current = removeScheduleBlock(current)
	if enabled {
		current = strings.TrimRight(current, "\n") + "\n" + scheduleBegin + "\n* * * * * PATH=" + shellQuote(runtimePath()) + "; export PATH; cd " + shellQuote(project) + " && " + shellQuote(runtimeNCPBinary()) + " php artisan schedule:run >/dev/null 2>&1\n" + scheduleEnd + "\n"
	}
	file, err := os.CreateTemp("", "nixcp-crontab-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.WriteString(current); err == nil {
		err = file.Close()
	} else {
		_ = file.Close()
	}
	if err != nil {
		return err
	}
	apply := []string{name}
	if !enabled && strings.TrimSpace(current) == "" {
		apply = []string{"-r"}
	}
	res, err = runtime.Runner.Run(cmd.Context(), &execx.Command{Name: "crontab", Args: apply})
	if err != nil || res.ExitCode != 0 {
		return apperrors.New("schedule_write_failed", strings.TrimSpace(res.Stderr), "Check that crontab is available", apperrors.ExitCodeRuntime)
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	return emitRuntimeResult(cmd, action, runtimeSchedule, true)
}
func removeScheduleBlock(value string) string {
	start := strings.Index(value, scheduleBegin)
	if start < 0 {
		return value
	}
	end := strings.Index(value[start:], scheduleEnd)
	if end < 0 {
		return value[:start]
	}
	return value[:start] + value[start+end+len(scheduleEnd):]
}
func shellQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
func emitRuntimeResult(cmd *cobra.Command, action string, target runtimeTarget, changed bool) error {
	if commandJSON(cmd) {
		return emitJSON(cmd, map[string]any{"ok": true, "command": action, "changed": changed, "data": map[string]string{"service": string(target)}})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", action, target)
	return nil
}
