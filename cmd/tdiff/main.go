package main

import (
	"fmt"
	"os"

	"github.com/owenps/tdiff/internal/cli"
)

func main() {
	if err := cli.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tdiff:", err)
		os.Exit(1)
	}
}
