// Command syp is the shrink-your-shit program.
package main

import (
	"fmt"
	"os"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println("syp", version)
		return
	}
	fmt.Fprintln(os.Stderr, "usage: syp version")
	os.Exit(2)
}
