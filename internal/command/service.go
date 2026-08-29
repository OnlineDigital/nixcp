package command

import "github.com/spf13/cobra"

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
		cmd.AddCommand(newServiceAction(name, action))
	}
	return cmd
}
func newServiceSubcommand(name string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: name + " service"}
	for _, action := range []string{"install", "start", "status", "stop", "restart"} {
		cmd.AddCommand(newServiceAction(name, action))
	}
	return cmd
}
func newServiceAction(service, action string) *cobra.Command {
	return &cobra.Command{Use: action, Short: action + " service " + service, Args: cobra.NoArgs, RunE: unavailableRun("service " + service + " " + action)}
}
