package command

import (
	"sort"
	"strings"

	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newSkillCommand prints the full command reference — every visible command
// with its synopsis, flags, and examples — in one deterministic block. It
// exists so an AI agent (or a new contributor) can load the entire CLI
// surface in a single call instead of walking help screens one by one.
func newSkillCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "skill",
		Short:   "Print the full command reference",
		Long:    "Print every command with its usage line, synopsis, flags, and examples as one deterministic reference. Intended for agents and scripts that need the complete CLI surface in a single call.",
		Example: "  ncp skill\n  ncp skill --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries := collectCommandReference(cmd.Root())
			if commandJSON(cmd) {
				return emitJSON(cmd, output.Success("skill", false, map[string]any{"commands": entries}, nil))
			}
			var b strings.Builder
			for _, e := range entries {
				b.WriteString("== ")
				b.WriteString(e["path"])
				if u := e["use"]; u != "" && u != e["name"] {
					b.WriteString(" (" + u + ")")
				}
				b.WriteString(" ==\n")
				if s := e["short"]; s != "" {
					b.WriteString("synopsis: ")
					b.WriteString(s)
					b.WriteString("\n")
				}
				if f := e["flags"]; f != "" && f != "(none)" {
					b.WriteString("flags:\n")
					for _, line := range strings.Split(f, "\n") {
						b.WriteString("  ")
						b.WriteString(line)
						b.WriteString("\n")
					}
				}
				if x := e["example"]; x != "" {
					b.WriteString("examples:\n")
					for _, line := range strings.Split(strings.TrimRight(x, "\n"), "\n") {
						b.WriteString("  ")
						b.WriteString(strings.TrimPrefix(line, "  "))
						b.WriteString("\n")
					}
				}
				b.WriteString("\n")
			}
			b.WriteString("== global flags ==\n")
			b.WriteString("  --json — Emit a single JSON object\n")
			b.WriteString("  --no-input — Disable interactive prompts\n")
			b.WriteString("  --yes — Skip confirmation prompts\n")
			b.WriteString("  --timeout <duration> — Operation timeout (default: 30s)\n")
			cmd.Print(b.String())
			return nil
		},
	}
}

// commandRefEntry is the JSON shape of one command reference entry.
type commandRefEntry map[string]string

// collectCommandReference walks the command tree depth-first, skipping
// hidden commands and the built-in completion/help plumbing, and returns
// entries sorted by command path for deterministic output.
func collectCommandReference(root *cobra.Command) []commandRefEntry {
	var entries []commandRefEntry
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" || sub.Name() == "completion" {
				continue
			}
			name := sub.Name()
			use := strings.Fields(sub.Use)[0]
			entry := commandRefEntry{
				"path":    path + " " + name,
				"name":    name,
				"use":     strings.TrimSpace(sub.Use),
				"short":   sub.Short,
				"example": strings.TrimRight(sub.Example, "\n"),
			}
			if sub.HasAvailableFlags() {
				entry["flags"] = flagReference(sub)
			} else {
				entry["flags"] = "(none)"
			}
			entries = append(entries, entry)
			walk(sub, path+" "+use)
		}
	}
	walk(root, root.Name())
	sort.Slice(entries, func(i, j int) bool { return entries[i]["path"] < entries[j]["path"] })
	return entries
}

// flagReference renders one flag per line as "--name <type> (default: x)".
func flagReference(cmd *cobra.Command) string {
	cmd.Flags().SortFlags = true
	var lines []string
	local := cmd.LocalFlags()
	if local == nil {
		return ""
	}
	local.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		line := "--" + f.Name
		if f.Shorthand != "" {
			line = "-" + f.Shorthand + ", " + line
		}
		var typeHint string
		switch f.Value.Type() {
		case "bool":
			// bools need no value argument
		case "string":
			typeHint = " <string>"
		case "int":
			typeHint = " <int>"
		case "duration":
			typeHint = " <duration>"
		default:
			typeHint = " <" + f.Value.Type() + ">"
		}
		line += typeHint
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "[]" {
			line += " (default: " + f.DefValue + ")"
		}
		line += " — " + f.Usage
		lines = append(lines, line)
	})
	return strings.Join(lines, "\n")
}
