package state

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/idna"
)

const (
	rebuildModeTraditional = "traditional"
	rebuildModeFlake       = "flake"

	serviceStateRunning = "running"
	serviceStateStopped = "stopped"
)

const (
	httpDefaultSystem = "x86_64-linux"
)

var (
	domainLabelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	invalidIDChars     = regexp.MustCompile(`[^a-z0-9]+`)
)

// NormalizeDomain validates and normalizes an RFC-like domain name.
func NormalizeDomain(raw string) (string, error) {
	domain := strings.TrimSpace(strings.ToLower(raw))
	if domain == "" {
		return "", fmt.Errorf("domain cannot be empty")
	}
	if strings.ContainsAny(domain, " /\t\n\r\v\f") {
		return "", fmt.Errorf("domain contains whitespace")
	}
	if strings.Contains(domain, ":") {
		return "", fmt.Errorf("domain cannot include port")
	}
	if strings.Contains(domain, "*") {
		return "", fmt.Errorf("wildcard domains are not supported")
	}
	if strings.ContainsAny(domain, "/?#") {
		return "", fmt.Errorf("domain cannot include path/query/fragment")
	}
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		return "", fmt.Errorf("domain cannot include URL scheme")
	}

	domain = strings.TrimSuffix(domain, ".")
	if domain == "" {
		return "", fmt.Errorf("domain cannot be only trailing dot")
	}

	ascii, err := idna.ToASCII(domain)
	if err != nil {
		return "", fmt.Errorf("invalid domain: %w", err)
	}
	if ascii == "" {
		return "", fmt.Errorf("invalid domain")
	}
	if strings.Contains(ascii, ":") {
		return "", fmt.Errorf("ipv6 host literals are not supported")
	}

	if len(ascii) > 253 {
		return "", fmt.Errorf("domain too long")
	}

	labels := strings.Split(ascii, ".")
	for _, label := range labels {
		if label == "" {
			return "", fmt.Errorf("domain labels cannot be empty")
		}
		if len(label) > 63 {
			return "", fmt.Errorf("domain label too long")
		}
		if !domainLabelPattern.MatchString(label) {
			return "", fmt.Errorf("invalid domain label %q", label)
		}
	}

	return ascii, nil
}

// GenerateStableSiteID creates a deterministic ID from a normalized domain.
func GenerateStableSiteID(domain string, existing map[string]struct{}) string {
	base := strings.ToLower(strings.TrimSpace(domain))
	base = invalidIDChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "site"
	}
	if existing == nil {
		existing = map[string]struct{}{}
	}
	if _, ok := existing[base]; !ok {
		return base
	}
	h := sha1.Sum([]byte(base))
	hash := hex.EncodeToString(h[:])[:8]
	candidate := base + "-" + hash
	if _, ok := existing[candidate]; !ok {
		return candidate
	}

	for i := 1; i < 1000; i++ {
		alt := fmt.Sprintf("%s-%s-%d", base, hash, i)
		if _, ok := existing[alt]; !ok {
			return alt
		}
	}
	return candidate
}

func IsTemplateHandler(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "laravel", "wordpress", "generic":
		return true
	default:
		return false
	}
}

func IsValidServiceState(state string) bool {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case serviceStateRunning, serviceStateStopped:
		return true
	default:
		return false
	}
}

func IsValidRebuildMode(mode string) bool {
	switch strings.TrimSpace(strings.ToLower(mode)) {
	case rebuildModeTraditional, rebuildModeFlake:
		return true
	default:
		return false
	}
}

// IsSupportedPHPVersion is the explicit v1 nixpkgs catalog boundary.
func IsSupportedPHPVersion(version string) bool {
	switch version {
	case "8.2", "8.3", "8.4", "8.5":
		return true
	default:
		return false
	}
}

func IsValidExtName(raw string) bool {
	name := strings.TrimSpace(strings.ToLower(raw))
	if name == "" {
		return false
	}
	if len(name) > 48 {
		return false
	}
	matched, _ := regexp.MatchString(`^[a-z0-9_]+$`, name)
	return matched
}
