package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"homelab/fastexec/internal/config"
)

// Session probe (temporary investigation): records what each MCP client
// sends that could identify a conversation, without storing contents.
// For every initialize and tools/call it writes, to <Data>/probe/mcp-<pid>.jsonl:
// clientInfo, the keys of params and params._meta, and for each _meta value
// only its type, length and a short hash (to see whether it is stable within a
// conversation and different across conversations). Off with FASTEXEC_PROBE=0.
// Capped at probeMaxLines per process.

const probeMaxLines = 400

var (
	probeMu    sync.Mutex
	probeFile  *os.File
	probeLines int
	probeOff   = os.Getenv("FASTEXEC_PROBE") == "0"
	probeEnvRe = regexp.MustCompile(`(?i)claude|anthropic|mcp|session|cowork|conversation`)
)

func shortHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:4])
}

func describeValue(raw json.RawMessage) map[string]any {
	d := map[string]any{"len": len(raw), "hash": shortHash(raw)}
	var v any
	if json.Unmarshal(raw, &v) == nil {
		switch x := v.(type) {
		case string:
			d["type"] = "string"
			d["strlen"] = len(x)
		case float64:
			d["type"] = "number"
		case bool:
			d["type"] = "bool"
		case map[string]any:
			d["type"] = "object"
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			d["keys"] = keys
		case []any:
			d["type"] = "array"
		default:
			d["type"] = "null"
		}
	}
	return d
}

func probeRecord(method string, params json.RawMessage) {
	if probeOff || (method != "initialize" && method != "tools/call") {
		return
	}
	probeMu.Lock()
	defer probeMu.Unlock()
	if probeLines >= probeMaxLines {
		return
	}
	rec := map[string]any{"t": time.Now().Format(time.RFC3339Nano), "pid": os.Getpid(), "method": method}

	if probeFile == nil {
		dir := config.Get().Dir("probe")
		f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("mcp-%d.jsonl", os.Getpid())), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			probeOff = true
			return
		}
		probeFile = f
		var names []string
		for _, kv := range os.Environ() {
			for i := 0; i < len(kv); i++ {
				if kv[i] == '=' {
					if probeEnvRe.MatchString(kv[:i]) {
						names = append(names, kv[:i])
					}
					break
				}
			}
		}
		sort.Strings(names)
		rec["env_names"] = names
		rec["ppid"] = os.Getppid()
	}

	var p map[string]json.RawMessage
	_ = json.Unmarshal(params, &p)
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rec["param_keys"] = keys

	if method == "initialize" {
		var ci any
		_ = json.Unmarshal(p["clientInfo"], &ci)
		rec["clientInfo"] = ci
		var pv string
		_ = json.Unmarshal(p["protocolVersion"], &pv)
		rec["protocolVersion"] = pv
		if c, ok := p["capabilities"]; ok {
			rec["capabilities"] = describeValue(c)["keys"]
		}
	} else {
		var name string
		_ = json.Unmarshal(p["name"], &name)
		rec["tool"] = name
		if m, ok := p["_meta"]; ok {
			var meta map[string]json.RawMessage
			if json.Unmarshal(m, &meta) == nil {
				dm := map[string]any{}
				for k, v := range meta {
					dm[k] = describeValue(v)
				}
				rec["meta"] = dm
			}
		}
		// argumentos com nome de sessão (ex.: injetados por hook): só o formato
		var args map[string]json.RawMessage
		if json.Unmarshal(p["arguments"], &args) == nil {
			for k, v := range args {
				if probeEnvRe.MatchString(k) || (len(k) > 0 && k[0] == '_') {
					if rec["arg_ids"] == nil {
						rec["arg_ids"] = map[string]any{}
					}
					rec["arg_ids"].(map[string]any)[k] = describeValue(v)
				}
			}
		}
	}
	line, _ := json.Marshal(rec)
	_, _ = probeFile.Write(append(line, '\n'))
	probeLines++
}
