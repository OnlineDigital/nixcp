package command

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	apperrors "github.com/nixcp/nixcp/internal/errors"
	"github.com/nixcp/nixcp/internal/execx"
	"github.com/nixcp/nixcp/internal/ui"
	"github.com/spf13/cobra"
)

var viteConfigNames = []string{"vite.config.js", "vite.config.ts", "vite.config.mjs", "vite.config.mts"}

type viteObject struct{ open, close int }
type viteProperty struct {
	name                 string
	valueStart, valueEnd int
	object               *viteObject
}
type viteEdit struct {
	start, end int
	text       string
}

// prepareViteConfigUpdate owns the optional, interactive change only. Scripts
// and --no-input/--json keep the established, non-mutating behaviour.
func prepareViteConfigUpdate(cmd *cobra.Command, runtime Runtime, project string) error {
	if !commandUIMode(cmd).Interactive() {
		return nil
	}
	path, err := findViteConfig(project)
	if err != nil || path == "" {
		return err
	}
	original, err := os.ReadFile(path)
	if err != nil {
		return apperrors.New("vite_config_read_failed", err.Error(), "Check that the Vite config is readable", apperrors.ExitCodeRuntime)
	}
	updated, err := patchViteConfig(original)
	if err != nil {
		return apperrors.New("vite_config_unsupported", err.Error(), "Update the Vite server configuration manually, then run ncp enable vite again", apperrors.ExitCodePrecond)
	}
	if bytes.Equal(original, updated) {
		return nil
	}
	ok, err := ui.Confirm("Update " + filepath.Base(path) + " with NixCP's Vite server and HMR settings?")
	if err != nil || !ok {
		if err != nil {
			return apperrors.New("aborted_by_user", "aborted by user", "", apperrors.ExitCodeUsage)
		}
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return apperrors.New("vite_config_stat_failed", err.Error(), "Check the Vite config", apperrors.ExitCodeRuntime)
	}
	if err := writeViteConfig(path, updated, info.Mode()); err != nil {
		return apperrors.New("vite_config_write_failed", err.Error(), "Check that the Vite config is writable", apperrors.ExitCodeRuntime)
	}
	if err := showViteConfigDiff(cmd, runtime, path, original); err != nil {
		_ = writeViteConfig(path, original, info.Mode())
		return err
	}
	editorLabel := "Manual change with $EDITOR"
	if editor := viteEditorName(); editor != "" {
		editorLabel = "Manual change with " + editor
	}
	choice, err := ui.Select("Review Vite configuration", "Does the displayed modification look correct?", []ui.SelectOption{
		{Value: "accept", Label: "Yes, go ahead", Desc: "keep the changes and enable Vite"},
		{Value: "cancel", Label: "No, cancel everything", Desc: "restore the original config and stop"},
		{Value: "manual", Label: editorLabel, Desc: "edit the config, then continue"},
	})
	if err != nil || choice == "cancel" {
		_ = writeViteConfig(path, original, info.Mode())
		return apperrors.New("aborted_by_user", "aborted by user", "", apperrors.ExitCodeUsage)
	}
	if choice == "manual" {
		return editViteConfig(cmd, runtime, project, path)
	}
	return nil
}

func findViteConfig(project string) (string, error) {
	var matches []string
	for _, name := range viteConfigNames {
		path := filepath.Join(project, name)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", apperrors.New("vite_config_discovery_failed", err.Error(), "Check the project directory", apperrors.ExitCodeRuntime)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", apperrors.New("vite_config_invalid", name+" must be a regular file", "Use a regular root Vite config file", apperrors.ExitCodePrecond)
		}
		matches = append(matches, path)
	}
	if len(matches) > 1 {
		return "", apperrors.New("vite_config_ambiguous", "more than one root vite.config file exists", "Keep one of vite.config.js, .ts, .mjs, or .mts", apperrors.ExitCodePrecond)
	}
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0], nil
}

