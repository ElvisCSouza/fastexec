// Command fastexec: servidor MCP e CLI. Toda a lógica está no pacote cli, que também serve de base para builds
// com subcomandos próprios (cli.Register).
package main

import "homelab/fastexec/cli"

func main() { cli.Main() }
