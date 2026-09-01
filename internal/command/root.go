package command

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nixcp/nixcp/internal/database"
	"github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/nix"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/platform"
	"github.com/nixcp/nixcp/internal/service"
	sitepkg "github.com/nixcp/nixcp/internal/site"
	"github.com/nixcp/nixcp/internal/transaction"
	"github.com/nixcp/nixcp/internal/tui"
	"github.com/nixcp/nixcp/internal/ui"
	"github.com/spf13/cobra"
)

const defaultVersion = "0.0.0"

// BuildMetadata exposes version/build metadata for `ncp version`.
type BuildMetadata struct {
	Version string
	Commit  string
	BuiltAt string
}

// Runtime holds injected stage-2 adapters.
type Runtime struct {
	Runner        execx.Runner
	Metadata      BuildMetadata
	Platform      platform.Inspector
	Renderer      nix.Renderer
	Services      service.Systemd
	SiteChecker   sitepkg.Checker
	NginxConfig   sitepkg.ConfigVerifier
	DatabaseCheck database.Checker
	Transactions  *transaction.Manager
	StateHome     string // test-only override; production uses the current user's home.
}

// WithStateHome injects an isolated state home for tests.
func WithStateHome(home string) RuntimeOption { return func(rt *Runtime) { rt.StateHome = home } }

// StateHomeOrDefault resolves the state root override or falls back to the
// given user home (production default).
func (rt Runtime) StateHomeOrDefault(fallback string) string {
	if rt.StateHome != "" {
		return rt.StateHome
	}
	return fallback
}

// RuntimeOption configures the composition root.
type RuntimeOption func(*Runtime)

// WithRunner injects a process runner.
func WithRunner(r execx.Runner) RuntimeOption {
	return func(rt *Runtime) {
		if r != nil {
			rt.Runner = r
		}
	}
}

// WithServices injects a systemd adapter (tests use fakes).
func WithServices(s service.Systemd) RuntimeOption {
	return func(rt *Runtime) {
		if s != nil {
			rt.Services = s
		}
	}
}

// WithSiteChecker injects the local site probe used after link transactions.
func WithSiteChecker(c sitepkg.Checker) RuntimeOption {
	return func(rt *Runtime) {
		if c != nil {
			rt.SiteChecker = c
		}
	}
}

// WithNginxConfigVerifier injects the active-Nginx configuration verifier.
func WithNginxConfigVerifier(v sitepkg.ConfigVerifier) RuntimeOption {
	return func(rt *Runtime) {
		if v != nil {
			rt.NginxConfig = v
		}
	}
}

// WithDatabaseChecker injects declared-database verification.
func WithDatabaseChecker(c database.Checker) RuntimeOption {
	return func(rt *Runtime) {
		if c != nil {
			rt.DatabaseCheck = c
		}
	}
}

// WithBuildMetadata injects version/build metadata.
func WithBuildMetadata(meta BuildMetadata) RuntimeOption {
	return func(rt *Runtime) {
		if meta.Version != "" {
			rt.Metadata.Version = meta.Version
		}
		rt.Metadata.Commit = meta.Commit
		rt.Metadata.BuiltAt = meta.BuiltAt
	}
}

// ApplicationRoot wraps the command tree and process lifecycle.
type ApplicationRoot struct {
	Root   *cobra.Command
	Ctx    context.Context
	cancel context.CancelFunc
}

func defaultRuntime() Runtime {
	runner := &execx.RealRunner{}
	return Runtime{
		Runner: runner,
		Metadata: BuildMetadata{
			Version: defaultVersion,
		},
		Platform:      platform.HostInspector{},
		Renderer:      nix.Renderer{},
		Services:      service.Adapter{Runner: runner},
		SiteChecker:   sitepkg.RealChecker{},
		NginxConfig:   sitepkg.NginxConfigVerifier{Runner: runner},
		DatabaseCheck: database.LocalChecker{Runner: runner},
	}
}

