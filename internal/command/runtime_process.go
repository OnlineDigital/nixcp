package command

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
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
			if err := runtimeSystemctl(c, runtime, "--user", "restart", target.unit(runtimeProjectSlug(project))); err != nil {
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
	home, err := runtimeHome(runtime)
	if err != nil {
		return err
	}
	unit := target.unit(runtimeProjectSlug(project))
	path := filepath.Join(home, ".config", "systemd", "user", unit)
	if err := writeGenerated(path, []byte(renderRuntimeUnit(target, project, flags))); err != nil {
		return apperrors.New("runtime_unit_write_failed", err.Error(), "Check that your user systemd directory is writable", apperrors.ExitCodeRuntime)
	}
	if err := runtimeSystemctl(cmd, runtime, "--user", "daemon-reload"); err != nil {
		return err
	}
	if err := runtimeSystemctl(cmd, runtime, "--user", "enable", "--now", unit); err != nil {
		return err
	}
	return emitRuntimeResult(cmd, "enable", target, true)
}

func disableRuntime(cmd *cobra.Command, runtime Runtime, target runtimeTarget) error {
	if target == runtimeSchedule {
		return updateSchedule(cmd, runtime, "", false)
	}
	home, err := runtimeHome(runtime)
	if err != nil {
		return err
	}
	project, err := runtimeProjectDir()
	if err != nil {
		return err
	}
	unit := target.unit(runtimeProjectSlug(project))
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
	return emitRuntimeResult(cmd, "disable", target, true)
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
	argv := []string{"ncp", "php", "artisan"}
	switch target {
	case runtimeQueue:
		argv = append(argv, "queue:work")
	case runtimeHorizon:
		argv = append(argv, "horizon")
	case runtimeVite:
		argv = []string{"npm", "run", "dev"}
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
	return "[Unit]\nDescription=NixCP Laravel " + string(target) + "\nAfter=network.target\n\n[Service]\nType=simple\nWorkingDirectory=" + systemdPath(project) + "\nExecStart=" + systemdArgs(argv) + "\nRestart=on-failure\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n"
}

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
		current = strings.TrimRight(current, "\n") + "\n" + scheduleBegin + "\n* * * * * cd " + shellQuote(project) + " && ncp php artisan schedule:run >/dev/null 2>&1\n" + scheduleEnd + "\n"
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
