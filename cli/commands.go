package cli

// commands.go: subcomandos do CLI. Um subcomando novo = uma função aqui (ou em arquivo próprio) + uma linha em
// commands (ou cli.Register, de fora do módulo); o Main() não muda. Corpos extraídos do switch antigo do main() sem alteração.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"homelab/fastexec/internal/corpus"
	"homelab/fastexec/internal/daemon"
	"homelab/fastexec/internal/telemetry"
	"homelab/fastexec/internal/tools"
	"homelab/fastexec/internal/transport"
)

var commands = map[string]func(subArgs []string){
	"daemon-run":    cmdDaemonRun,
	"daemon-status": cmdDaemonStatus,
	"daemon-stop":   cmdDaemonStop,
	"daemon":        cmdDaemon,
	"exec":          cmdExec,
	"get":           cmdGet,
	"put":           cmdPut,
	"sql":           cmdSql,
	"job":           cmdJob,
	"ping":          cmdPing,
	"stats":         cmdStats,
	"discover":      cmdDiscover,
	"handoff":       cmdHandoff,
	"memory":        cmdHandoff,
	"graph":         cmdGraph,
	"config":        cmdConfig,
	"version":       cmdVersion,
	"token":         cmdToken,
	"doctor":        cmdDoctor,
	"lsp":           cmdLsp,
	"setup":         cmdSetup,
	"monitor":       cmdMonitor,
	"tray":          cmdTray,
}

// cmdDaemonRun atende: daemon-run.
func cmdDaemonRun(subArgs []string) {
	if err := daemon.RunDaemon(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// cmdDaemonStatus atende: daemon-status.
func cmdDaemonStatus(subArgs []string) {
	pid, err := daemon.PingDaemon()
	if err != nil {
		fmt.Println("Local daemon: not running")
	} else {
		fmt.Printf("Local daemon: running (PID %d on %s)\n", pid, daemon.Addr)
	}
}

// cmdDaemonStop atende: daemon-stop.
func cmdDaemonStop(subArgs []string) {
	if err := daemon.StopDaemon(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Local daemon stopped.")
}

// cmdDaemon atende: daemon.
func cmdDaemon(subArgs []string) {
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
		fmt.Println("Local daemon stopped.")
	case "status", "ping":
		pid, err := daemon.PingDaemon()
		if err != nil {
			fmt.Println("Local daemon: not running")
		} else {
			fmt.Printf("Local daemon: running (PID %d on %s)\n", pid, daemon.Addr)
		}
	default:
		fmt.Println("Usage: fastexec daemon <status|start|stop>")
	}
}

// cmdExec atende: exec.
func cmdExec(subArgs []string) {
	if len(subArgs) < 1 {
		fmt.Println("Usage: fastexec exec [host] <command>")
		os.Exit(1)
	}
	host := "" // vazio = host padrão da config
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
			fmt.Fprintln(os.Stderr, "command failed")
		}
		os.Exit(1)
	}
}

// cmdGet atende: get.
func cmdGet(subArgs []string) {
	if len(subArgs) < 1 {
		fmt.Println("Usage: fastexec get <remote_path> [local_path] [host]")
		os.Exit(1)
	}
	remotePath := subArgs[0]
	localPath := ""
	host := "" // vazio = host padrão da config
	if len(subArgs) >= 2 {
		localPath = subArgs[1]
	}
	if len(subArgs) >= 3 {
		host = subArgs[2]
	}
	if h, p := tools.SplitHostPath(remotePath); h != "" && h != "local" {
		host = h
		remotePath = p
	}
	out, err := tools.HandleGetFile(tools.GetFileArgs{Host: host, RemotePath: remotePath, LocalPath: localPath})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(out)
}

// cmdPut atende: put.
func cmdPut(subArgs []string) {
	if len(subArgs) < 2 {
		fmt.Println("Usage: fastexec put <local_path> <remote_path> [host]")
		os.Exit(1)
	}
	localPath := subArgs[0]
	remotePath := subArgs[1]
	host := "" // vazio = host padrão da config
	if len(subArgs) >= 3 {
		host = subArgs[2]
	}
	if h, p := tools.SplitHostPath(remotePath); h != "" && h != "local" {
		host = h
		remotePath = p
	} else if hLoc, pLoc := tools.SplitHostPath(localPath); hLoc != "" && hLoc != "local" {
		host = hLoc
		remotePath = pLoc
		localPath = subArgs[1]
	}
	out, err := tools.HandlePutFile(tools.PutFileArgs{Host: host, LocalPath: localPath, RemotePath: remotePath})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(out)
}

