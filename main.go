package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "rex-md-viewer:", safeText(err.Error()))
		if _, ok := err.(*usageError); ok {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
