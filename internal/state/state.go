package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type UserStatus string

const (
	UserPending UserStatus = "pending"
	UserActive  UserStatus = "active"
	UserBlocked UserStatus = "blocked"
)

type MachineStatus string

const (
	MachinePending MachineStatus = "pending"
	MachineActive  MachineStatus = "active"
	MachineBlocked MachineStatus = "blocked"
)

type User struct {
	Name     string     `json:"name"`
	Salt     string     `json:"salt"`
	Verifier string     `json:"verifier"`
	Status   UserStatus `json:"status"`
	JoinedAt time.Time  `json:"joined_at"`
}

type Machine struct {
	User        string        `json:"user"`
	Name        string        `json:"name"`
	Public      string        `json:"public"`
	Fingerprint string        `json:"fingerprint"`
	VPNIP       string        `json:"vpn_ip"`
	Status      MachineStatus `json:"status"`
	Trusted     bool          `json:"trusted"`
	LastSeen    time.Time     `json:"last_seen"`
	JoinedAt    time.Time     `json:"joined_at"`
}

type State struct {
	mu       sync.Mutex
	path     string
	Users    []User    `json:"users"`
	Machines []Machine `json:"machines"`
}

// Load reads the state file at path, returning an empty state if absent.
func Load(path string) (*State, error) {
	s := &State{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("failed to read state file %s: %w", path, err)
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("failed to parse state file %s: %w", path, err)
	}
	return s, nil
}

// Save writes the state file atomically.
func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("failed to write state file %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("failed to replace state file %s: %w", s.path, err)
	}
	return nil
}