// NewRootCommand constructs the root command and all supported subcommands.
func NewRootCommand(ctx context.Context, opts ...RuntimeOption) (*cobra.Command, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	runtime := defaultRuntime()
	for _, opt := range opts {
		opt(&runtime)
	}
	root := &cobra.Command{
		Use:           "ncp",
		Short:         "NixOS development-site control plane",
		Long:          "NixCP manages PHP runtimes, Nginx, local databases, and HTTP sites through validated, rollback-capable NixOS transactions. Use `ncp help <command>` for details and examples at every command level.",
		Example:       "  ncp install\n  ncp service nginx install\n  ncp php install 8.4\n  ncp help php\n  ncp help php install",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Bare `ncp` on an interactive terminal opens the panel; piped and
			// scripted invocations keep the version banner (byte-stable).
			if len(args) == 0 && !commandJSON(cmd) && ui.StdinIsTTY() && ui.StdoutIsTTY() {
				if err := tui.Run(NewTUIBackend(runtime, opts...)); err != nil {
					return errors.New("tui_failed", fmt.Sprintf("interactive panel failed: %v", err), "", errors.ExitCodeRuntime)
				}
				return nil
			}
			if commandJSON(cmd) {
				payload := output.Success("ncp", false, map[string]any{"version": runtime.Metadata.Version}, nil)
				return emitJSON(cmd, payload)
			}
			cmd.Printf("NixCP CLI %s\n", runtime.Metadata.Version)
			return nil
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() == 0 || os.Getenv("SUDO_UID") != "" || os.Getenv("SUDO_USER") != "" {
				return errors.New("unsafe_privileged_execution", "NixCP must run as the configured unprivileged user, not through root or sudo", "Run ncp directly as your normal user", errors.ExitCodePrecond)
			}
			cmd.Root().Annotations["invoked-command"] = cmd.CommandPath()
			// Pass-through commands (php, artisan) disable flag parsing so raw
			// argv reaches the interpreter; the global flags still have to be
			// honored, so parse them from the raw argv tail ourselves and
			// strip them so NixCP-only flags never reach the child process.
			if cmd.DisableFlagParsing {
				cleaned, err := parseGlobalFlags(cmd, args)
				if err != nil {
					return err
				}
				// PersistentPreRunE receives args by value; stash the trimmed
				// argv on the command so RunE re-reads it via passthroughArgs.
				if cmd.Annotations == nil {
					cmd.Annotations = map[string]string{}
				}
				cmd.Annotations["passthrough-args"] = strings.Join(cleaned, "\x00")
			}
			jsonOut, err := commandBoolFlag(cmd, "json")
			if err != nil {
				return errors.New("invalid_flag_state", err.Error(), "", errors.ExitCodePrecond)
			}
			if jsonOut {
				_ = cmd.Flags().Set("no-input", "true")
			}
			// Bind the --timeout flag to the per-invocation context so every
			// long-running child (php, artisan, rebuild) observes the same
			// deadline. Cobra runs PersistentPreRunE after flag parsing, so
			// the value is authoritative here.
			timeout, tErr := commandTimeout(cmd)
			if tErr != nil {
				return errors.New("invalid_flag_state", tErr.Error(), "Use --timeout <duration>, e.g. --timeout 120s", errors.ExitCodePrecond)
			}
			if timeout > 0 {
				invokeCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
				cmd.SetContext(invokeCtx)
				// Cobra exposes no lifecycle hook for derived contexts, so the
				// cancel func is surfaced for the composition root (ApplicationRoot.Execute)
				// through a package-level registry keyed by the root command.
				registerTimeoutCancel(cmd.Root(), cancel)
			}
			return nil
		},
	}

	root.SetContext(ctx)
	root.Annotations = map[string]string{}

	root.PersistentFlags().Bool("json", false, "Emit a single JSON object")
	root.PersistentFlags().Bool("no-input", false, "Disable interactive prompts")
	root.PersistentFlags().Bool("yes", false, "Skip confirmation prompts")
	root.PersistentFlags().Duration("timeout", 30*time.Second, "Operation timeout")

	root.AddCommand(newInstallCommand(runtime))
	root.AddCommand(newStatusCommand(runtime))
	root.AddCommand(newDoctorCommand(runtime))
	root.AddCommand(newServiceCommand(runtime))
	root.AddCommand(newServiceAliasCommand(runtime, service.Nginx))
	root.AddCommand(newServiceAliasCommand(runtime, service.MariaDB))
	root.AddCommand(newServiceAliasCommand(runtime, service.Valkey))
	root.AddCommand(newPHPCommand(runtime))
	root.AddCommand(newArtisanCommand(runtime))
	root.AddCommand(newArtisanAliasCommand(runtime, "a", "artisan shortcut", nil))
	root.AddCommand(newArtisanAliasCommand(runtime, "am", "artisan migrate shortcut", []string{"migrate"}))
	root.AddCommand(newArtisanAliasCommand(runtime, "tinker", "artisan tinker shortcut", []string{"tinker"}))
	root.AddCommand(newComposerCommand(runtime))
	root.AddCommand(newComposerAliasCommand(runtime, "ci", "composer install shortcut", []string{"install"}))
	root.AddCommand(newLinkCommand(runtime))
	root.AddCommand(newUnlinkCommand(runtime))
	root.AddCommand(newSitesCommand(runtime))
	root.AddCommand(newTUICommand(runtime, opts...))
	root.AddCommand(newSkillCommand())
	root.AddCommand(newShellCommand())
	root.AddCommand(newVersionCommand(runtime.Metadata))

	root.InitDefaultCompletionCmd()

	return root, nil
}

// New builds the composition root and applies process-signal cancellation semantics.
func New(ctx context.Context, opts ...RuntimeOption) (*ApplicationRoot, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	rootCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	runtime := defaultRuntime()
	for _, opt := range opts {
		opt(&runtime)
	}

	root, err := NewRootCommand(rootCtx, opts...)
	if err != nil {
		cancel()
		return nil, err
	}

	_ = runtime
	return &ApplicationRoot{Root: root, Ctx: rootCtx, cancel: cancel}, nil
}