// cmdSql atende: sql.
func cmdSql(subArgs []string) {
	if len(subArgs) < 1 {
		fmt.Println("Usage: fastexec sql \"<query>\" [session_or_db] [host]")
		os.Exit(1)
	}
	var query, db, session, host string
	if len(subArgs) == 1 {
		query = subArgs[0]
		session = "default"
	} else if strings.HasSuffix(subArgs[0], ".db") || strings.HasSuffix(subArgs[0], ".sqlite") {
		db = subArgs[0]
		query = subArgs[1]
		host = ""
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
}

// cmdJob atende: job.
func cmdJob(subArgs []string) {
	if len(subArgs) < 1 {
		subArgs = []string{"list"}
	}
	action := subArgs[0]
	var jobCmd, jobID string
	var lines int
	host := "" // vazio = host padrão da config

	switch action {
	case "start":
		if len(subArgs) < 2 {
			fmt.Println("Usage: fastexec job start \"<command>\" [job_id] [host]")
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
}

// cmdPing atende: ping.
func cmdPing(subArgs []string) {
	host := "all"
	if len(subArgs) >= 1 {
		host = subArgs[0]
	}
	out, err := tools.HandlePing(tools.PingArgs{Host: host})
	fmt.Println(out)
	if err != nil {
		os.Exit(1)
	}
}

// flagValue reads "--name value" or "--name=value" at args[*i]. It advances *i past a separate value. A missing
// value is an error: the command stops rather than silently ignoring the filter.
func flagValue(args []string, i *int, name string) (string, bool) {
	arg := args[*i]
	if strings.HasPrefix(arg, name+"=") {
		return strings.TrimPrefix(arg, name+"="), true
	}
	if arg != name {
		return "", false
	}
	if *i+1 >= len(args) || strings.HasPrefix(args[*i+1], "-") {
		fmt.Fprintf(os.Stderr, "fastexec stats: %s needs a value\n", name)
		os.Exit(2)
	}
	*i++
	return args[*i], true
}

// cmdStats atende: stats.
func cmdStats(subArgs []string) {
	filter := telemetry.StatsFilter{}
	measureMode, corpusMode, corpusRulesMode := false, false, false
	for i := 0; i < len(subArgs); i++ {
		arg := subArgs[i]
		// --build X / --build=X and --client X / --client=X isolate one build or one MCP client.
		if v, ok := flagValue(subArgs, &i, "--build"); ok {
			filter.Build = v
			continue
		}
		if v, ok := flagValue(subArgs, &i, "--client"); ok {
			filter.Client = v
			continue
		}
		switch strings.ToLower(arg) {
		case "--help", "-h", "help":
			fmt.Println("usage: fastexec stats [--today|-t] [--errors|-e] [--tools] [--measure] [--corpus] [--reset|--clear] [--build <v>] [--client <name>] [<period>]")
			fmt.Println("  --build X   only rows of FastExec build X (app_version or app_build)")
			fmt.Println("  --client X  only rows of MCP client X (client_name)")
			fmt.Println("  <period>  1h, 24h, 7d ... (hours or days; bare 'today' and 'week' also work)")
			fmt.Println("  Shows telemetry & audit for the period, plus a savings summary.")
			fmt.Println("  --errors   only error records      --tools   only the per-tool breakdown")
			fmt.Println("  --measure  measured vs estimated savings, harm signals, usage per feature, exec overhead")
			fmt.Println("  --corpus   recorded exec outputs: samples per command family against the comparison targets")
			fmt.Println("  --corpus-rules  offline: how much group/dedup would save on the recorded outputs")
			fmt.Println("  --reset    delete the telemetry database")
			return
		case "--today", "-t":
			filter.Today = true
		case "--errors", "-e":
			filter.OnlyErrors = true
		case "--tools":
			filter.OnlyTools = true
		case "--measure":
			measureMode = true
		case "--corpus":
			corpusMode = true
		case "--corpus-rules":
			corpusRulesMode = true
		case "--reset", "--clear":
			filter.ResetDB = true
		case "today":
			filter.Today = true
		case "week":
			filter.PeriodH = 168
		default:
			if h, ok := telemetry.ParsePeriod(arg); ok {
				filter.PeriodH = h
			}
		}
	}

	if corpusMode {
		fmt.Print(corpus.RenderDir(corpus.Dir(), nil))
		return
	}

	if corpusRulesMode {
		fmt.Print(tools.CorpusRulesReport(corpus.Dir()))
		return
	}

	render := telemetry.RenderStats
	if measureMode {
		render = telemetry.RenderMeasure
	}
	out, err := render(filter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Telemetry error:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

// cmdDiscover atende: discover [24h|7d|--today]. Where output tokens go and what the exec soft cap saves.
func cmdDiscover(subArgs []string) {
	filter := telemetry.StatsFilter{}
	for _, arg := range subArgs {
		if strings.EqualFold(arg, "--today") || arg == "-t" {
			filter.Today = true
		} else if h, ok := telemetry.ParsePeriod(arg); ok {
			filter.PeriodH = h
		}
	}
	out, err := telemetry.RenderDiscover(filter, 3000)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Telemetry error:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

// cmdHandoff atende: handoff, memory.
func cmdHandoff(subArgs []string) {
	action := "latest"
	var topic, notes, query string
	if len(subArgs) >= 1 {
		action = strings.ToLower(subArgs[0])
	}
	switch action {
	case "save", "generate", "create":
		if len(subArgs) >= 2 {
			topic = subArgs[1]
		}
		if len(subArgs) >= 3 {
			notes = strings.Join(subArgs[2:], " ")
		}
	case "search", "find":
		if len(subArgs) >= 2 {
			query = strings.Join(subArgs[1:], " ")
		}
	case "list", "ls":
		// list
	case "latest", "read", "get":
		// latest
	default:
		if len(subArgs) == 1 {
			query = subArgs[0]
			action = "search"
		}
	}

	out, err := tools.HandleHandoff(tools.HandoffArgs{
		Action: action,
		Topic:  topic,
		Notes:  notes,
		Query:  query,
	})
	fmt.Print(out)
	if !strings.HasSuffix(out, "\n") {
		fmt.Println()
	}
	if err != nil {
		os.Exit(1)
	}
}

// cmdGraph atende: graph.
func cmdGraph(subArgs []string) {
	os.Exit(runGraph(subArgs))
}
