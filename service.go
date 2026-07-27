package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func serviceConfigPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(exe), defaultConfigName), nil
}

func serviceArgs() ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cfgPath, err := serviceConfigPath()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(cfgPath); err != nil {
		return nil, fmt.Errorf("config %s not found", cfgPath)
	}
	return []string{exe, "run", "--config", cfgPath}, nil
}
