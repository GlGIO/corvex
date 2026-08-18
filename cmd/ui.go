package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/giovannialves/corvex/internal/server"
	"github.com/spf13/cobra"
)

var uiAddr string

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Serve the corvex UI on localhost",
	Long: "Start the local UI server: the gate inbox, the run history and the controls to " +
		"dispatch, approve and stop runs. Single user, localhost only, token in the URL.",
	Args: cobra.NoArgs,
	RunE: runUI,
}

func init() {
	uiCmd.Flags().StringVar(&uiAddr, "addr", "127.0.0.1:0", "address to listen on (port 0 lets the OS pick)")
	rootCmd.AddCommand(uiCmd)
}

func runUI(_ *cobra.Command, _ []string) error {
	workDir, err := workspaceDir()
	if err != nil {
		return err
	}
	srv, err := server.New(server.Options{WorkDir: workDir, Addr: uiAddr})
	if err != nil {
		return err
	}
	// Bound before serving so the URL printed is the URL that works, port and
	// token included — a UI whose address is guessed is a UI nobody opens.
	if _, err := srv.Listen(); err != nil {
		return err
	}

	fmt.Printf("corvex ui — %s\n", srv.URL())
	fmt.Printf("repository: %s\n", workDir)
	fmt.Println("The token in that URL is the whole of the auth: anything without it is refused,")
	fmt.Println("and so is any request that did not arrive on localhost. Ctrl-C stops the server;")
	fmt.Println("runs it started keep going.")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Serve(ctx)
}
