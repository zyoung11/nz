//go:build !windows

package service

import (
	"strings"
	"testing"
)

func TestUnitText(t *testing.T) {
	unit := unitText("/usr/local/bin/n0", "/etc/n0/config.json")
	for _, want := range []string{
		"ExecStart=/usr/local/bin/n0 run --config /etc/n0/config.json",
		"Restart=always",
		"RestartSec=5",
		"After=network-online.target",
		"Wants=network-online.target",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit missing %q", want)
		}
	}
}
