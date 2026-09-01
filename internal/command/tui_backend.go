package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/user"

	"github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/output"
	"github.com/nixcp/nixcp/internal/service"
	"github.com/nixcp/nixcp/internal/state"
	"github.com/nixcp/nixcp/internal/tui"
	"github.com/spf13/cobra"
)

// commandBackend implements tui.Backend by executing the CLI's own
// use-cases in-process. The TUI adds no second code path: reads go through
// the same state store as the CLI commands, and every mutation runs a fresh
// root command (cobra roots are single-use) with --json --no-input so the
// exact validate → render → transaction pipeline executes — precondition
// checks, root-safety, timeout binding, and rollback included.
type commandBackend struct {
	runtime Runtime
	// opts rebuild the runtime for each in-process CLI execution. They are
	// the same options used to construct the TUI's host process, so state
	// home overrides and test runners propagate identically.
	opts []RuntimeOption
}

// NewTUIBackend returns the TUI adapter over the shared runtime.
func NewTUIBackend(rt Runtime, opts ...RuntimeOption) tui.Backend {
	return &commandBackend{runtime: rt, opts: opts}
}

// runCLI executes argv as the CLI would run it, capturing stdout. The
// appended flags force machine output and disable huh prompts so the TUI
// never blocks on hidden interactivity.
func (b *commandBackend) runCLI(ctx context.Context, argv ...string) (string, int) {
	root, err := NewRootCommand(ctx, b.opts...)
	if err != nil {
		return "", 1
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	full := append([]string{"--json", "--no-input", "--timeout=1800s"}, argv...)
	root.SetArgs(full)
	code := executeRoot(root)
	return stdout.String(), code
}

// executeRoot runs the root command with the error envelope already written
// by ApplicationRoot.Execute's JSON path; here we replicate that behavior
// for a raw root (commandBackend owns its own roots, not an
// ApplicationRoot, so the shared helper cannot be reused directly).
func executeRoot(root *cobra.Command) int {
	err := root.ExecuteContext(root.Context())
	if tc := takeTimeoutCancel(root); tc != nil {
		tc()
	}
	if err == nil {
		return int(errors.ExitCodeSuccess)
	}
	appErr := errors.Normalize(err)
	payload := output.Error(invokedCommandName(root), appErr.Code, appErr.Message, appErr.Hint, appErr.CauseAsWarnings())
	_ = output.WriteJSON(root.OutOrStdout(), payload)
	return int(appErr.ExitCode())
}

// ---------------------------------------------------------------------------
// envelope parsing
// ---------------------------------------------------------------------------

type successEnvelope struct {
	Ok       bool            `json:"ok"`
	Command  string          `json:"command"`
	Changed  bool            `json:"changed"`
	Data     json.RawMessage `json:"data"`
	Warnings json.RawMessage `json:"warnings"`
}

type errorEnvelopeInfo struct {
	Ok    bool `json:"ok"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	} `json:"error"`
}

// envelopeError turns a failed CLI execution into an AppError the TUI logs.
func envelopeError(stdout string, code int) error {
	var env errorEnvelopeInfo
	if err := json.Unmarshal([]byte(stdout), &env); err == nil && !env.Ok {
		return errors.New(env.Error.Code, env.Error.Message, env.Error.Hint, errors.ExitCode(code))
	}
	return fmt.Errorf("command failed with exit code %d", code)
}

// phaseOf extracts the transaction phase from a success payload.
func phaseOf(data json.RawMessage) string {
	var d struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(data, &d); err == nil {
		return d.Phase
	}
	return ""
}

// warningsOf extracts warning strings from the warnings field.
func warningsOf(raw json.RawMessage) []string {
	var structured []struct {
		Code   string `json:"code"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &structured); err == nil && len(structured) > 0 {
		out := make([]string, 0, len(structured))
		for _, w := range structured {
			if w.Reason != "" {
				out = append(out, w.Reason)
			} else {
				out = append(out, w.Code)
			}
		}
		return out
	}
	var plain []string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain
	}
	return nil
}

