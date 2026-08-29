package command

import (
	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newVersionCommand(meta BuildMetadata) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, args []string) error {
			data := map[string]string{
				"version": meta.Version,
			}
			if meta.Commit != "" {
				data["commit"] = meta.Commit
			}
			if meta.BuiltAt != "" {
				data["built_at"] = meta.BuiltAt
			}
			payload := output.Success("version", false, data, nil)
			if cmd.Flags().Changed("json") {
				return emitJSON(cmd, payload)
			}
			cmd.Printf("%s\n", meta.Version)
			return nil
		},
	}
}
