package cli

import "github.com/spf13/cobra"

func taskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "task <description>",
		Short: "Submit an on-demand task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil // implemented in Task 12
		},
	}
}
