package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// overlay types
// ---------------------------------------------------------------------------

type actionKind int

const (
	actionPHPInstall actionKind = iota
	actionPHPUninstall
	actionPHPUseGlobal
	actionPHPExt
	actionService
	actionUnlink
	actionLink
)

// pendingAction describes a mutation waiting for confirmation/selection.
type pendingAction struct {
	kind      actionKind
	action    string // service sub-action: install/start/stop/restart
	arg       string // target: version, service name, site domain
	argPrefix bool   // label gets " <arg>" appended when the select fills arg
	label     string
	req       LinkRequest
}

// overlayFinished is the result any overlay produces when done.
type overlayFinished interface{ finished() }

type confirmFinished struct {
	confirmed bool
	pending   pendingAction
}

func (confirmFinished) finished() {}

type selectFinished struct {
	value   string
	pending pendingAction
}

func (selectFinished) finished() {}

type formFinished struct {
	cancelled bool
	req       LinkRequest
	pending   pendingAction
}

func (formFinished) finished() {}

// ---------------------------------------------------------------------------
// overlay model: confirm, select, and link-form modal dialogs
// ---------------------------------------------------------------------------

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayConfirm
	overlaySelect
	overlayForm
)

type overlayModel struct {
	kind   overlayKind
	title  string
	detail string
	// confirm
	choice bool
	// select
	options []string
	cursor  int
	// form (link)
	fields []formField
	focus  int
	// done flags: exactly one is set when the overlay has produced its
	// result; navigation keys never finish an overlay by accident.
	done      bool
	confirmed bool
	cancelled bool
	// payload
	pending pendingAction
}

type formField struct {
	label       string
	placeholder string
	value       string
	input       textInput
	err         string
}

// textInput is a tiny embedded single-line editor (avoids importing the
// full bubbles textinput state machine into the overlay; keeps the TUI
// dependency surface minimal and testable).
type textInput struct {
	value string
}

func (t *textInput) insert(s string) { t.value += s }
func (t *textInput) backspace() {
	if len(t.value) > 0 {
		t.value = t.value[:len(t.value)-1]
	}
}

func newOverlayModel() overlayModel {
	return overlayModel{}
}

func (o overlayModel) Init() tea.Cmd { return nil }

func (o overlayModel) Active() bool { return o.kind != overlayNone }

// Finished returns the overlay result once done, nil while still open.
func (o overlayModel) Finished() overlayFinished {
	if !o.done {
		return nil
	}
	switch o.kind {
	case overlayConfirm:
		return confirmFinished{confirmed: o.confirmed, pending: o.pending}
	case overlaySelect:
		if o.cancelled || o.cursor < 0 || o.cursor >= len(o.options) {
			return selectFinished{value: "", pending: o.pending}
		}
		return selectFinished{value: o.options[o.cursor], pending: o.pending}
	case overlayForm:
		return formFinished{cancelled: o.cancelled, req: o.req(), pending: o.pending}
	}
	return nil
}

func (o *overlayModel) openConfirm(title, detail string, defaultYes bool) {
	o.kind = overlayConfirm
	o.title = title
	o.detail = detail
	o.choice = defaultYes
}

func (o *overlayModel) openSelect(title string, options []string, pa pendingAction) {
	o.kind = overlaySelect
	o.title = title
	o.options = options
	o.cursor = 0
	o.pending = pa
}

func (o *overlayModel) openForm(spec formSpec) {
	o.kind = overlayForm
	o.title = spec.title
	o.fields = make([]formField, len(spec.fields))
	for i, f := range spec.fields {
		o.fields[i] = formField{label: f.label, placeholder: f.placeholder, value: f.defaultValue}
	}
	o.focus = 0
	o.pending = spec.pending
}

// req builds the LinkRequest from the current field values.
func (o overlayModel) req() LinkRequest {
	get := func(label string) string {
		for _, f := range o.fields {
			if f.label == label {
				return strings.TrimSpace(f.value)
			}
		}
		return ""
	}
	return LinkRequest{
		Domain:     get("Domain"),
		PHP:        get("PHP version"),
		Template:   get("Handler"),
		CustomPath: get("Custom snippet path"),
		MariaDB:    get("MariaDB database (optional)"),
		Path:       get("Project path"),
		Root:       get("Document root"),
	}
}

