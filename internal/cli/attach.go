package cli

import "github.com/spf13/cobra"

func attachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach [path]",
		Short: "Attach to a project and start the sidecar daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil // implemented in Task 11
		},
	}
}
