package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"homelab/fastexec/internal/logger"
	"homelab/fastexec/internal/mcp"
	"homelab/fastexec/internal/tools"
	"homelab/fastexec/internal/transport"
)

func main() {
	logger.Init()

	args := os.Args[1:]
	if len(args) == 0 || args[0] == "mcp" {
		server := mcp.NewServer()
		if err := server.Run(); err != nil {
			logger.Error("MCP server finalizado com erro: %v", err)
			os.Exit(1)
		}
		return
	}

	// Modo CLI direto para uso no terminal ou shims
	cmd := strings.ToLower(args[0])
	subArgs := args[1:]

	switch cmd {
	case "exec":
		if len(subArgs) < 1 {
			fmt.Println("Uso: fastexec exec [host] <comando>")
			os.Exit(1)
		}
		host := "noteserver"
		var command string
		if len(subArgs) >= 2 && !strings.Contains(subArgs[0], " ") {
			if _, err := transport.ResolveHost(subArgs[0]); err == nil {
				host = subArgs[0]
				command = strings.Join(subArgs[1:], " ")
			} else {
				command = strings.Join(subArgs, " ")
			}
		} else {
			command = strings.Join(subArgs, " ")
		}

		out, err := tools.HandleExec(tools.ExecArgs{Host: host, Command: command})
		if out != "" {
			fmt.Print(out)
			if !strings.HasSuffix(out, "\n") {
				fmt.Println()
			}
		}
		if err != nil {
			os.Exit(1)
		}

	case "get":
		if len(subArgs) < 1 {
			fmt.Println("Uso: fastexec get <remote_path> [local_path] [host]")
			os.Exit(1)
		}
		remotePath := subArgs[0]
		localPath := ""
		host := "noteserver"
		if len(subArgs) >= 2 {
			localPath = subArgs[1]
		}
		if len(subArgs) >= 3 {
			host = subArgs[2]
		}
		out, err := tools.HandleGetFile(tools.GetFileArgs{Host: host, RemotePath: remotePath, LocalPath: localPath})
		fmt.Println(out)
		if err != nil {
			os.Exit(1)
		}

	case "put":
		if len(subArgs) < 2 {
			fmt.Println("Uso: fastexec put <local_path> <remote_path> [host]")
			os.Exit(1)
		}
		localPath := subArgs[0]
		remotePath := subArgs[1]
		host := "noteserver"
		if len(subArgs) >= 3 {
			host = subArgs[2]
		}
		out, err := tools.HandlePutFile(tools.PutFileArgs{Host: host, LocalPath: localPath, RemotePath: remotePath})
		fmt.Println(out)
		if err != nil {
			os.Exit(1)
		}

	case "sql":
		if len(subArgs) < 1 {
			fmt.Println("Uso: fastexec sql \"<query>\" [session_ou_db] [host]")
			os.Exit(1)
		}
		var query, db, session, host string
		if len(subArgs) == 1 {
			query = subArgs[0]
			session = "default"
		} else if strings.HasSuffix(subArgs[0], ".db") || strings.HasSuffix(subArgs[0], ".sqlite") {
			db = subArgs[0]
			query = subArgs[1]
			host = "noteserver"
			if len(subArgs) >= 3 {
				host = subArgs[2]
			}
		} else {
			query = subArgs[0]
			session = subArgs[1]
		}

		out, err := tools.HandleSql(tools.SqlArgs{Host: host, Database: db, Session: session, Query: query})
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
		if err != nil {
			os.Exit(1)
		}

	case "job":
		if len(subArgs) < 1 {
			subArgs = []string{"list"}
		}
		action := subArgs[0]
		var jobCmd, jobID string
		var lines int
		host := "noteserver"

		switch action {
		case "start":
			if len(subArgs) < 2 {
				fmt.Println("Uso: fastexec job start \"<comando>\" [job_id] [host]")
				os.Exit(1)
			}
			jobCmd = subArgs[1]
			if len(subArgs) >= 3 {
				jobID = subArgs[2]
			}
			if len(subArgs) >= 4 {
				host = subArgs[3]
			}
		case "status", "stop", "kill":
			if len(subArgs) >= 2 {
				jobID = subArgs[1]
			}
			if len(subArgs) >= 3 {
				host = subArgs[2]
			}
		case "tail":
			if len(subArgs) >= 2 {
				jobID = subArgs[1]
			}
			if len(subArgs) >= 3 {
				lines, _ = strconv.Atoi(subArgs[2])
			}
			if len(subArgs) >= 4 {
				host = subArgs[3]
			}
		case "list", "ls":
			if len(subArgs) >= 2 {
				host = subArgs[1]
			}
		}

		out, err := tools.HandleJob(tools.JobArgs{
			Host:    host,
			Action:  action,
			Command: jobCmd,
			JobID:   jobID,
			Lines:   lines,
		})
		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
		if err != nil {
			os.Exit(1)
		}

	case "ping":
		host := "all"
		if len(subArgs) >= 1 {
			host = subArgs[0]
		}
		out, err := tools.HandlePing(tools.PingArgs{Host: host})
		fmt.Println(out)
		if err != nil {
			os.Exit(1)
		}

	default:
		fmt.Printf("Comando desconhecido: '%s'\n", cmd)
		fmt.Println("Comandos disponíveis: mcp, exec, get, put, sql, job, ping")
		os.Exit(1)
	}
}