// parseLinkData extracts the link-specific result fields.
func parseLinkData(env successEnvelope) (tui.LinkResult, error) {
	var d struct {
		ID      string `json:"id"`
		Domain  string `json:"domain"`
		Phase   string `json:"phase"`
		MariaDB *struct {
			Database string `json:"database"`
			User     string `json:"user"`
			Password string `json:"password"`
		} `json:"mariadb"`
	}
	if err := json.Unmarshal(env.Data, &d); err != nil {
		return tui.LinkResult{}, fmt.Errorf("unexpected link output")
	}
	res := tui.LinkResult{ID: d.ID, Domain: d.Domain, Phase: d.Phase}
	if d.MariaDB != nil {
		res.Database = d.MariaDB.Database
		res.DBUser = d.MariaDB.User
		res.DBPassword = d.MariaDB.Password
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// reads (direct, same stores as CLI)
// ---------------------------------------------------------------------------

// Snapshot loads the desired state read-only. Not-configured is not an error:
// the TUI renders its "not initialized" screen for that.
func (b *commandBackend) Snapshot(ctx context.Context) (tui.SnapshotData, error) {
	u, err := user.Current()
	if err != nil {
		return tui.SnapshotData{}, err
	}
	store := state.NewStore(b.runtime.StateHomeOrDefault(u.HomeDir))
	snap, err := store.Load()
	if err != nil {
		return tui.SnapshotData{}, nil
	}
	data := tui.SnapshotData{
		Configured:    true,
		Schema:        snap.Config.SchemaVersion,
		Owner:         snap.Config.Owner.Username,
		StateRoot:     store.Root,
		RebuildMode:   snap.Config.Rebuild.Mode,
		RebuildTarget: snap.Config.Rebuild.Target,
		PHPInstalled:  append([]string(nil), snap.Config.PHP.Installed...),
		PHPDefault:    snap.Config.PHP.GlobalDefault,
		Extensions:    append([]string(nil), snap.Config.PHP.Extensions...),
	}
	for _, name := range []service.Name{service.Nginx, service.MariaDB, service.Valkey} {
		cfg := desiredServiceConfig(snap.Config, name)
		data.Services = append(data.Services, tui.ServiceConfigData{
			Name:         string(name),
			Installed:    cfg.Installed,
			DesiredState: cfg.DesiredState,
		})
	}
	for _, s := range snap.Sites {
		db := ""
		if s.MariaDB != nil {
			db = s.MariaDB.Database
		}
		data.Sites = append(data.Sites, tui.SiteData{
			ID:           s.ID,
			Domain:       s.Domain,
			Enabled:      s.Enabled,
			PHP:          s.PHP,
			ProjectPath:  s.ProjectPath,
			DocumentRoot: s.DocumentRoot,
			HandlerType:  s.Nginx.Handler.Type,
			HandlerName:  s.Nginx.Handler.Name,
			Database:     db,
		})
	}
	return data, nil
}

// ServiceStatus returns actual systemd state for one allowlisted service.
func (b *commandBackend) ServiceStatus(ctx context.Context, name string) (tui.ServiceActual, error) {
	if b.runtime.Services == nil {
		return tui.ServiceActual{}, errors.New("systemd_unavailable", "systemd adapter is not configured", "Retry on a supported NixOS host", errors.ExitCodeRuntime)
	}
	actual, err := b.runtime.Services.Status(ctx, service.Name(name))
	if err != nil {
		return tui.ServiceActual{}, err
	}
	return tui.ServiceActual{Active: actual.Active, Enabled: actual.Enabled, Health: actual.Health}, nil
}

// SiteHealth probes a linked site's FPM socket + local HTTP reachability.
func (b *commandBackend) SiteHealth(ctx context.Context, domain, siteID string, enabled bool) (tui.SiteHealthData, error) {
	checker := b.runtime.SiteChecker
	if checker == nil {
		return tui.SiteHealthData{}, errors.New("site_checker_unavailable", "site health checker is not configured", "", errors.ExitCodeRuntime)
	}
	status := checker.CheckSite(ctx, domain, siteID, enabled)
	return tui.SiteHealthData{
		SocketOK:   status.SocketOK,
		HTTPOK:     status.HTTPOK,
		HTTPStatus: status.HTTPStatus,
		Healthy:    status.ProblemCode == "",
		Describe:   status.Describe(),
	}, nil
}

// ---------------------------------------------------------------------------
// mutations (all through the CLI pipeline in-process)
// ---------------------------------------------------------------------------

func (b *commandBackend) PHPInstall(ctx context.Context, version string) (tui.ActionResult, error) {
	return b.action(ctx, "php", "install", version)
}

func (b *commandBackend) PHPUninstall(ctx context.Context, version string) (tui.ActionResult, error) {
	return b.action(ctx, "php", "uninstall", version)
}

func (b *commandBackend) PHPUseGlobal(ctx context.Context, version string) (tui.ActionResult, error) {
	return b.action(ctx, "php", "use", "--global", version)
}

func (b *commandBackend) PHPExtInstall(ctx context.Context, ext string) (tui.ActionResult, error) {
	return b.action(ctx, "php", "ext", "install", ext)
}

func (b *commandBackend) ServiceAction(ctx context.Context, service, action string) (tui.ActionResult, error) {
	return b.action(ctx, "service", service, action)
}

func (b *commandBackend) UnlinkSite(ctx context.Context, domain string) (tui.ActionResult, error) {
	return b.action(ctx, "unlink", domain)
}

// LinkSite creates a site from the form's validated payload. The argv
// mirrors the documented CLI flags exactly; the form overlay validates the
// shape client-side, the CLI re-validates everything server-side.
func (b *commandBackend) LinkSite(ctx context.Context, req tui.LinkRequest) (tui.LinkResult, error) {
	argv := []string{"link", req.Domain}
	switch req.Template {
	case "laravel", "wordpress":
		argv = append(argv, "--template", req.Template)
	case "custom":
		argv = append(argv, "--config", req.CustomPath)
	}
	if req.MariaDB != "" {
		argv = append(argv, "--mariadb", req.MariaDB)
	}
	if req.Path != "" {
		argv = append(argv, "--path", req.Path)
	}
	if req.Root != "" {
		argv = append(argv, "--root", req.Root)
	}
	if req.PHP != "" {
		argv = append(argv, "--php", req.PHP)
	}
	stdout, code := b.runCLI(ctx, argv...)
	env, err := parseSuccess(stdout)
	if err != nil || code != 0 {
		return tui.LinkResult{}, envelopeError(stdout, code)
	}
	return parseLinkData(env)
}

// action is the generic mutation path.
func (b *commandBackend) action(ctx context.Context, argv ...string) (tui.ActionResult, error) {
	stdout, code := b.runCLI(ctx, argv...)
	env, err := parseSuccess(stdout)
	if err != nil || code != 0 {
		return tui.ActionResult{}, envelopeError(stdout, code)
	}
	return tui.ActionResult{
		Changed:  env.Changed,
		Phase:    phaseOf(env.Data),
		Warnings: warningsOf(env.Warnings),
	}, nil
}

// parseSuccess decodes a success envelope from captured stdout.
func parseSuccess(stdout string) (successEnvelope, error) {
	var env successEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		return successEnvelope{}, err
	}
	return env, nil
}
