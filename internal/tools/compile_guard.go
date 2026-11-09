package tools

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	reGoUndefined    = regexp.MustCompile(`\bundefined:\s+([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)?)\b`)
	reTsCannotFind   = regexp.MustCompile(`\bCannot find name '([A-Za-z_]\w*)'`)
	reJsRefError     = regexp.MustCompile(`\bReferenceError:\s+([A-Za-z_]\w*)\s+is not defined`)
	rePyNameError    = regexp.MustCompile(`\bNameError: name '([A-Za-z_]\w*)' is not defined`)
	rePyImportError  = regexp.MustCompile(`\bImportError: cannot import name '([A-Za-z_]\w*)'`)
	reCppUndeclared  = regexp.MustCompile(`'([A-Za-z_]\w*)' was not declared in this scope`)
	reRustCannotFind = regexp.MustCompile(`\bcannot find (?:value|function|type)\s+` + "`" + `([A-Za-z_]\w*)` + "`")
	reZigUndeclared  = regexp.MustCompile(`\buse of undeclared identifier '([A-Za-z_]\w*)'`)
)

func checkCompileOrTestFailure(cwd, command, output string) string {
	if len(output) > 256*1024 {
		return ""
	}

	var sym string
	if m := reGoUndefined.FindStringSubmatch(output); len(m) > 1 {
		sym = m[1]
		if idx := strings.LastIndex(sym, "."); idx >= 0 {
			sym = sym[idx+1:]
		}
	} else if m := reTsCannotFind.FindStringSubmatch(output); len(m) > 1 {
		sym = m[1]
	} else if m := reJsRefError.FindStringSubmatch(output); len(m) > 1 {
		sym = m[1]
	} else if m := rePyNameError.FindStringSubmatch(output); len(m) > 1 {
		sym = m[1]
	} else if m := rePyImportError.FindStringSubmatch(output); len(m) > 1 {
		sym = m[1]
	} else if m := reCppUndeclared.FindStringSubmatch(output); len(m) > 1 {
		sym = m[1]
	} else if m := reRustCannotFind.FindStringSubmatch(output); len(m) > 1 {
		sym = m[1]
	} else if m := reZigUndeclared.FindStringSubmatch(output); len(m) > 1 {
		sym = m[1]
	}

	if sym == "" {
		return ""
	}

	root := cwd
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	if root == "" {
		return ""
	}

	matches, err := outlineTree(root, sym, 3)
	if err != nil || matches == "" || strings.Contains(matches, "(no declaration") {
		return ""
	}

	return fmt.Sprintf("[FastExec Context Helper] '%s' was not found during execution, but was located in the repository via code_graph:\n%s\nNext: call read_file(path, symbol='%s') or edit_file(path, symbol='%s').", sym, matches, sym, sym)
}
