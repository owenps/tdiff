package main

import (
	"fmt"
	"os"

	"github.com/owenps/tdiff/internal/cli"
)

func main() {
	if err := cli.Run(); err != nil {
		code := 1
		if e, ok := err.(interface{ ExitCode() int }); ok {
			code = e.ExitCode()
		}
		if err.Error() != "" {
			fmt.Fprintln(os.Stderr, "tdiff:", err)
		}
		os.Exit(code)
	}
}
