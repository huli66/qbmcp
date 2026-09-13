//go:build windows

package tests

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"qbmcp/internal/config"
	"qbmcp/internal/host"
)

func TestStatusPathsAndPortWithoutProcess(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	s := host.StatusDetails()
	if s.Port != 32300 || s.PortSource != "default" || s.ConfigExists {
		t.Fatalf("initial: %+v", s)
	}
	c := config.Config{Port: 43210, AllowedOrigins: []string{}}
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}
	s = host.StatusDetails()
	if s.Port != c.Port || s.PortSource != "config" || s.MCPURL != "http://127.0.0.1:43210/mcp" {
		t.Fatalf("configured: %+v", s)
	}
	for _, asJSON := range []bool{false, true} {
		var b bytes.Buffer
		if err := host.WriteStatus(&b, s, asJSON); err != nil {
			t.Fatal(err)
		}
		for _, removed := range []string{"token", "demo_url", "凭据", "测试页面", "内置工具"} {
			if strings.Contains(b.String(), removed) {
				t.Fatalf("status still contains %q", removed)
			}
		}
		if asJSON {
			var roundtrip host.UserStatus
			if err := json.Unmarshal(b.Bytes(), &roundtrip); err != nil {
				t.Fatal(err)
			}
			if roundtrip.ConfigPath != config.Path() || roundtrip.Background != host.BackgroundPath() || roundtrip.LogPath == "" || roundtrip.RuntimePath == "" {
				t.Fatal("missing paths")
			}
		} else if !strings.Contains(b.String(), config.Path()) || !strings.Contains(b.String(), "43210") || !strings.Contains(b.String(), "未查询到健康状态") {
			t.Fatal("incomplete text status")
		}
	}
}

func TestStatusInvalidConfigDoesNotClaimDefaultPort(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	if err := os.WriteFile(config.Path(), []byte("invalid json"), 0600); err != nil {
		t.Fatal(err)
	}
	s := host.StatusDetails()
	if s.Port != 0 || s.MCPURL != "" || s.Error == "" || s.ConfigPath != config.Path() || s.PortSource != "unavailable" {
		t.Fatalf("invalid config: %+v", s)
	}
}
