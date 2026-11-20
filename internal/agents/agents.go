// Package agents loads the list of agents and teams from a JSON file and finds
// which agent a virtual key belongs to.
//
// Each agent gets its own virtual key (like "ab-K7Q..."). The file stores only
// the SHA-256 hash of each key, never the key itself, so the file can be
// committed or put in a Kubernetes ConfigMap without leaking secrets.
package agents

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
)

// KeyPrefix starts every virtual key, so a leaked key is easy to recognise.
const KeyPrefix = "ab-"

// Team is one team and its token budget.
type Team struct {
	Name   string `json:"name"`
	Budget int64  `json:"budget"`
}

// Agent is one AI agent: who it is, who owns it, and what it may spend.
type Agent struct {
	Name      string `json:"name"`
	Team      string `json:"team"`
	Owner     string `json:"owner"`
	Budget    *int64 `json:"budget,omitempty"` // nil = only the team budget applies
	KeySHA256 string `json:"key_sha256"`
}

// File is the JSON config file format.
type File struct {
	Teams  []Team  `json:"teams"`
	Agents []Agent `json:"agents"`
}

// Registry answers "which agent owns this key?". It never changes after it is
// built, so it is safe for concurrent use without a lock.
type Registry struct {
	teams  []Team
	agents []Agent
	byHash map[string]Agent
}

// Load reads and checks an agents file.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path comes from the operator's config, not from a request
	if err != nil {
		return nil, fmt.Errorf("read agents file: %w", err)
	}
	r, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("agents file %s: %w", path, err)
	}
	return r, nil
}

// Parse checks the JSON of an agents file and builds a Registry. Unknown
// fields are rejected, so a typo like "buget" is caught at start.
func Parse(data []byte) (*Registry, error) {
	var f File
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if err := f.validate(); err != nil {
		return nil, err
	}

	r := &Registry{
		teams:  append([]Team(nil), f.Teams...),
		agents: append([]Agent(nil), f.Agents...),
		byHash: make(map[string]Agent, len(f.Agents)),
	}
	for _, a := range f.Agents {
		r.byHash[a.KeySHA256] = a
	}
	return r, nil
}

// Lookup returns the agent that owns key. The key is hashed first and the
// hash is looked up; the plain key is never stored.
func (r *Registry) Lookup(key string) (Agent, bool) {
	a, ok := r.byHash[HashKey(key)]
	return a, ok
}

// Teams returns a copy of all teams.
func (r *Registry) Teams() []Team { return append([]Team(nil), r.teams...) }

// Agents returns a copy of all agents.
func (r *Registry) Agents() []Agent { return append([]Agent(nil), r.agents...) }

// HashKey returns the lowercase hex SHA-256 of a virtual key.
//
// A plain fast hash is fine here (unlike for human passwords) because keys are
// long random strings: there is no dictionary of likely keys to try.
func HashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// NewKey returns a new random virtual key with at least 128 bits of randomness.
func NewKey() string {
	return KeyPrefix + rand.Text()
}

// namePattern allows Kubernetes-style names: lowercase letters, digits, '-',
// '.' and '_', starting and ending with a letter or digit, at most 63 chars.
var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,61}[a-z0-9])?$`)

var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validate collects every problem in the file, so the operator can fix them
// all in one go.
func (f *File) validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	teams := make(map[string]bool, len(f.Teams))
	for i, t := range f.Teams {
		switch {
		case !namePattern.MatchString(t.Name):
			bad("teams[%d]: name %q must be lowercase letters, digits, '-', '.' or '_'", i, t.Name)
		case teams[t.Name]:
			bad("teams[%d]: team %q is listed twice", i, t.Name)
		}
		if t.Budget < 0 {
			bad("team %q: budget must be >= 0", t.Name)
		}
		teams[t.Name] = true
	}

	names := make(map[string]bool, len(f.Agents))
	hashes := make(map[string]bool, len(f.Agents))
	for i, a := range f.Agents {
		switch {
		case !namePattern.MatchString(a.Name):
			bad("agents[%d]: name %q must be lowercase letters, digits, '-', '.' or '_'", i, a.Name)
		case names[a.Name]:
			bad("agents[%d]: agent %q is listed twice", i, a.Name)
		}
		names[a.Name] = true

		if !teams[a.Team] {
			bad("agent %q: team %q is not in the teams list", a.Name, a.Team)
		}
		if a.Owner == "" {
			bad("agent %q: owner is required — every agent needs someone responsible for it", a.Name)
		}
		if a.Budget != nil && *a.Budget < 0 {
			bad("agent %q: budget must be >= 0", a.Name)
		}
		switch {
		case !hashPattern.MatchString(a.KeySHA256):
			bad("agent %q: key_sha256 must be 64 lowercase hex characters (run: proxy keygen)", a.Name)
		case hashes[a.KeySHA256]:
			bad("agent %q: key_sha256 is already used by another agent", a.Name)
		}
		hashes[a.KeySHA256] = true
	}
	return errors.Join(errs...)
}
