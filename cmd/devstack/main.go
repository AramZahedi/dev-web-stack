// Command devstack is the user-facing CLI for dev-web-stack.
package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/cli"
	"github.com/AramZahedi/dev-web-stack/internal/client"
)

func main() {
	root := newRootCmd()
	root.SetOut(os.Stdout)
	root.SetErr(os.Stderr)
	root.SetIn(os.Stdin)
	if err := root.Execute(); err != nil {
		cli.PrintError(root, err)
		if code := cli.ExitCode(err); code > 0 {
			os.Exit(code)
		}
		os.Exit(1)
	}
}

// newRootCmd builds the user-facing command tree and adds the internal
// serve-static command the supervisor launches for static sites.
func newRootCmd() *cobra.Command {
	root := cli.NewRootCmd(func(home string) (cli.Client, error) {
		return client.New(home)
	})
	root.AddCommand(newServeStaticCmd())
	return root
}
