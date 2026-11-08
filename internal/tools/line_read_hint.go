package tools

import "regexp"

// Scripts that slice a file by lines through exec (Get-Content | Select -Skip,
// (Get-Content f)[a..b], sed -n 'a,bp', head/tail -n) bypass read_file and its
// ranges/pattern. The command still runs; the footer says what to use next.

var reLineSlice = regexp.MustCompile(`(?i)(?:\b(?:get-content|gc|cat|type)\b[^|;&]*\|\s*(?:select-object|select)\b[^|;&]*-(?:skip|first|last|index)\b|\(\s*(?:get-content|gc)\b[^)]*\)\s*\[\s*\d+\s*\.\.\s*\d+\s*\]|\bsed\s+-n\s+['"]?\d+(?:,\d*)?p|\b(?:head|tail)\s+-(?:n\s*)?\d+\s+[^|;&\s]+\.\w+)`)

const lineSliceHint = "[FastExec Hint] Slicing a file by lines through exec: read_file(path, start_line, end_line) or read_file(path, ranges='10-40,90-120') does it in one call (several ranges at once), read_file(path, pattern='regex', context=3) returns only the matching lines, and code_outline(path, symbol='name') gives the line of a function."

func isLineSliceRead(cmd string) bool { return reLineSlice.MatchString(cmd) }
