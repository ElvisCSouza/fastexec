package tools

// text_structure.go: a cheap line index (headings, top-level keys, HTML sections) for big non-code files, so a capped
// read says where things are and the next call can ask for a precise range. Languages the code scanner knows use
// code_outline instead. Line-based and dependency free: it never parses, it only recognises the common shapes.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	structureMaxEntries = 40
	structureMaxLine    = 100
)

var (
	reMdHeading = regexp.MustCompile(`^#{1,4}\s+\S`)
	reYamlKey   = regexp.MustCompile(`^[A-Za-z0-9_"'.\-]+\s*:`)
	reJSONKey   = regexp.MustCompile(`^  "[^"]+"\s*:`)
	reHTMLBlock = regexp.MustCompile(`(?i)<(style|script|body|header|nav|main|section|footer|template|svg)\b|\sid="[^"]{1,40}"`)
)

// textStructure returns "N: text" lines for the file kinds it recognises ("" for anything else or when there is
// too little structure to be worth the tokens).
func textStructure(path, data string) string {
	var re *regexp.Regexp
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		re = reMdHeading
	case ".yaml", ".yml":
		re = reYamlKey
	case ".json":
		re = reJSONKey
	case ".html", ".htm":
		re = reHTMLBlock
	default:
		return ""
	}
	var b strings.Builder
	n := 0
	for i, l := range splitLines(data) {
		if !re.MatchString(l) {
			continue
		}
		if n == structureMaxEntries {
			b.WriteString("...\n")
			break
		}
		l = strings.TrimSpace(l)
		if len(l) > structureMaxLine {
			l = l[:structureMaxLine] + "…"
		}
		fmt.Fprintf(&b, "%d: %s\n", i+1, l)
		n++
	}
	if n < 3 {
		return ""
	}
	return strings.TrimRight(b.String(), "\n")
}