func (o overlayModel) Update(msg tea.Msg) (overlayModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return o, nil
	}
	switch o.kind {
	case overlayConfirm:
		switch key.String() {
		case "y", "Y", "enter":
			o.confirmed = true
			o.done = true
		case "n", "N", "esc":
			o.done = true
		case "tab", "left", "right", "h", "l":
			o.choice = !o.choice
		}
	case overlaySelect:
		switch key.String() {
		case "up", "k":
			if o.cursor > 0 {
				o.cursor--
			}
		case "down", "j":
			if o.cursor < len(o.options)-1 {
				o.cursor++
			}
		case "enter":
			o.done = true
		case "esc", "q":
			o.cancelled = true
			o.done = true
		}
	case overlayForm:
		switch key.String() {
		case "esc":
			o.cancelled = true
			o.done = true
		case "tab", "shift+tab", "down":
			if len(o.fields) > 0 {
				o.focus = (o.focus + 1) % len(o.fields)
			}
		case "up":
			if len(o.fields) > 0 {
				o.focus = (o.focus - 1 + len(o.fields)) % len(o.fields)
			}
		case "ctrl+u":
			if o.focus < len(o.fields) {
				o.fields[o.focus].value = ""
				o.fields[o.focus].input = textInput{}
			}
		case "enter":
			if o.focus == len(o.fields)-1 {
				o.done = true
			} else {
				o.focus++
			}
		case "backspace":
			if o.focus < len(o.fields) {
				o.fields[o.focus].input.backspace()
				o.fields[o.focus].value = o.fields[o.focus].input.value
			}
		default:
			if len(key.Runes) > 0 && o.focus < len(o.fields) {
				o.fields[o.focus].input.insert(string(key.Runes))
				o.fields[o.focus].value = o.fields[o.focus].input.value
			}
		}
	}
	return o, nil
}

func (o overlayModel) View() string {
	if o.kind == overlayNone {
		return ""
	}
	var b strings.Builder
	switch o.kind {
	case overlayConfirm:
		b.WriteString(theme.title.Render(o.title) + "\n\n")
		if o.detail != "" {
			b.WriteString(indent(o.detail) + "\n\n")
		}
		yes := theme.ok.Render("y yes")
		no := theme.err.Render("n no")
		if o.choice {
			yes = theme.active.Render("[y] yes")
		} else {
			no = theme.active.Render("[n] no")
		}
		b.WriteString("  " + yes + "   " + no + "\n")
	case overlaySelect:
		b.WriteString(theme.title.Render(o.title) + "\n\n")
		for i, opt := range o.options {
			cursor := "  "
			style := theme.inactive
			if i == o.cursor {
				cursor = theme.active.Render("> ")
				style = theme.active
			}
			b.WriteString(cursor + style.Render(opt) + "\n")
		}
		b.WriteString("\n" + theme.subtle.Render("enter select · esc cancel"))
	case overlayForm:
		b.WriteString(theme.title.Render(o.title) + "\n\n")
		for i, f := range o.fields {
			style := theme.inactive
			if i == o.focus {
				style = theme.active
			}
			value := f.value
			if value == "" {
				value = theme.subtle.Render(f.placeholder)
			}
			b.WriteString("  " + style.Render(f.label+":") + " " + value + "\n")
		}
		b.WriteString("\n" + theme.subtle.Render("tab next · enter submit on last · esc cancel"))
	}
	return theme.box.Render(b.String())
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// link form spec
// ---------------------------------------------------------------------------

type formSpec struct {
	title   string
	fields  []formFieldSpec
	pending pendingAction
}

type formFieldSpec struct {
	label        string
	placeholder  string
	defaultValue string
}

func (m rootModel) linkFormSpec() formSpec {
	phpDefault := m.snap.PHPDefault
	if phpDefault == "" {
		phpDefault = "8.4"
	}
	return formSpec{
		title: "Link new site",
		fields: []formFieldSpec{
			{label: "Domain", placeholder: "app.example.test"},
			{label: "PHP version", placeholder: phpDefault, defaultValue: phpDefault},
			{label: "Handler", placeholder: "generic | laravel | wordpress | custom", defaultValue: "generic"},
			{label: "Custom snippet path", placeholder: "/abs/path.conf (custom only)"},
			{label: "MariaDB database (optional)", placeholder: "app"},
			{label: "Project path", placeholder: "current directory"},
			{label: "Document root", placeholder: "public (laravel/wordpress) or empty"},
		},
		pending: pendingAction{kind: actionLink},
	}
}

// phpCatalogVersions/extensions mirror the php package catalog for the
// install overlays; the backend validates for real.
var phpCatalogVersions = []string{"8.2", "8.3", "8.4", "8.5"}

var phpCatalogExtensions = []string{
	"apcu", "bcmath", "curl", "gd", "imagick", "intl", "mbstring",
	"mysqli", "pdo_mysql", "pdo_pgsql", "pdo_sqlite", "redis",
	"soap", "sockets", "xdebug", "xml", "zip", "opcache",
}
