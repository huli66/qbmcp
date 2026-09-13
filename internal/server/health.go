package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"qbmcp/internal/config"
)

type Health struct {
	PID             int    `json:"pid"`
	Service         string `json:"service"`
	Version         string `json:"version"`
	UptimeSeconds   int64  `json:"uptime_seconds"`
	Address         string `json:"address"`
	PageConnected   bool   `json:"page_connected"`
	Ready           bool   `json:"ready"`
	ToolCount       int    `json:"tool_count"`
	MCPSessions     int    `json:"mcp_sessions"`
	PendingRequests int    `json:"pending_requests"`
}

func (a *App) Health() Health {
	state := a.bridge.Snapshot()
	h := Health{Service: "running", Version: config.Version, UptimeSeconds: int64(time.Since(a.started).Seconds()), Address: fmt.Sprintf("127.0.0.1:%d", a.config.Port), PageConnected: state.PageConnected, Ready: state.Ready, ToolCount: state.ToolCount, PendingRequests: state.PendingRequests}
	h.PID = os.Getpid()
	for range a.mcp.Sessions() {
		h.MCPSessions++
	}
	return h
}

func (a *App) Close() {
	a.bridge.Close()
	for session := range a.mcp.Sessions() {
		_ = session.Close()
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
