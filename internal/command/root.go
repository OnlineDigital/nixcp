package command

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/nix"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/platform"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/transaction"
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
	Runner       execx.Runner
	Metadata     BuildMetadata
	Platform     platform.Inspector
	Renderer     nix.Renderer
	Services     service.Systemd
	Transactions *transaction.Manager
	StateHome    string // test-only override; production uses the current user's home.
}

// WithStateHome injects an isolated state home for tests.
func WithStateHome(home string) RuntimeOption { return func(rt *Runtime) { rt.StateHome = home } }

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
	return Runtime{
		Runner: &execx.RealRunner{},
		Metadata: BuildMetadata{
			Version: defaultVersion,
		},
		Platform: platform.HostInspector{},
		Renderer: nix.Renderer{},
		Services: service.Adapter{Runner: &execx.RealRunner{}},
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
		Short:         "NixCP CLI",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if commandJSON(cmd) {
				payload := output.Success("ncp", false, map[string]any{"version": runtime.Metadata.Version}, nil)
				return emitJSON(cmd, payload)
			}
			cmd.Printf("NixCP CLI %s\n", runtime.Metadata.Version)
			return nil
		},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Root().Annotations["invoked-command"] = cmd.CommandPath()
			jsonOut, err := commandBoolFlag(cmd, "json")
			if err != nil {
				return errors.New("invalid_flag_state", err.Error(), "", errors.ExitCodePrecond)
			}
			if jsonOut {
				_ = cmd.Flags().Set("no-input", "true")
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
	root.AddCommand(newStatusCommand())
	root.AddCommand(newDoctorCommand())
	root.AddCommand(newServiceCommand(runtime))
	root.AddCommand(newServiceAliasCommand(runtime, service.Nginx))
	root.AddCommand(newServiceAliasCommand(runtime, service.MariaDB))
	root.AddCommand(newServiceAliasCommand(runtime, service.Redis))
	root.AddCommand(newPHPCommand(runtime))
	root.AddCommand(newArtisanCommand(runtime))
	root.AddCommand(newLinkCommand())
	root.AddCommand(newUnlinkCommand())
	root.AddCommand(newSitesCommand())
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

// Execute runs the command tree and returns a contract-compatible exit code.
func (a *ApplicationRoot) Execute() int {
	defer func() {
		if a.cancel != nil {
			a.cancel()
		}
	}()

	err := a.Root.ExecuteContext(a.Ctx)
	if err == nil {
		return int(errors.ExitCodeSuccess)
	}

	appErr := errors.Normalize(err)
	jsonOut, _ := commandBoolFlag(a.Root, "json")
	if jsonOut {
		commandName := invokedCommandName(a.Root)
		payload := output.Error(commandName, appErr.Code, appErr.Message, appErr.Hint, appErr.CauseAsWarnings())
		_ = output.WriteJSON(a.Root.OutOrStdout(), payload)
	}

	return int(appErr.ExitCode())
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
