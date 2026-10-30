package state

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
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
	DesiredIP   int           `json:"desired_ip,omitempty"`
	Public      string        `json:"public"`
	Fingerprint string        `json:"fingerprint"`
	VPNIP       string        `json:"vpn_ip"`
	Status      MachineStatus `json:"status"`
	Trusted     bool          `json:"trusted"`
	LastSeen    time.Time     `json:"last_seen"`
	JoinedAt    time.Time     `json:"joined_at"`
}

// IdentityData holds the machine key material.
type IdentityData struct {
	SignPublic  string `json:"sign_public"`
	SignPrivate string `json:"sign_private"`
	ExchPublic  string `json:"exch_public"`
	ExchPrivate string `json:"exch_private"`
}

// ServerAuth holds the cached server public keys.
type ServerAuth struct {
	SignPub []byte `json:"sign_pub,omitempty"`
	ExchPub []byte `json:"exch_pub,omitempty"`
}

// MachineView is a machine marked for the querying machine itself.
type MachineView struct {
	Machine
	Self bool `json:"self,omitempty"`
}

type State struct {
	mu         sync.Mutex
	path       string
	Identity   *IdentityData `json:"identity,omitempty"`
	ServerAuth *ServerAuth   `json:"server_auth,omitempty"`
	Users      []User        `json:"users"`
	Machines   []Machine     `json:"machines"`
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

// Unblock restores a blocked user or machine to active. Pending
// entries are left untouched.
func (s *State) Unblock(user, machine string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if machine == "" {
		u := s.findUserLocked(user)
		if u == nil {
			return fmt.Errorf("user %s not found", user)
		}
		if u.Status == UserBlocked {
			u.Status = UserActive
		}
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
	if m.Status == MachineBlocked {
		m.Status = MachineActive
	}
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

// GetUser returns a copy of the named user.
func (s *State) GetUser(name string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUserLocked(name)
	if u == nil {
		return User{}, false
	}
	return *u, true
}

// GetMachine returns a copy of the named machine.
func (s *State) GetMachine(user, machine string) (Machine, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.findMachineLocked(user, machine)
	if m == nil {
		return Machine{}, false
	}
	return *m, true
}

// FindMachineByPublic returns the machine holding the public key.
func (s *State) FindMachineByPublic(public string) (Machine, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Machines {
		if s.Machines[i].Public == public {
			return s.Machines[i], true
		}
	}
	return Machine{}, false
}

// FindMachineByIP returns the machine bound to the VPN address.
func (s *State) FindMachineByIP(ip string) (Machine, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Machines {
		if s.Machines[i].VPNIP == ip {
			return s.Machines[i], true
		}
	}
	return Machine{}, false
}

// ActiveMachines returns a copy of all active machines.
func (s *State) ActiveMachines() []Machine {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Machine, 0, len(s.Machines))
	for _, m := range s.Machines {
		if m.Status == MachineActive {
			out = append(out, m)
		}
	}
	return out
}

// Touch records machine activity for the last seen column.
func (s *State) Touch(user, machine string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.findMachineLocked(user, machine); m != nil {
		m.LastSeen = time.Now()
	}
}

// RegisterJoin records a join attempt and returns the resulting status.
// It returns "pending" for new users and new machines, the current status
// for known machines and "rejected" for blocked or duplicated entries.
func (s *State) RegisterJoin(user, machine, public, fingerprint string, salt, verifier []byte, desired int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUserLocked(user)
	if u == nil {
		if len(salt) == 0 || len(verifier) == 0 {
			return "", fmt.Errorf("new user %s must send salt and verifier", user)
		}
		s.Users = append(s.Users, User{
			Name: user, Salt: base64.StdEncoding.EncodeToString(salt),
			Verifier: base64.StdEncoding.EncodeToString(verifier),
			Status:   UserPending, JoinedAt: time.Now(),
		})
		s.Machines = append(s.Machines, Machine{
			User: user, Name: machine, Public: public, Fingerprint: fingerprint,
			DesiredIP: desired, Status: MachinePending, JoinedAt: time.Now(),
		})
		return string(MachinePending), s.saveLocked()
	}
	if u.Status == UserBlocked {
		return string(MachineBlocked), nil
	}
	for i := range s.Machines {
		m := &s.Machines[i]
		if m.Public != public {
			continue
		}
		if m.Status == MachineBlocked {
			return string(MachineBlocked), nil
		}
		if m.Name != machine {
			if s.machineNameUsedLocked(user, machine, public) {
				return "", fmt.Errorf("machine name %s/%s already used by another key", user, machine)
			}
			m.Name = machine
		}
		if desired != 0 {
			m.DesiredIP = desired
		}
		m.LastSeen = time.Now()
		return string(m.Status), s.saveLocked()
	}
	if s.machineNameUsedLocked(user, machine, public) {
		return "", fmt.Errorf("machine name %s/%s already used by another key", user, machine)
	}
	s.Machines = append(s.Machines, Machine{
		User: user, Name: machine, Public: public, Fingerprint: fingerprint,
		DesiredIP: desired, Status: MachinePending, JoinedAt: time.Now(),
	})
	return string(MachinePending), s.saveLocked()
}

func (s *State) machineNameUsedLocked(user, machine, public string) bool {
	for _, m := range s.Machines {
		if m.User == user && m.Name == machine && m.Public != public {
			return true
		}
	}
	return false
}

// SetVerifier stores the password verifier for an existing user.
func (s *State) SetVerifier(name string, salt, verifier []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUserLocked(name)
	if u == nil {
		return fmt.Errorf("user %s not found", name)
	}
	u.Salt = base64.StdEncoding.EncodeToString(salt)
	u.Verifier = base64.StdEncoding.EncodeToString(verifier)
	return s.saveLocked()
}

// SetMachineIP binds the allocated IP to the machine.
func (s *State) SetMachineIP(user, machine, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.findMachineLocked(user, machine)
	if m == nil {
		return fmt.Errorf("machine %s/%s not found", user, machine)
	}
	m.VPNIP = ip
	return s.saveLocked()
}

// AllocateIP returns the desired host address when free, otherwise the
// lowest free address in subnet from .2 up. Desired is a host number
// from 2 to 254, or 0 for automatic allocation.
func (s *State) AllocateIP(subnet string, desired int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		return "", fmt.Errorf("bad subnet %q: %w", subnet, err)
	}
	used := make(map[string]string, len(s.Machines))
	for _, m := range s.Machines {
		if m.VPNIP != "" {
			used[m.VPNIP] = m.User + "/" + m.Name
		}
	}
	serverIP := prefix.Addr().Next()
	if desired != 0 {
		if desired < 2 || desired > 254 {
			return "", fmt.Errorf("desired ip %d out of range 2 to 254", desired)
		}
		base := prefix.Addr().As4()
		addr := netip.AddrFrom4([4]byte{base[0], base[1], base[2], byte(desired)})
		if !prefix.Contains(addr) || addr == serverIP {
			return "", fmt.Errorf("desired ip %v is not usable in %s", addr, subnet)
		}
		if owner, taken := used[addr.String()]; taken {
			return "", fmt.Errorf("ip %s already used by %s", addr, owner)
		}
		return addr.String(), nil
	}
	for addr := serverIP.Next(); prefix.Contains(addr); addr = addr.Next() {
		if addr == serverIP || used[addr.String()] != "" {
			continue
		}
		return addr.String(), nil
	}
	return "", fmt.Errorf("subnet %s has no free address", subnet)
}

// EnsureAdmin keeps the admin user present and active. The salt and
// verifier are stored only when the admin user is created, so admin
// machines can register with password proof.
func (s *State) EnsureAdmin(name string, salt, verifier []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.findUserLocked(name)
	if u == nil {
		s.Users = append(s.Users, User{
			Name:     name,
			Salt:     base64.StdEncoding.EncodeToString(salt),
			Verifier: base64.StdEncoding.EncodeToString(verifier),
			Status:   UserActive, JoinedAt: time.Now(),
		})
		return s.saveLocked()
	}
	if u.Status != UserActive {
		u.Status = UserActive
		return s.saveLocked()
	}
	return nil
}
