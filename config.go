package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const defaultConfigName = "config.json"

type config struct {
	Mode      string `json:"mode"`
	Password  string `json:"password"`
	Name      string `json:"name"`
	Domain    string `json:"domain"`
	DesiredIP int    `json:"ip"`
	Route     string `json:"route"`
	Port      int    `json:"port"`
	TUN       string `json:"tun"`
}

func defaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return defaultConfigName
	}
	return filepath.Join(filepath.Dir(exe), defaultConfigName)
}

func loadConfig(path string) (*config, error) {
	if path == "" {
		path = defaultConfigPath()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}
	cfg := &config{Port: defaultPort, TUN: "nz0", Route: "auto"}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("password 未设置")
	}
	if cfg.Mode != "server" && cfg.Mode != "node" {
		return nil, fmt.Errorf("mode must be \"server\" or \"node\"")
	}
	if cfg.Mode == "server" {
		if cfg.Domain != "" {
			logWarn("domain ignored in server mode")
		}
		if cfg.Route != "" && cfg.Route != "auto" {
			logWarn("route ignored in server mode")
		}
		cfg.Domain = ""
		cfg.Route = "auto"
	}
	if cfg.Mode == "node" {
		if cfg.Domain == "" {
			return nil, fmt.Errorf("domain required for node mode")
		}
		if cfg.Name == "" {
			return nil, fmt.Errorf("name required for node mode")
		}
	}
	if cfg.Port == 0 {
		cfg.Port = defaultPort
	}
	if cfg.Route == "" {
		cfg.Route = "auto"
	}
	return cfg, nil
}
