package cli

import "github.com/spf13/cobra"

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show recent tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil // implemented in Task 13
		},
	}
}
