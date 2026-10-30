package state

import (
	"path/filepath"
	"testing"
)

func TestRegisterJoinKeepsDesiredIP(t *testing.T) {
	st, err := Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RegisterJoin("u", "m", "pub", "fp", []byte("s"), []byte("v"), 7); err != nil {
		t.Fatal(err)
	}
	m, ok := st.GetMachine("u", "m")
	if !ok || m.DesiredIP != 7 {
		t.Fatalf("desired = %d, want 7", m.DesiredIP)
	}
}
