package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"qbmcp/internal/fileutil"
)

const Version = "0.3.1"
const DefaultPort = 32300

type Config struct {
	Port           int      `json:"port"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func DataDir() string {
	if root := os.Getenv("QBMCP_HOME"); root != "" {
		absolute, err := filepath.Abs(root)
		if err == nil {
			return absolute
		}
	}
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		root, _ = os.UserConfigDir()
	}
	return filepath.Join(root, "qbmcp")
}

func InstallDir() string {
	if os.Getenv("QBMCP_HOME") != "" {
		return filepath.Join(DataDir(), "bin")
	}
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "qbmcp")
}

func Path() string { return filepath.Join(DataDir(), "config.json") }

func Load() (Config, error) {
	var c Config
	b, err := os.ReadFile(Path())
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("配置格式错误: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("端口必须在 1..65535 之间")
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
			return fmt.Errorf("仅允许明确的本地 HTTP Origin: %q", origin)
		}
	}
	return nil
}

// Save validates and atomically replaces the supported configuration fields.
func Save(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return fileutil.WriteJSON(Path(), c)
}
