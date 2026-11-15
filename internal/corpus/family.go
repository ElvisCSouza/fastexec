package corpus

import (
	"regexp"
	"strings"
)

// A recorded label is often a wrapper (bash -lc "...", powershell -Command "...", cd x && ...). Tool names the real
// program; Category groups programs into the families the comparison cares about. Both are tables, so a new
// wrapper or a new program is a new entry, not a new branch.

// unwrapper peels one wrapper off a command line; ok is false when the line is not wrapped that way.
type unwrapper struct {
	re *regexp.Regexp
	is string // when set, the wrapper itself names the tool (a script file hides what it runs)
}

// exePath matches an optional folder before a program name, quoted or not, with spaces allowed after a drive letter.
const (
	exePath = `(?:"?[A-Za-z]:[^"]*?[\\/]|\S*[\\/])?`
	exeExt  = `(?:\.exe)?"?`
)

func wrap(pattern string) unwrapper { return unwrapper{re: regexp.MustCompile(pattern)} }

var unwrappers = []unwrapper{
	wrap(`(?is)^` + exePath + `(?:bash|sh|zsh)` + exeExt + `\s+(?:-[a-z]+\s+)*-[a-z]*c\s+(.+)$`),
	wrap(`(?is)^` + exePath + `(?:powershell|pwsh)` + exeExt + `\s+.*?-(?:command|c)\s+(.+)$`),
	{re: regexp.MustCompile(`(?is)^` + exePath + `(?:powershell|pwsh)` + exeExt + `\s+.*?-file\s+(\S+)`), is: "script"},
	wrap(`(?is)^` + exePath + `cmd` + exeExt + `\s+/[ck]\s+(.+)$`),
	wrap(`(?is)^cd\s+(?:/d\s+)?(?:"[^"]*"|\S+)\s*(?:&&|&|;)\s*(.+)$`),
	wrap(`(?is)^(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+)+(.+)$`),
	wrap(`(?is)^set\s+\S+\s*&\s*(.+)$`),
	wrap(`(?is)^\$env:\w+\s*=\s*(?:'[^']*'|"[^"]*"|[^;\s]+)\s*;\s*(.+)$`),
	wrap(`(?is)^set-location\s+(?:"[^"]*"|'[^']*'|\S+?)\s*;\s*(.+)$`),
	wrap(`(?is)^&\s+(.+)$`),
}

// firstStage cuts a command line at the first pipe, && or ;.
var firstStage = regexp.MustCompile(`\s*(?:\|\||\||&&|;)\s*`)

// Tool returns the lower-case name of the program a label really runs ("git", "go", "get-childitem").
// A PowerShell script file is reported as "script"; an empty label as "other".
func Tool(label string) string {
	s := strings.TrimSpace(label)
	for i := 0; i < 8; i++ {
		peeled := false
		for _, u := range unwrappers {
			if m := u.re.FindStringSubmatch(s); m != nil {
				if u.is != "" {
					return u.is
				}
				s = unquote(strings.TrimSpace(m[1]))
				peeled = true
				break
			}
		}
		if !peeled {
			break
		}
	}
	s = firstStage.Split(s, 2)[0]
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "other"
	}
	tok := strings.Trim(fields[0], `"'`)
	if i := strings.LastIndexAny(tok, `\/`); i >= 0 {
		tok = tok[i+1:]
	}
	tok = strings.ToLower(strings.TrimSuffix(strings.ToLower(tok), ".exe"))
	if tok == "" || strings.HasPrefix(tok, "$") {
		return "other"
	}
	return tok
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// categoryOf maps a tool name to a family. Unlisted tools are "other".
var categoryOf = map[string]string{}

// RegisterCategory files tools under a family.
func RegisterCategory(category string, tools ...string) {
	for _, t := range tools {
		categoryOf[strings.ToLower(t)] = category
	}
}

// Category names the family of a tool.
func Category(tool string) string {
	if c, ok := categoryOf[tool]; ok {
		return c
	}
	return "other"
}

func init() {
	RegisterCategory("git", "git", "gh")
	RegisterCategory("build_test", "go", "gofmt", "dotnet", "npm", "pnpm", "yarn", "node", "cargo", "rustc", "make", "msbuild", "pytest", "tsc", "zig", "gradle", "mvn", "cmake")
	RegisterCategory("listagem", "ls", "dir", "get-childitem", "gci", "tree")
	RegisterCategory("busca", "grep", "rg", "findstr", "select-string", "ag", "find", "fd", "sls")
	RegisterCategory("leitura", "cat", "type", "get-content", "gc", "head", "sed", "awk", "more", "less")
	RegisterCategory("logs", "tail", "journalctl", "get-winevent", "dmesg", "tcpdump")
	RegisterCategory("container", "docker", "kubectl", "podman")
	RegisterCategory("dados", "sqlite3", "duckdb", "sql", "psql", "mysql")
	RegisterCategory("sistema", "ps", "tasklist", "get-process", "systemctl", "uname", "where", "which", "date", "echo", "set", "sc", "taskkill", "test-path", "fastexec", "opencode-cli", "rm", "del", "cd", "copy-item", "remove-item", "mkdir", "mv", "cp")
}