func showViteConfigDiff(cmd *cobra.Command, runtime Runtime, path string, original []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nixcp-vite-original-*")
	if err != nil {
		return apperrors.New("vite_config_diff_failed", err.Error(), "Check that the project directory is writable", apperrors.ExitCodeRuntime)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(original); err != nil {
		_ = tmp.Close()
		return apperrors.New("vite_config_diff_failed", err.Error(), "", apperrors.ExitCodeRuntime)
	}
	if err := tmp.Close(); err != nil {
		return apperrors.New("vite_config_diff_failed", err.Error(), "", apperrors.ExitCodeRuntime)
	}
	// --label is not supported by every Git build (notably older Nix Git
	// packages). --no-index works outside a repository and still gives the
	// user a normal, color-free review diff without relying on that option.
	res, err := runtime.Runner.Run(cmd.Context(), &execx.Command{Name: "git", Args: []string{"--no-pager", "diff", "--no-index", "--no-color", "--no-ext-diff", tmpName, path}})
	if err != nil && res.ExitCode != 1 {
		return apperrors.New("vite_config_diff_failed", strings.TrimSpace(res.Stderr), "Ensure git is installed", apperrors.ExitCodeRuntime)
	}
	fmt.Fprint(cmd.OutOrStdout(), res.Stdout)
	return nil
}
func viteEditorName() string {
	editor := strings.Fields(os.Getenv("EDITOR"))
	if len(editor) == 0 {
		return ""
	}
	return filepath.Base(editor[0])
}

func editViteConfig(cmd *cobra.Command, runtime Runtime, project, path string) error {
	editor := strings.Fields(os.Getenv("EDITOR"))
	if len(editor) == 0 {
		return apperrors.New("vite_editor_unset", "$EDITOR is not set", "Set EDITOR, then run ncp enable vite again", apperrors.ExitCodePrecond)
	}
	res, err := runtime.Runner.Run(cmd.Context(), &execx.Command{Name: editor[0], Args: append(editor[1:], path), Dir: project, Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(), Interactive: true})
	if err != nil || res.ExitCode != 0 {
		return apperrors.New("vite_editor_failed", "$EDITOR exited unsuccessfully", "Fix the configuration and run ncp enable vite again", apperrors.ExitCodeRuntime)
	}
	return nil
}
func writeViteConfig(path string, body []byte, mode fs.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nixcp-vite-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(mode.Perm()); err == nil {
		_, err = tmp.Write(body)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// patchViteConfig only recognizes static object literals. It deliberately
// rejects dynamic configuration rather than risking a lossy rewrite.
func patchViteConfig(src []byte) ([]byte, error) {
	root, err := viteExportObject(src)
	if err != nil {
		return nil, err
	}
	props, err := viteProperties(src, root)
	if err != nil {
		return nil, err
	}
	server := property(props, "server")
	if server == nil {
		return applyViteEdits(src, []viteEdit{{root.close, root.close, viteObjectAddition(src, root, "server", viteServerValue)}}), nil
	}
	if server.object == nil {
		return nil, fmt.Errorf("server must be a static object")
	}
	return patchViteServer(src, *server.object)
}

const viteServerValue = "{\n    host: '0.0.0.0',\n    hmr: {\n        host: 'renew-crm.p.ohost.cloud',\n        protocol: 'wss',\n        clientPort: 443,\n    },\n    watch: {\n        ignored: ['**/storage/framework/views/**'],\n    },\n}"

func patchViteServer(src []byte, server viteObject) ([]byte, error) {
	props, err := viteProperties(src, server)
	if err != nil {
		return nil, err
	}
	edits, missing := []viteEdit{}, []viteField{}
	if p := property(props, "host"); p != nil {
		edits = append(edits, viteEdit{p.valueStart, p.valueEnd, "'0.0.0.0'"})
	} else {
		missing = append(missing, viteField{"host", "'0.0.0.0'"})
	}
	if p := property(props, "hmr"); p != nil {
		if p.object == nil {
			return nil, fmt.Errorf("server.hmr must be a static object")
		}
		child, err := patchFlatObject(src, *p.object, []viteField{{"host", "'renew-crm.p.ohost.cloud'"}, {"protocol", "'wss'"}, {"clientPort", "443"}})
		if err != nil {
			return nil, err
		}
		edits = append(edits, child...)
	} else {
		missing = append(missing, viteField{"hmr", "{ host: 'renew-crm.p.ohost.cloud', protocol: 'wss', clientPort: 443 }"})
	}
	if p := property(props, "watch"); p != nil {
		if p.object == nil {
			return nil, fmt.Errorf("server.watch must be a static object")
		}
		child, err := patchWatch(src, *p.object)
		if err != nil {
			return nil, err
		}
		edits = append(edits, child...)
	} else {
		missing = append(missing, viteField{"watch", "{ ignored: ['**/storage/framework/views/**'] }"})
	}
	if len(missing) != 0 {
		edits = append(edits, viteEdit{server.close, server.close, viteObjectAdditions(src, server, missing)})
	}
	return applyViteEdits(src, edits), nil
}

type viteField struct{ name, value string }

func patchFlatObject(src []byte, o viteObject, want []viteField) ([]viteEdit, error) {
	ps, err := viteProperties(src, o)
	if err != nil {
		return nil, err
	}
	var edits []viteEdit
	var missing []viteField
	for _, f := range want {
		if p := property(ps, f.name); p != nil {
			edits = append(edits, viteEdit{p.valueStart, p.valueEnd, f.value})
		} else {
			missing = append(missing, f)
		}
	}
	if len(missing) != 0 {
		edits = append(edits, viteEdit{o.close, o.close, viteObjectAdditions(src, o, missing)})
	}
	return edits, nil
}
func patchWatch(src []byte, o viteObject) ([]viteEdit, error) {
	ps, err := viteProperties(src, o)
	if err != nil {
		return nil, err
	}
	p := property(ps, "ignored")
	if p == nil {
		return []viteEdit{{o.close, o.close, viteObjectAddition(src, o, "ignored", "['**/storage/framework/views/**']")}}, nil
	}
	value := src[p.valueStart:p.valueEnd]
	if len(value) < 2 || value[0] != '[' || value[len(value)-1] != ']' {
		return nil, fmt.Errorf("server.watch.ignored must be an array")
	}
	if bytes.Contains(value, []byte("**/storage/framework/views/**")) {
		return nil, nil
	}
	return []viteEdit{{p.valueEnd - 1, p.valueEnd - 1, "'**/storage/framework/views/**', "}}, nil
}
func property(ps []viteProperty, name string) *viteProperty {
	for i := range ps {
		if ps[i].name == name {
			return &ps[i]
		}
	}
	return nil
}
func viteObjectAddition(src []byte, o viteObject, name, value string) string {
	return viteObjectAdditions(src, o, []viteField{{name, value}})
}
func viteObjectAdditions(src []byte, o viteObject, fields []viteField) string {
	body := bytes.TrimSpace(src[o.open+1 : o.close])
	prefix := "\n    "
	if len(body) > 0 {
		prefix = ",\n    "
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, f.name+": "+f.value)
	}
	return prefix + strings.Join(parts, ",\n    ") + "\n"
}
func applyViteEdits(src []byte, edits []viteEdit) []byte {
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	for _, e := range edits {
		src = append(append(append([]byte{}, src[:e.start]...), []byte(e.text)...), src[e.end:]...)
	}
	return src
}

func viteExportObject(s []byte) (viteObject, error) {
	i := bytes.Index(s, []byte("export default"))
	if i < 0 {
		return viteObject{}, fmt.Errorf("could not find export default")
	}
	i += len("export default")
	i = viteSpace(s, i)
	if bytes.HasPrefix(s[i:], []byte("defineConfig")) {
		i += len("defineConfig")
		i = viteSpace(s, i)
		if i >= len(s) || s[i] != '(' {
			return viteObject{}, fmt.Errorf("invalid defineConfig call")
		}
		i = viteSpace(s, i+1)
	}
	if i >= len(s) || s[i] != '{' {
		return viteObject{}, fmt.Errorf("export default must contain a static object")
	}
	end, err := viteMatching(s, i, '{', '}')
	return viteObject{i, end}, err
}
func viteSpace(s []byte, i int) int {
	for i < len(s) && unicode.IsSpace(rune(s[i])) {
		i++
	}
	return i
}
func viteMatching(s []byte, start int, open, close byte) (int, error) {
	depth := 0
	for i := start; i < len(s); i++ {
		if s[i] == '\'' || s[i] == '"' || s[i] == '`' {
			e, err := viteSkipString(s, i)
			if err != nil {
				return 0, err
			}
			i = e
			continue
		}
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '/' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
			e := bytes.Index(s[i+2:], []byte("*/"))
			if e < 0 {
				return 0, fmt.Errorf("unterminated comment")
			}
			i += e + 3
			continue
		}
		if s[i] == open {
			depth++
		}
		if s[i] == close {
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("unterminated object")
}
func viteSkipString(s []byte, i int) (int, error) {
	quote := s[i]
	for i++; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == quote {
			return i, nil
		}
	}
	return 0, fmt.Errorf("unterminated string")
}
func viteProperties(s []byte, o viteObject) ([]viteProperty, error) {
	var out []viteProperty
	for i := o.open + 1; i < o.close; {
		i = viteSkipTrivia(s, i, o.close)
		if i >= o.close {
			break
		}
		start := i
		for i < o.close && (unicode.IsLetter(rune(s[i])) || unicode.IsDigit(rune(s[i])) || s[i] == '_' || s[i] == '$') {
			i++
		}
		if start == i {
			return nil, fmt.Errorf("unsupported object property")
		}
		name := string(s[start:i])
		i = viteSpace(s, i)
		if i >= o.close || s[i] != ':' {
			return nil, fmt.Errorf("unsupported property %s", name)
		}
		i = viteSpace(s, i+1)
		end, obj, err := viteValueEnd(s, i, o.close)
		if err != nil {
			return nil, err
		}
		out = append(out, viteProperty{name, i, end, obj})
		i = end
	}
	return out, nil
}
func viteSkipTrivia(s []byte, i, limit int) int {
	for i < limit {
		if unicode.IsSpace(rune(s[i])) || s[i] == ',' {
			i++
			continue
		}
		if i+1 < limit && s[i] == '/' && s[i+1] == '/' {
			for i < limit && s[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < limit && s[i] == '/' && s[i+1] == '*' {
			e := bytes.Index(s[i+2:limit], []byte("*/"))
			if e < 0 {
				return limit
			}
			i += e + 4
			continue
		}
		break
	}
	return i
}
func viteValueEnd(s []byte, i, limit int) (int, *viteObject, error) {
	if i >= limit {
		return 0, nil, fmt.Errorf("missing value")
	}
	var obj *viteObject
	if s[i] == '{' {
		end, err := viteMatching(s, i, '{', '}')
		if err != nil {
			return 0, nil, err
		}
		obj = &viteObject{i, end}
	}
	depth := 0
	for j := i; j < limit; j++ {
		if s[j] == '\'' || s[j] == '"' || s[j] == '`' {
			end, err := viteSkipString(s, j)
			if err != nil {
				return 0, nil, err
			}
			j = end
			continue
		}
		if s[j] == '(' || s[j] == '[' || s[j] == '{' {
			depth++
		}
		if s[j] == ')' || s[j] == ']' || s[j] == '}' {
			if depth == 0 {
				return j, obj, nil
			}
			depth--
		}
		if s[j] == ',' && depth == 0 {
			return j, obj, nil
		}
	}
	return limit, obj, nil
}
