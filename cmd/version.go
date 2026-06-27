package cmd

import (
	"fmt"

	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the corvex version",
	Args:  cobra.NoArgs,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("corvex %s\n", types.Version)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
