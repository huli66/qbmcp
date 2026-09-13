//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestStatusPathsAndPortWithoutProcess(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	s := statusDetails()
	if s.Port != 32300 || s.PortSource != "default" || s.ConfigExists {
		t.Fatalf("initial: %+v", s)
	}
	c := Config{Port: 43210, Token: randomID(), AllowedOrigins: []string{}}
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	s = statusDetails()
	if s.Port != c.Port || s.PortSource != "config" || s.MCPURL != "http://127.0.0.1:43210/mcp" {
		t.Fatalf("configured: %+v", s)
	}
	for _, asJSON := range []bool{false, true} {
		var b bytes.Buffer
		if err := writeUserStatus(&b, s, asJSON); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(b.String(), c.Token) {
			t.Fatal("status leaked token")
		}
		if asJSON {
			var roundtrip UserStatus
			if err := json.Unmarshal(b.Bytes(), &roundtrip); err != nil {
				t.Fatal(err)
			}
			if roundtrip.ConfigPath != configPath() || roundtrip.Background != backgroundPath() || roundtrip.LogPath == "" || roundtrip.RuntimePath == "" {
				t.Fatal("missing paths")
			}
		} else if !strings.Contains(b.String(), configPath()) || !strings.Contains(b.String(), "43210") || !strings.Contains(b.String(), "未查询到健康状态") {
			t.Fatal("incomplete text status")
		}
	}
}

func TestStatusInvalidConfigDoesNotClaimDefaultPort(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	if err := os.WriteFile(configPath(), []byte("invalid json"), 0600); err != nil {
		t.Fatal(err)
	}
	s := statusDetails()
	if s.Port != 0 || s.MCPURL != "" || s.Error == "" || s.ConfigPath != configPath() || s.PortSource != "unavailable" {
		t.Fatalf("invalid config: %+v", s)
	}
}
