package main

import (
	"os"

	"github.com/idestis/recap/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
