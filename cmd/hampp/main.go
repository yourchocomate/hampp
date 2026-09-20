// Command hampp runs a local PHP development stack inside Termux on Android.
package main

import (
	"os"

	"github.com/yourchocomate/hampp/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
