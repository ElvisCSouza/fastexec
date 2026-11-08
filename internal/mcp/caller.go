package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

// Caller identity. MCP clients do not send a conversation id (verified with the
// session probe: Claude Desktop sends only name + arguments). What is
// deterministic is the channel: the clientInfo.name of this stdio connection
// plus the pid of this proxy process. It travels to the shared daemon as the
// hidden argument "_caller", so per-channel counters and telemetry do not mix
// different clients.

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

// withCaller adds "_caller" to a JSON object of arguments (kept if already set).
func withCaller(args json.RawMessage, caller string) json.RawMessage {
	var m map[string]json.RawMessage
	if len(args) == 0 {
		m = map[string]json.RawMessage{}
	} else if json.Unmarshal(args, &m) != nil || m == nil {
		return args
	}
	if _, ok := m["_caller"]; ok {
		return args
	}
	b, _ := json.Marshal(caller)
	m["_caller"] = b
	out, err := json.Marshal(m)
	if err != nil {
		return args
	}
	return out
}

// callerOf reads "_caller" back from the arguments ("" when absent).
func callerOf(args json.RawMessage) string {
	var c struct {
		Caller string `json:"_caller"`
	}
	_ = json.Unmarshal(args, &c)
	return c.Caller
}
