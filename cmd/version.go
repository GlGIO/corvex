package cmd

import (
	"fmt"
	"os"

	"github.com/giovannialves/corvex/internal/types"
	"github.com/spf13/cobra"
)

var versionJSON *bool

type versionOutput struct {
	Version string `json:"version"`
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the corvex version",
	Args:  cobra.NoArgs,
	RunE:  runVersion,
}

func init() {
	versionJSON = addJSONFlag(versionCmd)
	rootCmd.AddCommand(versionCmd)
}

func runVersion(_ *cobra.Command, _ []string) error {
	if *versionJSON {
		return printJSON(os.Stdout, versionOutput{Version: types.Version})
	}
	fmt.Printf("corvex %s\n", types.Version)
	return nil
}
