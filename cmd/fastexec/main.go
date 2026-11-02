package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"homelab/fastexec/internal/daemon"
	"homelab/fastexec/internal/logger"
	"homelab/fastexec/internal/mcp"
	"homelab/fastexec/internal/telemetry"
	"homelab/fastexec/internal/tools"
	"homelab/fastexec/internal/transport"
)

func main() {
	logger.Init()

	args := os.Args[1:]
	if len(args) == 0 || args[0] == "mcp" {
		server := mcp.NewServer()
		server.Forwarder = func(name string, rawArgs json.RawMessage) (mcp.ToolCallResult, error) {
			res, err := daemon.CallTool(name, rawArgs)
			if err != nil {
				return mcp.ToolCallResult{}, err
			}
			return *res, nil
		}
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
	case "daemon-run":
		if err := daemon.RunDaemon(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

	case "daemon-status":
		pid, err := daemon.PingDaemon()
		if err != nil {
			fmt.Println("Daemon local: inativo")
		} else {
			fmt.Printf("Daemon local: ativo (PID %d em %s)\n", pid, daemon.Addr)
		}

	case "daemon-stop":
		if err := daemon.StopDaemon(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Daemon local encerrado com sucesso.")

	case "daemon":
		sub := "status"
		if len(subArgs) > 0 {
			sub = strings.ToLower(subArgs[0])
		}
		switch sub {
		case "start", "run":
			if err := daemon.RunDaemon(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		case "stop":
			if err := daemon.StopDaemon(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println("Daemon local encerrado com sucesso.")
		case "status", "ping":
			pid, err := daemon.PingDaemon()
			if err != nil {
				fmt.Println("Daemon local: inativo")
			} else {
				fmt.Printf("Daemon local: ativo (PID %d em %s)\n", pid, daemon.Addr)
			}
		default:
			fmt.Println("Uso: fastexec daemon <status|start|stop>")
		}

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

		execArgs := tools.ExecArgs{Host: host, Command: command}
		argsBytes, _ := json.Marshal(execArgs)

		var out string
		var isErr bool
		if res, err := daemon.CallTool("exec", argsBytes); err == nil && res != nil {
			if len(res.Content) > 0 {
				out = res.Content[0].Text
			}
			isErr = res.IsError
		} else {
			var herr error
			out, herr = tools.HandleExec(execArgs)
			isErr = (herr != nil)
		}

		if out != "" {
			fmt.Print(out)
			if !strings.HasSuffix(out, "\n") {
				fmt.Println()
			}
		}
		if isErr {
			if out == "" {
				fmt.Fprintln(os.Stderr, "comando falhou")
			}
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
		case "status", "stop", "kill", "wait":
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

		jobArgs := tools.JobArgs{
			Host:    host,
			Action:  action,
			Command: jobCmd,
			JobID:   jobID,
			Lines:   lines,
		}
		jobBytes, _ := json.Marshal(jobArgs)

		var out string
		var isErr bool
		if res, err := daemon.CallTool("job", jobBytes); err == nil && res != nil {
			if len(res.Content) > 0 {
				out = res.Content[0].Text
			}
			isErr = res.IsError
		} else {
			var herr error
			out, herr = tools.HandleJob(jobArgs)
			isErr = (herr != nil)
		}

		fmt.Print(out)
		if !strings.HasSuffix(out, "\n") {
			fmt.Println()
		}
		if isErr {
			os.Exit(1)
		}

	case "firestick":
		runFirestick(subArgs)

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

	case "stats":
		filter := telemetry.StatsFilter{}
		for _, arg := range subArgs {
			switch strings.ToLower(arg) {
			case "--today", "-t":
				filter.Today = true
			case "--errors", "-e":
				filter.OnlyErrors = true
			case "--tools":
				filter.OnlyTools = true
			case "--reset", "--clear":
				filter.ResetDB = true
			}
		}

		out, err := telemetry.RenderStats(filter)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Erro ao obter telemetria:", err)
			os.Exit(1)
		}
		fmt.Print(out)

	default:
		fmt.Printf("Comando desconhecido: '%s'\n", cmd)
		fmt.Println("Comandos disponíveis: mcp, exec, get, put, sql, job, ping, stats, firestick, daemon")
		os.Exit(1)
	}
}
