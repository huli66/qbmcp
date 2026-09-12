package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

const version = "0.1.0"

type Config struct {
	Port           int      `json:"port"`
	Token          string   `json:"token"`
	AllowedOrigins []string `json:"allowed_origins"`
}

func dataDir() string {
	root := os.Getenv("ProgramData")
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, "qbmcp")
}

func configPath() string { return filepath.Join(dataDir(), "config.json") }

func loadConfig() (Config, error) {
	var c Config
	b, err := os.ReadFile(configPath())
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("配置格式错误: %w", err)
	}
	return c, c.validate()
}

func (c Config) validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("端口必须在 1..65535 之间")
	}
	b, err := hex.DecodeString(c.Token)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("token 必须为 32 字节随机值的十六进制表示")
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
			return fmt.Errorf("仅允许明确的本地 HTTP Origin: %q", origin)
		}
	}
	return nil
}

func randomID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func saveConfig(c Config) error {
	if err := c.validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dataDir(), "config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, configPath())
}
