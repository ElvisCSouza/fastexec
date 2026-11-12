//go:build !windows

package cli

import (
	"fmt"
	"os"
)

// startTray: sem ícone de bandeja fora do Windows; nada a fazer.
func startTray(_ string, _ bool) string { return "" }

// cmdTray: o ícone de bandeja só existe no Windows; nos outros sistemas use `fastexec monitor`.
func cmdTray(_ []string) {
	fmt.Fprintln(os.Stderr, "The tray icon is only available on Windows. Use: fastexec monitor")
	os.Exit(1)
}