// timeoutCancelRegistry surfaces the per-invocation deadline cancel created
// by PersistentPreRunE to Execute. Cobra runs PreRunE inside Execute, after
// New has returned, so the handoff has to go through a package-level map;
// the entry is removed as soon as Execute picks it up.
var timeoutCancelRegistry = map[*cobra.Command]context.CancelFunc{}

func registerTimeoutCancel(root *cobra.Command, cancel context.CancelFunc) {
	timeoutCancelRegistry[root] = cancel
}

func takeTimeoutCancel(root *cobra.Command) context.CancelFunc {
	cancel, ok := timeoutCancelRegistry[root]
	if ok {
		delete(timeoutCancelRegistry, root)
	}
	return cancel
} // Execute runs the command tree and returns a contract-compatible exit code.
func (a *ApplicationRoot) Execute() int {
	defer func() {
		if a.cancel != nil {
			a.cancel()
		}
	}()

	err := a.Root.ExecuteContext(a.Ctx)
	// Release the --timeout deadline timer registered by PersistentPreRunE
	// (Cobra has no derived-context lifecycle; the registry hands it back).
	if tc := takeTimeoutCancel(a.Root); tc != nil {
		tc()
	}
	if err == nil {
		return int(errors.ExitCodeSuccess)
	}

	appErr := errors.Normalize(err)
	jsonOut, _ := commandBoolFlag(a.Root, "json")
	if jsonOut {
		commandName := invokedCommandName(a.Root)
		// Passthrough failures (php/artisan) carry the child's own exit code,
		// stderr, and signal; surface them as structured diagnostics instead
		// of flattening everything into the message string.
		if diags := appErrDiagnostics(appErr); diags != nil {
			payload := output.ErrorWithDiagnostics(commandName, appErr.Code, appErr.Message, appErr.Hint, appErr.CauseAsWarnings(), diags)
			_ = output.WriteJSON(a.Root.OutOrStdout(), payload)
		} else {
			payload := output.Error(commandName, appErr.Code, appErr.Message, appErr.Hint, appErr.CauseAsWarnings())
			_ = output.WriteJSON(a.Root.OutOrStdout(), payload)
		}
	} else {
		// Cobra errors are intentionally silenced so NixCP can keep a stable
		// JSON envelope. The human-facing path must still surface the typed
		// error, otherwise users receive only an opaque exit code.
		writeHumanError(a.Root.ErrOrStderr(), appErr)
	}

	return int(appErr.ExitCode())
}

// writeHumanError is the single non-JSON error renderer. Keep diagnostics on
// stderr so normal stdout remains usable in shell pipelines just like JSON.
func writeHumanError(w interface{ Write([]byte) (int, error) }, appErr *errors.AppError) {
	if appErr == nil {
		return
	}
	fmt.Fprintf(w, "error [%s]: %s\n", appErr.Code, appErr.Message)
	if appErr.Hint != "" {
		fmt.Fprintf(w, "hint: %s\n", appErr.Hint)
	}
	detail := strings.TrimSpace(appErr.Details)
	if detail == "" && appErr.Cause != nil {
		candidate := strings.TrimSpace(appErr.Cause.Error())
		if candidate != "" && candidate != appErr.Message {
			detail = candidate
		}
	}
	if detail != "" {
		fmt.Fprintf(w, "details: %s\n", detail)
	}
}

// appErrDiagnostics converts an AppError's process-passthrough metadata into
// the structured diagnostics of the JSON error envelope. It returns nil for
// non-process errors so their envelope stays exactly as before.
func appErrDiagnostics(appErr *errors.AppError) []output.Diagnostic {
	if appErr == nil || appErr.ProcessExit == nil {
		return nil
	}
	pe := appErr.ProcessExit
	exit := pe.ExitCode
	return []output.Diagnostic{{
		Type:    "process-exit",
		Command: pe.Command,
		Exit:    &exit,
		Signal:  pe.Signal,
		Stderr:  pe.Stderr,
	}}
}

func invokedCommandName(root *cobra.Command) string {
	if root == nil {
		return "ncp"
	}
	if name := root.Annotations["invoked-command"]; name != "" {
		return name
	}
	return root.Name()
}

// commandTimeout reads timeout from command flags.
func commandTimeout(cmd *cobra.Command) (time.Duration, error) {
	return commandDurationFlag(cmd, "timeout")
}

func commandBoolFlag(cmd *cobra.Command, name string) (bool, error) {
	if cmd == nil {
		return false, nil
	}
	return cmd.Flags().GetBool(name)
}

func commandDurationFlag(cmd *cobra.Command, name string) (time.Duration, error) {
	if cmd == nil {
		return 0, nil
	}
	return cmd.Flags().GetDuration(name)
}
