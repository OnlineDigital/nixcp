package command

import (
	"fmt"

	"github.com/nixcp/nixcp/internal/output"
	"github.com/spf13/cobra"
)

func newServiceCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "service", Short: "Manage platform services"}
	for _, service := range []string{"nginx", "mariadb", "redis"} {
		cmd.AddCommand(newServiceSubcommand(service))
	}
	return cmd
}

func newServiceAliasCommand(name string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: name + " service alias"}
	for _, action := range []string{"install", "start", "status", "stop", "restart"} {
		cmd.AddCommand(newServiceAliasAction(name, action))
	}
	return cmd
}

func newServiceSubcommand(name string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: name + " service"}
	for _, action := range []string{"install", "start", "status", "stop", "restart"} {
		cmd.AddCommand(newServiceSubcommandAction(name, action))
	}
	return cmd
}

func newServiceSubcommandAction(service string, action string) *cobra.Command {
	return &cobra.Command{
		Use:   action,
		Short: action + " service " + service,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := output.Success("service."+service+"."+action, action == "install", map[string]any{"service": service, "action": action}, nil)
			if commandJSON(cmd) {
				return emitJSON(cmd, payload)
			}
			cmd.Println(fmt.Sprintf("%s %s", service, action))
			return nil
		},
	}
}

func newServiceAliasAction(service string, action string) *cobra.Command {
	return newServiceSubcommandAction(service, action)
}
