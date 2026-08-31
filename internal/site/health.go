package site

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/state"
)

// HealthTimeout bounds each individual site probe.
const HealthTimeout = 10 * time.Second

// HealthStatus reports desired vs actual state for one linked site.
type HealthStatus struct {
	Domain      string
	SiteID      string
	SocketPath  string
	SocketOK    bool
	HTTPOK      bool
	HTTPStatus  int
	DesiredOn   bool
	ProblemCode string
}

// Checker validates that a linked site is actually reachable as desired.
// Implementations must be non-destructive.
type Checker interface {
	CheckSite(ctx context.Context, domain, siteID string, desiredEnabled bool) HealthStatus
}

// ConfigVerifier validates the Nginx configuration selected by the active
// system profile. It is separate from Checker so transaction tests can inject
// deterministic probes without requiring a host Nginx installation.
type ConfigVerifier interface {
	Verify(context.Context) error
}

// NginxConfigVerifier invokes nginx directly with fixed argv. nginx -t reads
// the active default configuration and does not reload or otherwise mutate it.
type NginxConfigVerifier struct{ Runner execx.Runner }

func (v NginxConfigVerifier) Verify(ctx context.Context) error {
	if v.Runner == nil {
		return fmt.Errorf("nginx configuration verifier is not configured")
	}
	result, err := v.Runner.Run(ctx, &execx.Command{Name: "nginx", Args: []string{"-t"}, StdoutMax: execx.DefaultStdoutLimit, StderrMax: execx.DefaultStderrLimit})
	if err != nil || result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if detail != "" {
			return fmt.Errorf("nginx active configuration check failed: %s", detail)
		}
		if err != nil {
			return fmt.Errorf("nginx active configuration check failed: %w", err)
		}
		return fmt.Errorf("nginx active configuration check failed")
	}
	return nil
}

// TransactionHealth is the production Health.Check adapter for linked sites.
// It only probes resources marked as site:<stable-id>, so unrelated
// transactions do not require a live Nginx or PHP-FPM installation.
// nginx-config asks it to validate the active Nginx configuration without an
// HTTP probe (used by unlink transactions).
type TransactionHealth struct {
	Config  ConfigVerifier
	Checker Checker
	Sites   map[string]state.SiteConfig
}

func (h TransactionHealth) Check(ctx context.Context, affected []string) error {
	siteIDs := make([]string, 0)
	checkConfig := false
	for _, resource := range affected {
		if resource == "nginx-config" {
			checkConfig = true
			continue
		}
		if id, ok := strings.CutPrefix(resource, "site:"); ok {
			if id == "" {
				return fmt.Errorf("site health received an empty site resource")
			}
			siteIDs = append(siteIDs, id)
			checkConfig = true
		}
	}
	if !checkConfig {
		return nil
	}
	if h.Config == nil || h.Checker == nil {
		return fmt.Errorf("site transaction health is not configured")
	}
	if err := h.Config.Verify(ctx); err != nil {
		return err
	}
	for _, id := range siteIDs {
		s, ok := h.Sites[id]
		if !ok {
			return fmt.Errorf("site health received unknown site %q", id)
		}
		status := h.Checker.CheckSite(ctx, s.Domain, s.ID, s.Enabled)
		if !status.DesiredOn || !status.SocketOK || !status.HTTPOK || status.ProblemCode != "" {
			return fmt.Errorf("site health failed: %s", status.Describe())
		}
	}
	return nil
}

// RealChecker probes the local runtime without sudo: the PHP-FPM socket and
// an HTTP request against Nginx with the site's Host header.
type RealChecker struct {
	// HTTPDo allows tests to inject transport; nil uses http.DefaultClient
	// with a bounded timeout.
	HTTPDo func(ctx context.Context, req *http.Request) (*http.Response, error)
}

// SocketPath returns the per-site PHP-FPM socket path managed by the module.
func SocketPath(siteID string) string { return "/run/nixcp/php-fpm/" + siteID + ".sock" }

// CheckSite probes socket + HTTP for one site. It never mutates state.
func (c RealChecker) CheckSite(ctx context.Context, domain, siteID string, desiredEnabled bool) HealthStatus {
	status := HealthStatus{Domain: domain, SiteID: siteID, SocketPath: SocketPath(siteID), DesiredOn: desiredEnabled}

	ctx, cancel := context.WithTimeout(ctx, HealthTimeout)
	defer cancel()

	info, err := os.Stat(status.SocketPath)
	status.SocketOK = err == nil && info.Mode()&os.ModeSocket != 0

	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1/", nil)
	if reqErr == nil {
		req.Host = domain
		do := c.HTTPDo
		if do == nil {
			do = func(ctx context.Context, r *http.Request) (*http.Response, error) {
				client := &http.Client{Transport: &http.Transport{
					DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				}, Timeout: HealthTimeout}
				return client.Do(r)
			}
		}
		if resp, doErr := do(ctx, req); doErr == nil {
			status.HTTPStatus = resp.StatusCode
			_ = resp.Body.Close()
			status.HTTPOK = resp.StatusCode > 0 && resp.StatusCode < 500
		}
	}

	if desiredEnabled && !status.HTTPOK {
		status.ProblemCode = "site_not_reachable"
	}
	if desiredEnabled && !status.SocketOK {
		status.ProblemCode = "php_fpm_socket_missing"
	}
	return status
}

// Describe renders a one-line human diagnostic.
func (h HealthStatus) Describe() string {
	// A status without an explicit problem code is only healthy when the
	// probes actually passed; disabled sites (or any status where both
	// probes failed without a code set) must not be reported as healthy.
	if h.ProblemCode == "" {
		if h.DesiredOn && h.SocketOK && h.HTTPOK {
			return fmt.Sprintf("%s: healthy (socket ok, HTTP %d)", h.Domain, h.HTTPStatus)
		}
		if !h.DesiredOn {
			if h.SocketOK || h.HTTPOK {
				return fmt.Sprintf("%s: disabled but still serving traffic (socket ok: %t, HTTP %d)", h.Domain, h.SocketOK, h.HTTPStatus)
			}
			return fmt.Sprintf("%s: disabled (no traffic expected)", h.Domain)
		}
		return fmt.Sprintf("%s: degraded (socket ok: %t, HTTP %d)", h.Domain, h.SocketOK, h.HTTPStatus)
	}
	switch h.ProblemCode {
	case "php_fpm_socket_missing":
		return fmt.Sprintf("%s: PHP-FPM socket missing at %s", h.Domain, h.SocketPath)
	case "site_not_reachable":
		return fmt.Sprintf("%s: HTTP request failed locally (status %d)", h.Domain, h.HTTPStatus)
	}
	return fmt.Sprintf("%s: %s", h.Domain, strings.TrimSpace(h.ProblemCode))
}
