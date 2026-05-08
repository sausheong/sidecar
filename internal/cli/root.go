package cli

import "github.com/spf13/cobra"

func RootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sidecar",
		Short: "Autonomous engineering agent that continuously maintains your software",
	}
	root.AddCommand(attachCmd(), taskCmd(), statusCmd())
	return root
}
