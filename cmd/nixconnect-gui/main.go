package main

import (
	"fmt"
	"os"

	"github.com/ojuny-project/nix-connect/internal/cli"
	"github.com/ojuny-project/nix-connect/internal/gui"
	"github.com/ojuny-project/nix-connect/internal/httpapi"
)

func main() {
	client := httpapi.NewLocal(cli.DefaultSocket)
	if err := gui.Run(client); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
