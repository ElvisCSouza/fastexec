package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Caller identity, two levels (checked with a session probe in Nov/2026):
//
//   - conversation: some clients send a conversation id in tools/call _meta.
//     Antigravity sends "antigravity.google/conversation_id" (a UUID, stable
//     per conversation); Codex sends session_id/thread_id. Deterministic.
//   - channel: Claude Desktop sends no id (one process serves all its
//     conversations). The channel is clientInfo.name of this stdio connection
//     plus the pid of this proxy process. Deterministic, but coarser.
//
// Both travel to the shared daemon as hidden arguments "_caller" and
// "_conversation"; per-file hints and telemetry use the finest one available.

var callerSafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func (s *Server) setClient(params json.RawMessage) {
	var p struct {
		ClientInfo struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	_ = json.Unmarshal(params, &p)
	s.clientMu.Lock()
	s.clientName = callerSafe.ReplaceAllString(p.ClientInfo.Name, "_")
	s.clientMu.Unlock()
}

func (s *Server) callerID() string {
	s.clientMu.Lock()
	name := s.clientName
	s.clientMu.Unlock()
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("%s#%d", name, os.Getpid())
}

// conversationOf picks a conversation id from tools/call _meta: the
// Antigravity key first, then any key ending in conversation_id, session_id
// or thread_id (in that order). Only string values; sanitized, max 64 chars.
func conversationOf(meta map[string]json.RawMessage) string {
	if len(meta) == 0 {
		return ""
	}
	str := func(raw json.RawMessage) string {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		s = callerSafe.ReplaceAllString(strings.TrimSpace(s), "_")
		if len(s) > 64 {
			s = s[:64]
		}
		return s
	}
	if v := str(meta["antigravity.google/conversation_id"]); v != "" {
		return v
	}
	for _, suffix := range []string{"conversation_id", "session_id", "thread_id", "conversationid", "sessionid"} {
		for k, raw := range meta {
			if strings.HasSuffix(strings.ToLower(k), suffix) {
				if v := str(raw); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// withCaller adds "_caller" (and "_conversation" when known) to a JSON object
// of arguments; values already present are kept.
func withCaller(args json.RawMessage, caller, conversation string) json.RawMessage {
	var m map[string]json.RawMessage
	if len(args) == 0 {
		m = map[string]json.RawMessage{}
	} else if json.Unmarshal(args, &m) != nil || m == nil {
		return args
	}
	changed := false
	if _, ok := m["_caller"]; !ok {
		b, _ := json.Marshal(caller)
		m["_caller"] = b
		changed = true
	}
	if _, ok := m["_conversation"]; !ok && conversation != "" {
		b, _ := json.Marshal(conversation)
		m["_conversation"] = b
		changed = true
	}
	if !changed {
		return args
	}
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return out
}

// callerOf reads "_caller" and "_conversation" back from the arguments.
func callerOf(args json.RawMessage) (caller, conversation string) {
	var c struct {
		Caller       string `json:"_caller"`
		Conversation string `json:"_conversation"`
	}
	_ = json.Unmarshal(args, &c)
	return c.Caller, c.Conversation
}
