package tests

import (
	"encoding/json"
	"os"
	"testing"

	"qbmcp/internal/config"
)

func TestConfigRoundTripKeepsSupportedSettings(t *testing.T) {
	t.Setenv("QBMCP_HOME", t.TempDir())
	legacy := []byte(`{"port":34567,"allowed_origins":["http://localhost:5173"],"token":"obsolete"}`)
	if err := os.WriteFile(config.Path(), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 34567 || len(c.AllowedOrigins) != 1 || c.AllowedOrigins[0] != "http://localhost:5173" {
		t.Fatalf("supported settings changed: %+v", c)
	}
	if err := config.Save(c); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved["port"] != float64(c.Port) || saved["allowed_origins"] == nil {
		t.Fatalf("unexpected saved settings: %s", raw)
	}
	if _, exists := saved["token"]; exists {
		t.Fatal("obsolete setting survived save")
	}
}

func TestConfigValidation(t *testing.T) {
	c := config.Config{Port: 32300, AllowedOrigins: []string{"http://localhost:3000"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"*", "null", "https://example.com", "http://localhost:3000/path", "http://user@localhost:3000"} {
		c.AllowedOrigins = []string{origin}
		if c.Validate() == nil {
			t.Fatalf("accepted %q", origin)
		}
	}
}
