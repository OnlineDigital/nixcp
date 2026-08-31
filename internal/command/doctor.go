package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/ui"
	"github.com/spf13/cobra"
)

type doctorCheck struct {
	Name   string
	Status string // pass | fail | warn | skip
	Detail string
}

type doctorReport struct {
	Configured bool          `json:"configured"`
	Checks     []doctorCheck `json:"checks"`
	Healthy    bool          `json:"healthy"`
}

// newDoctorCommand builds the read-only diagnostics command. Checks never
// mutate anything; failures are reported, not repaired.
func newDoctorCommand(runtime Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Run read-only diagnostic checks",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDoctor(cmd, runtime) },
	}
}

func runDoctor(cmd *cobra.Command, runtime Runtime) error {
	report := doctorReport{Configured: false, Healthy: false}

	// 1. State store + permissions.
	home := runtime.StateHome
	if home == "" {
		home = os.Getenv("HOME")
	}
	checks := []doctorCheck{
		checkOwnerPermissions(home),
		checkGeneratedModule(home),
	}

	store := state.NewStore(home)
	snap, loadErr := store.Load()
	if loadErr != nil {
		// The ownership check already reported the root cause; only add a
		// fail entry when it passed but the store still cannot be read.
		if checks[0].Status == "pass" {
			checks = append(checks, doctorCheck{Name: "state", Status: "fail", Detail: "state store unreadable: " + loadErr.Error()})
		}
		report.Checks = checks
		return emitDoctor(cmd, report)
	}
	report.Configured = true
	checks = append(checks, doctorCheck{Name: "config", Status: "pass", Detail: fmt.Sprintf("validated schema %d configuration and %d site records", snap.Config.SchemaVersion, len(snap.Sites))})

	// 2. Static artifacts referenced by the rendered module.
	if moduleErr := checkStaticArtifacts(store); moduleErr != nil {
		checks = append(checks, *moduleErr)
	} else {
		checks = append(checks, doctorCheck{Name: "artifacts", Status: "pass", Detail: "static artifacts present"})
	}

	// 3. NixOS import wiring (read-only grep of the generated module path in
	// the classic configuration; a miss is a warning, not a failure).
	checks = append(checks, checkNixImport(runtime, snap))

	// 4. Rebuild toolchain on PATH (nixos-rebuild binary present).
	checks = append(checks, checkToolchain(cmd.Context(), runtime))

	// 5. Confirm each configured service matches the state that will be
	// rendered into NixOS. Systemd can be absent on a development machine, so
	// an unavailable probe is a warning rather than a misleading failure.
	checks = append(checks, checkServiceDiagnostics(cmd, runtime, snap.Config)...)

	report.Healthy = allPassed(checks)
	report.Checks = checks
	return emitDoctor(cmd, report)
}

func checkServiceDiagnostics(cmd *cobra.Command, runtime Runtime, config state.ConfigSnapshot) []doctorCheck {
	statuses := collectServiceStatus(cmd, runtime, config)
	checks := make([]doctorCheck, 0, len(statuses))
	for _, name := range []service.Name{service.Nginx, service.MariaDB, service.Redis} {
		status := statuses[string(name)]
		check := doctorCheck{Name: "service." + string(name)}
		if status.Actual == nil {
			check.Status = "warn"
			check.Detail = "actual state unavailable: " + status.Error
		} else if *status.Drift {
			check.Status = "fail"
			check.Detail = fmt.Sprintf("desired=%s installed=%t but active=%t enabled=%t health=%s", status.Desired.DesiredState, status.Desired.Installed, status.Actual.Active, status.Actual.Enabled, status.Actual.Health)
		} else {
			check.Status = "pass"
			check.Detail = fmt.Sprintf("desired state matches active=%t enabled=%t", status.Actual.Active, status.Actual.Enabled)
		}
		checks = append(checks, check)
	}
	return checks
}

