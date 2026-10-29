package config

import "testing"

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`{"mode":"server","admin":{"name":"me","password":"pw"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("port default = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.Subnet != DefaultSubnet {
		t.Errorf("subnet default = %q, want %q", cfg.Subnet, DefaultSubnet)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"bad mode", `{"mode":"client","admin":{"name":"me","password":"pw"}}`},
		{"empty mode", `{"admin":{"name":"me","password":"pw"}}`},
		{"port zero", `{"mode":"server","port":0,"admin":{"name":"me","password":"pw"}}`},
		{"port too big", `{"mode":"server","port":70000,"admin":{"name":"me","password":"pw"}}`},
		{"server without admin", `{"mode":"server"}`},
		{"server without admin password", `{"mode":"server","admin":{"name":"me"}}`},
		{"server with user", `{"mode":"server","admin":{"name":"me","password":"pw"},"user":"x"}`},
		{"server with domain", `{"mode":"server","admin":{"name":"me","password":"pw"},"domain":"h"}`},
		{"bad subnet", `{"mode":"server","subnet":"10.0.0.1","admin":{"name":"me","password":"pw"}}`},
		{"node without user", `{"mode":"node","password":"pw","name":"laptop","domain":"h"}`},
		{"node without password", `{"mode":"node","user":"u","name":"laptop","domain":"h"}`},
		{"node without name", `{"mode":"node","user":"u","password":"pw","domain":"h"}`},
		{"node without domain", `{"mode":"node","user":"u","password":"pw","name":"laptop"}`},
		{"node with admin", `{"mode":"node","user":"u","password":"pw","name":"laptop","domain":"h","admin":{"name":"me","password":"pw"}}`},
		{"node with subnet", `{"mode":"node","user":"u","password":"pw","name":"laptop","domain":"h","subnet":"10.0.0.0/24"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse([]byte(tc.json))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if err := cfg.Validate(); err == nil {
				t.Errorf("validate accepted %s", tc.json)
			}
		})
	}
}

func TestValidateNodeOK(t *testing.T) {
	cfg, err := Parse([]byte(`{"mode":"node","user":"u","password":"pw","name":"laptop","domain":"h"}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}
