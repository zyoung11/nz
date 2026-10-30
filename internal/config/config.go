package config

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
)

const (
	DefaultPort    = 4399
	DefaultSubnet  = "192.168.200.0/24"
	ConfigFileName = "config.json"
	KeyFileName    = "n0.key"
	StateFileName  = "state.json"
	LogFileName    = "n0.log"
	TunName        = "n0"
)

type Account struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type Config struct {
	Mode     string  `json:"mode"`
	Port     int     `json:"port"`
	Subnet   string  `json:"subnet"`
	Admin    Account `json:"admin"`
	User     string  `json:"user"`
	Password string  `json:"password"`
	Name     string  `json:"name"`
	Domain   string  `json:"domain"`
}

// DefaultPath returns the config path next to the running binary.
func DefaultPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ConfigFileName
	}
	return filepath.Join(filepath.Dir(exe), ConfigFileName)
}

// Load reads and parses the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes config content and fills defaults.
func Parse(data []byte) (*Config, error) {
	cfg := &Config{Port: DefaultPort, Subnet: DefaultSubnet}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	return cfg, nil
}

// ValidateFile loads the config at path and validates it without side effects.
func ValidateFile(path string) error {
	cfg, err := Load(path)
	if err != nil {
		return err
	}
	return cfg.Validate()
}

// Validate checks every field and reports the first problem found.
func (c *Config) Validate() error {
	if c.Mode != "server" && c.Mode != "node" {
		return fmt.Errorf("field mode must be \"server\" or \"node\", got %q", c.Mode)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("field port must be 1 to 65535, got %d", c.Port)
	}
	if c.Mode == "server" {
		return c.validateServer()
	}
	return c.validateNode()
}

func (c *Config) validateServer() error {
	if c.User != "" {
		return fmt.Errorf("field user is only valid in node mode")
	}
	if c.Password != "" {
		return fmt.Errorf("field password is only valid in node mode")
	}
	if c.Name != "" {
		return fmt.Errorf("field name is only valid in node mode")
	}
	if c.Domain != "" {
		return fmt.Errorf("field domain is only valid in node mode")
	}
	if _, err := netip.ParsePrefix(c.Subnet); err != nil {
		return fmt.Errorf("field subnet must be a CIDR prefix, got %q", c.Subnet)
	}
	if c.Admin.Name == "" {
		return fmt.Errorf("field admin.name is required in server mode")
	}
	if c.Admin.Password == "" {
		return fmt.Errorf("field admin.password is required in server mode")
	}
	return nil
}

func (c *Config) validateNode() error {
	if c.Subnet != DefaultSubnet {
		return fmt.Errorf("field subnet is only valid in server mode")
	}
	if c.Admin.Name != "" || c.Admin.Password != "" {
		return fmt.Errorf("field admin is only valid in server mode")
	}
	if c.User == "" {
		return fmt.Errorf("field user is required in node mode")
	}
	if c.Password == "" {
		return fmt.Errorf("field password is required in node mode")
	}
	if c.Name == "" {
		return fmt.Errorf("field name is required in node mode")
	}
	if c.Domain == "" {
		return fmt.Errorf("field domain is required in node mode")
	}
	return nil
}
