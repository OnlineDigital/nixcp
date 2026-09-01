package tui

import (
	"fmt"
	"sort"
	"strings"
)

// viewStatus renders the dashboard: overall summary, services, PHP, sites.
func (m rootModel) viewStatus() string {
	var b strings.Builder

	// Header line
	b.WriteString(theme.title.Render("Overview") + "\n")
	b.WriteString(fmt.Sprintf("  owner: %s · state: %s · rebuild: %s",
		m.snap.Owner, m.snap.StateRoot, m.snap.RebuildMode))
	if m.snap.RebuildTarget != "" {
		b.WriteString(" (" + m.snap.RebuildTarget + ")")
	}
	b.WriteString("\n\n")

	// Services summary
	b.WriteString(theme.title.Render("Services") + "\n")
	for _, svc := range m.snap.Services {
		actual, hasActual := m.status[svc.Name]
		line := "  "
		if !svc.Installed {
			line += theme.subtle.Render("○ " + svc.Name + " — not installed")
		} else {
			icon, style := "●", theme.ok
			drift := ""
			if hasActual {
				if actual.Active && svc.DesiredState == "running" {
					// healthy
				} else if !actual.Active && svc.DesiredState == "stopped" {
					style = theme.subtle
				} else {
					style = theme.warn
					drift = theme.warn.Render(" ! drift")
				}
			} else if err, has := m.statusErr[svc.Name]; has {
				drift = theme.warn.Render(" ? " + err.Error())
			}
			line += style.Render(icon+" "+svc.Name) + theme.subtle.Render(fmt.Sprintf(" desired=%s", svc.DesiredState))
			if hasActual {
				state := "stopped"
				if actual.Active {
					state = "running"
				}
				line += theme.subtle.Render(fmt.Sprintf(" actual=%s", state))
			}
			line += drift
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")

	// PHP summary
	b.WriteString(theme.title.Render("PHP") + "\n")
	if len(m.snap.PHPInstalled) == 0 {
		b.WriteString("  " + theme.subtle.Render("none installed — press 3 then a") + "\n")
	} else {
		versions := append([]string(nil), m.snap.PHPInstalled...)
		sort.Strings(versions)
		badges := make([]string, 0, len(versions))
		for _, v := range versions {
			badge := v
			if v == m.snap.PHPDefault {
				badge = theme.ok.Render("★ " + v)
			}
			badges = append(badges, badge)
		}
		b.WriteString("  " + strings.Join(badges, "  ") + "\n")
		if n := len(m.snap.Extensions); n > 0 {
			b.WriteString("  " + theme.subtle.Render(fmt.Sprintf("%d extensions", n)) + "\n")
		}
	}
	b.WriteString("\n")

	// Sites summary
	b.WriteString(theme.title.Render("Sites") + "\n")
	if len(m.snap.Sites) == 0 {
		b.WriteString("  " + theme.subtle.Render("none linked — press 2 then a") + "\n")
	} else {
		for _, s := range m.snap.Sites {
			status := theme.ok.Render("●")
			if h, ok := m.health[s.ID]; ok {
				if !h.Healthy {
					if s.Enabled {
						status = theme.err.Render("●")
					} else {
						status = theme.subtle.Render("○")
					}
				}
			}
			domain := s.Domain
			if !s.Enabled {
				domain = theme.subtle.Render(s.Domain + " (disabled)")
			}
			b.WriteString(fmt.Sprintf("  %s %s %s\n", status, domain, theme.subtle.Render("php "+s.PHP)))
		}
	}

	// Overall health
	b.WriteString("\n")
	problems := 0
	if m.snapErr == nil {
		for _, svc := range m.snap.Services {
			if !svc.Installed {
				continue
			}
			if a, ok := m.status[svc.Name]; ok {
				running := svc.DesiredState == "running"
				if a.Active != running {
					problems++
				}
			}
		}
		for _, s := range m.snap.Sites {
			if h, ok := m.health[s.ID]; ok && s.Enabled && !h.Healthy {
				problems++
			}
		}
	}
	if problems == 0 {
		b.WriteString("  " + theme.ok.Render("✓ all systems nominal") + "\n")
	} else {
		b.WriteString("  " + theme.warn.Render(fmt.Sprintf("! %d issue(s) need attention", problems)) + "\n")
	}
	return b.String()
}

// viewSites renders the sites list with details for the first (v1 cursor).
func (m rootModel) viewSites() string {
	if len(m.snap.Sites) == 0 {
		return m.boxView("No sites",
			"No sites linked yet.",
			"",
			"Press "+theme.active.Render("a")+" to link your first site.",
			theme.subtle.Render("Nginx must be installed and running."))
	}
	var b strings.Builder
	for i, s := range m.snap.Sites {
		cursor := "  "
		if i == minInt(m.cursorSites, len(m.snap.Sites)-1) {
			cursor = theme.active.Render("> ")
		}
		b.WriteString(cursor + theme.title.Render(s.Domain) + theme.subtle.Render(fmt.Sprintf("  php=%s handler=%s", s.PHP, handlerLabel(s))) + "\n")
	}
	b.WriteString("\n" + theme.title.Render("Details") + "\n")
	s, _ := m.selectedSite()
	b.WriteString(fmt.Sprintf("  id:          %s\n", s.ID))
	b.WriteString(fmt.Sprintf("  enabled:     %t\n", s.Enabled))
	b.WriteString(fmt.Sprintf("  project:     %s\n", s.ProjectPath))
	b.WriteString(fmt.Sprintf("  doc root:    %s\n", s.DocumentRoot))
	if s.Database != "" {
		b.WriteString(fmt.Sprintf("  database:    %s (credentials in `ncp sites show %s`)\n", s.Database, s.Domain))
	}
	if h, ok := m.health[s.ID]; ok {
		healthLine := h.Describe
		style := theme.ok
		if !h.Healthy {
			style = theme.err
		}
		b.WriteString("  health:      " + style.Render(healthLine) + "\n")
	} else {
		b.WriteString("  health:      " + theme.subtle.Render("press p to probe") + "\n")
	}
	b.WriteString("  url:         http://" + s.Domain + "\n")
	return b.String()
}

func handlerLabel(s SiteData) string {
	if s.HandlerType == "template" && s.HandlerName != "" {
		return "template:" + s.HandlerName
	}
	if s.HandlerType == "custom" {
		return "custom"
	}
	return "generic"
}

// viewPHP renders installed versions, catalog availability, extensions.
func (m rootModel) viewPHP() string {
	var b strings.Builder
	b.WriteString(theme.title.Render("Installed versions") + "\n")
	if len(m.snap.PHPInstalled) == 0 {
		b.WriteString("  " + theme.subtle.Render("none — press a to install") + "\n")
	} else {
		versions := append([]string(nil), m.snap.PHPInstalled...)
		sort.Strings(versions)
		sel := minInt(m.cursorPHP, len(versions)-1)
		for i, v := range versions {
			cursor := "  "
			if i == sel {
				cursor = theme.active.Render("> ")
			}
			marker := ""
			if v == m.snap.PHPDefault {
				marker = " " + theme.ok.Render("★ global default")
			}
			b.WriteString(cursor + v + marker + "\n")
		}
	}

	b.WriteString("\n" + theme.title.Render("Available in catalog") + "\n")
	var avail []string
	for _, v := range phpCatalogVersions {
		if !containsString(m.snap.PHPInstalled, v) {
			avail = append(avail, v)
		}
	}
	if len(avail) == 0 {
		b.WriteString("  " + theme.subtle.Render("all catalog versions installed") + "\n")
	} else {
		b.WriteString("  " + strings.Join(avail, "  ") + "\n")
	}

	b.WriteString("\n" + theme.title.Render("Extensions") + "\n")
	if len(m.snap.Extensions) == 0 {
		b.WriteString("  " + theme.subtle.Render("none — press x to install") + "\n")
	} else {
		b.WriteString("  " + strings.Join(m.snap.Extensions, "  ") + "\n")
	}
	var availExt []string
	for _, e := range phpCatalogExtensions {
		if !containsString(m.snap.Extensions, e) {
			availExt = append(availExt, e)
		}
	}
	if len(availExt) > 0 {
		b.WriteString("\n" + theme.subtle.Render("available: "+strings.Join(availExt, " ")) + "\n")
	}
	return b.String()
}

// viewServices renders the three allowlisted services with actual state.
func (m rootModel) viewServices() string {
	var b strings.Builder
	names := serviceNames(m)
	for i, name := range names {
		var cfg ServiceConfigData
		found := false
		for _, s := range m.snap.Services {
			if s.Name == name {
				cfg = s
				found = true
				break
			}
		}
		if !found {
			cfg = ServiceConfigData{Name: name}
		}
		cursor := "  "
		if i == minInt(m.cursorServices, len(names)-1) {
			cursor = theme.active.Render("> ")
		}
		b.WriteString(cursor + theme.title.Render(name))
		if !cfg.Installed {
			b.WriteString(theme.subtle.Render("  not installed"))
		} else {
			state := "stopped"
			if a, ok := m.status[name]; ok && a.Active {
				state = "running"
			}
			drift := ""
			running := cfg.DesiredState == "running"
			if a, ok := m.status[name]; ok && (a.Active != running) {
				drift = theme.warn.Render(" ! drift")
			}
			b.WriteString(theme.subtle.Render(fmt.Sprintf("  desired=%s actual=%s", cfg.DesiredState, state)))
			b.WriteString(drift)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n" + theme.subtle.Render("unit health via systemctl; actions run locked NixOS transactions"))
	return b.String()
}

// viewActivity renders the session log, newest last.
func (m rootModel) viewActivity() string {
	if len(m.log) == 0 {
		return m.boxView("Activity", "No actions in this session yet.", "", theme.subtle.Render("Mutations, errors and cancels appear here."))
	}
	var b strings.Builder
	for _, e := range m.log {
		style := theme.ok
		switch e.status {
		case "failed":
			style = theme.err
		case "cancelled", "info":
			style = theme.info
		case "noop":
			style = theme.subtle
		case "changed":
			style = theme.ok
		}
		b.WriteString(fmt.Sprintf("%s  %-8s  %s\n", theme.subtle.Render(e.time), style.Render(e.status), e.label))
		if e.detail != "" && e.detail != e.label {
			b.WriteString("            " + theme.subtle.Render(wrapDetail(e.detail, 80)) + "\n")
		}
	}
	return b.String()
}

func wrapDetail(s string, width int) string {
	if len(s) <= width {
		return s
	}
	var out []string
	for len(s) > width {
		cut := s[:width]
		if i := strings.LastIndex(cut, " "); i > 0 {
			cut = cut[:i]
		}
		out = append(out, cut)
		s = strings.TrimSpace(s[len(cut):])
	}
	out = append(out, s)
	return strings.Join(out, "\n            ")
}
