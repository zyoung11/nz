package state

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

// ParseTarget splits "friend" or "friend/laptop" into user and machine.
// Machine is empty for a user target.
func ParseTarget(target string) (string, string, error) {
	if target == "" {
		return "", "", fmt.Errorf("target is empty")
	}
	user, machine, _ := strings.Cut(target, "/")
	if user == "" {
		return "", "", fmt.Errorf("target %q has no user part", target)
	}
	return user, machine, nil
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
	return s.saveLocked()
}

func (s *State) saveLocked() error {
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

// Snapshot returns a copy of all users and machines.
func (s *State) Snapshot() ([]User, []Machine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]User(nil), s.Users...), append([]Machine(nil), s.Machines...)
}

// Allow approves a pending user or machine.
func (s *State) Allow(user, machine string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine == "" {
		u := s.findUserLocked(user)
		if u == nil {
			return fmt.Errorf("user %s not found", user)
		}
		if u.Status != UserPending {
			return fmt.Errorf("user %s is %s, not pending", user, u.Status)
		}
		u.Status = UserActive
		return s.saveLocked()
	}
	m := s.findMachineLocked(user, machine)
	if m == nil {
		return fmt.Errorf("machine %s/%s not found", user, machine)
	}
	if m.Status != MachinePending {
		return fmt.Errorf("machine %s/%s is %s, not pending", user, machine, m.Status)
	}
	u := s.findUserLocked(user)
	if u == nil || u.Status != UserActive {
		return fmt.Errorf("user %s is not active, allow the user first", user)
	}
	m.Status = MachineActive
	return s.saveLocked()
}

// Deny removes a pending user or machine.
func (s *State) Deny(user, machine string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine == "" {
		if s.findUserLocked(user) == nil {
			return fmt.Errorf("user %s not found", user)
		}
		s.Users = removeUser(s.Users, user)
		s.Machines = removeUserMachines(s.Machines, user)
		return s.saveLocked()
	}
	if s.findMachineLocked(user, machine) == nil {
		return fmt.Errorf("machine %s/%s not found", user, machine)
	}
	s.Machines = removeMachine(s.Machines, user, machine)
	return s.saveLocked()
}

// Block disables a user with all machines, or a single machine.
func (s *State) Block(user, machine string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine == "" {
		u := s.findUserLocked(user)
		if u == nil {
			return fmt.Errorf("user %s not found", user)
		}
		u.Status = UserBlocked
		for i := range s.Machines {
			if s.Machines[i].User == user {
				s.Machines[i].Status = MachineBlocked
			}
		}
		return s.saveLocked()
	}
	m := s.findMachineLocked(user, machine)
	if m == nil {
		return fmt.Errorf("machine %s/%s not found", user, machine)
	}
	m.Status = MachineBlocked
	return s.saveLocked()
}

// Unblock restores a user or machine to active.
func (s *State) Unblock(user, machine string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine == "" {
		u := s.findUserLocked(user)
		if u == nil {
			return fmt.Errorf("user %s not found", user)
		}
		u.Status = UserActive
		for i := range s.Machines {
			if s.Machines[i].User == user && s.Machines[i].Status == MachineBlocked {
				s.Machines[i].Status = MachineActive
			}
		}
		return s.saveLocked()
	}
	m := s.findMachineLocked(user, machine)
	if m == nil {
		return fmt.Errorf("machine %s/%s not found", user, machine)
	}
	m.Status = MachineActive
	return s.saveLocked()
}

// Trust allows a user or machine to reach admin machines.
func (s *State) Trust(user, machine string) error {
	return s.setTrust(user, machine, true)
}

// Untrust revokes trust from a user or machine.
func (s *State) Untrust(user, machine string) error {
	return s.setTrust(user, machine, false)
}

func (s *State) setTrust(user, machine string, trusted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine == "" {
		u := s.findUserLocked(user)
		if u == nil {
			return fmt.Errorf("user %s not found", user)
		}
		for i := range s.Machines {
			if s.Machines[i].User == user {
				s.Machines[i].Trusted = trusted
			}
		}
		return s.saveLocked()
	}
	m := s.findMachineLocked(user, machine)
	if m == nil {
		return fmt.Errorf("machine %s/%s not found", user, machine)
	}
	m.Trusted = trusted
	return s.saveLocked()
}

func (s *State) findUserLocked(name string) *User {
	for i := range s.Users {
		if s.Users[i].Name == name {
			return &s.Users[i]
		}
	}
	return nil
}

func (s *State) findMachineLocked(user, name string) *Machine {
	for i := range s.Machines {
		if s.Machines[i].User == user && s.Machines[i].Name == name {
			return &s.Machines[i]
		}
	}
	return nil
}

func removeUser(users []User, name string) []User {
	out := users[:0]
	for _, u := range users {
		if u.Name != name {
			out = append(out, u)
		}
	}
	return out
}

func removeUserMachines(machines []Machine, user string) []Machine {
	out := machines[:0]
	for _, m := range machines {
		if m.User != user {
			out = append(out, m)
		}
	}
	return out
}

func removeMachine(machines []Machine, user, name string) []Machine {
	out := machines[:0]
	for _, m := range machines {
		if !(m.User == user && m.Name == name) {
			out = append(out, m)
		}
	}
	return out
}