func emitDoctor(cmd *cobra.Command, report doctorReport) error {
	if commandJSON(cmd) {
		// JSON consumers (automation) must see the failure exit code too:
		// write the report envelope, then surface doctor_failed so the
		// process exit code matches the human path.
		if err := emitJSON(cmd, output.Success("doctor", false, report, nil)); err != nil {
			return err
		}
		if report.Configured && !report.Healthy {
			return apperrors.New("doctor_failed", "one or more diagnostic checks failed", "See the failing checks in the JSON report above", apperrors.ExitCodeRuntime)
		}
		return nil
	}
	for _, c := range report.Checks {
		marker := "FAIL"
		switch c.Status {
		case "pass":
			marker = " ok "
		case "warn":
			marker = "warn"
		case "skip":
			marker = "skip"
		}
		// Marker is colorized only on a TTY without NO_COLOR; plain mode
		// stays byte-identical for scripts.
		switch c.Status {
		case "pass":
			marker = ui.OKLine(marker)
		case "warn":
			marker = ui.WarnLine(marker)
		case "skip":
			// skip is informational; no styling.
		default:
			marker = ui.FailLine(marker)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", marker, c.Name, c.Detail)
	}
	if !report.Configured {
		fmt.Fprintln(cmd.OutOrStdout(), "doctor: not-configured (run: ncp install)")
		return nil
	}
	if !report.Healthy {
		return apperrors.New("doctor_failed", "one or more diagnostic checks failed", "Address the [FAIL] items above; doctor is read-only and repairs nothing", apperrors.ExitCodeRuntime)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "doctor: healthy")
	return nil
}

func allPassed(checks []doctorCheck) bool {
	for _, c := range checks {
		if c.Status == "fail" {
			return false
		}
	}
	return true
}

func checkOwnerPermissions(home string) doctorCheck {
	root := filepath.Join(home, ".nixcp")
	info, err := os.Lstat(root)
	if err != nil {
		return doctorCheck{Name: "state", Status: "skip", Detail: "no state directory (run: ncp install)"}
	}
	if !info.Mode().IsDir() {
		return doctorCheck{Name: "state", Status: "fail", Detail: root + " is not a directory"}
	}
	if info.Mode().Perm() != 0700 {
		return doctorCheck{Name: "state", Status: "warn", Detail: fmt.Sprintf("%s has mode %o (expected 0700)", root, info.Mode().Perm())}
	}
	return doctorCheck{Name: "state", Status: "pass", Detail: root + " mode 0700"}
}

func checkGeneratedModule(home string) doctorCheck {
	p := filepath.Join(home, ".nixcp", "generated", "nixcp-module.nix")
	info, err := os.Lstat(p)
	if err != nil {
		return doctorCheck{Name: "module", Status: "warn", Detail: "generated module missing (run: ncp install)"}
	}
	if info.Mode().IsRegular() {
		return doctorCheck{Name: "module", Status: "pass", Detail: p}
	}
	return doctorCheck{Name: "module", Status: "fail", Detail: p + " is not a regular file"}
}

func checkStaticArtifacts(store *state.Store) *doctorCheck {
	for _, rel := range []string{
		filepath.Join("generated", "nixcp-module.nix"),
	} {
		if _, err := os.Lstat(filepath.Join(store.Root, rel)); err != nil {
			return &doctorCheck{Name: "artifacts", Status: "warn", Detail: rel + " missing (rerun ncp install to regenerate)"}
		}
	}
	return nil
}

// checkNixImport verifies the classic NixOS configuration imports the
// generated module. Read-only: it looks for the import line only.
func checkNixImport(runtime Runtime, snap state.Snapshot) doctorCheck {
	modulePath := filepath.Join(snap.Config.Owner.Home, ".nixcp", "generated", "nixcp-module.nix")
	needle := strings.TrimSuffix(nixPath(modulePath), " ")
	conf, err := os.ReadFile("/etc/nixos/configuration.nix")
	if err != nil {
		return doctorCheck{Name: "import", Status: "skip", Detail: "classic /etc/nixos/configuration.nix not readable (flake hosts: verify the import manually)"}
	}
	if strings.Contains(string(conf), needle) {
		return doctorCheck{Name: "import", Status: "pass", Detail: "configuration.nix imports the generated module"}
	}
	return doctorCheck{Name: "import", Status: "warn", Detail: "generated module not imported yet; add " + needle + " to configuration.nix"}
}

// checkToolchain verifies nixos-rebuild is resolvable on PATH.
func checkToolchain(ctx context.Context, runtime Runtime) doctorCheck {
	res, err := runtime.Runner.Run(ctx, &execx.Command{Name: "nixos-rebuild", Args: []string{"--version"}})
	if err != nil || res.ExitCode != 0 {
		return doctorCheck{Name: "toolchain", Status: "warn", Detail: "nixos-rebuild not available on PATH (switch operations will fail)"}
	}
	first := strings.TrimSpace(res.Stdout)
	if i := strings.IndexAny(first, "\r\n"); i >= 0 {
		first = first[:i]
	}
	if first == "" {
		first = "nixos-rebuild"
	}
	return doctorCheck{Name: "toolchain", Status: "pass", Detail: first}
}
