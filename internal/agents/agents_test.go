package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Known SHA-256 test vector: sha256("abc").
const abcHash = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

func TestHashKey(t *testing.T) {
	if got := HashKey("abc"); got != abcHash {
		t.Errorf("HashKey(abc) = %s, want %s", got, abcHash)
	}
}

func TestNewKey(t *testing.T) {
	a, b := NewKey(), NewKey()
	if !strings.HasPrefix(a, KeyPrefix) {
		t.Errorf("key %q has no %q prefix", a, KeyPrefix)
	}
	if len(a) < len(KeyPrefix)+26 {
		t.Errorf("key %q is too short", a)
	}
	if a == b {
		t.Error("two new keys are equal")
	}
}

func validFile(agents string) string {
	return `{"teams":[{"name":"pay","budget":100},{"name":"search","budget":0}],"agents":[` + agents + `]}`
}

func agentJSON(name, team, owner, hash string) string {
	return `{"name":"` + name + `","team":"` + team + `","owner":"` + owner + `","key_sha256":"` + hash + `"}`
}

func TestParse(t *testing.T) {
	h1, h2 := HashKey("k1"), HashKey("k2")
	tests := []struct {
		name    string
		json    string
		wantErr string // "" = valid; otherwise part of the error message
	}{
		{name: "valid", json: validFile(agentJSON("bot", "pay", "lead", h1) + "," + agentJSON("finder", "search", "team-lead", h2))},
		{name: "agent with own budget", json: validFile(`{"name":"bot","team":"pay","owner":"o","budget":10,"key_sha256":"` + h1 + `"}`)},
		{name: "no agents", json: validFile("")},
		{name: "not JSON", json: `{`, wantErr: "decode JSON"},
		{name: "typo in field", json: `{"teams":[{"name":"pay","buget":5}],"agents":[]}`, wantErr: "unknown field"},
		{name: "bad team name", json: `{"teams":[{"name":"Pay Team","budget":1}],"agents":[]}`, wantErr: "lowercase"},
		{name: "duplicate team", json: `{"teams":[{"name":"a","budget":1},{"name":"a","budget":2}],"agents":[]}`, wantErr: "listed twice"},
		{name: "negative team budget", json: `{"teams":[{"name":"a","budget":-1}],"agents":[]}`, wantErr: "budget must be >= 0"},
		{name: "unknown team", json: validFile(agentJSON("bot", "ghost", "o", h1)), wantErr: "not in the teams list"},
		{name: "missing owner", json: validFile(agentJSON("bot", "pay", "", h1)), wantErr: "owner is required"},
		{name: "bad hash", json: validFile(agentJSON("bot", "pay", "o", "abc")), wantErr: "64 lowercase hex"},
		{name: "uppercase hash", json: validFile(agentJSON("bot", "pay", "o", strings.ToUpper(h1))), wantErr: "64 lowercase hex"},
		{name: "shared key", json: validFile(agentJSON("a", "pay", "o", h1) + "," + agentJSON("b", "pay", "o", h1)), wantErr: "already used"},
		{name: "duplicate agent", json: validFile(agentJSON("a", "pay", "o", h1) + "," + agentJSON("a", "pay", "o", h2)), wantErr: "listed twice"},
		{name: "negative agent budget", json: validFile(`{"name":"bot","team":"pay","owner":"o","budget":-5,"key_sha256":"` + h1 + `"}`), wantErr: "budget must be >= 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.json))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseReportsAllProblems(t *testing.T) {
	_, err := Parse([]byte(validFile(agentJSON("bot", "ghost", "", "nope"))))
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"not in the teams list", "owner is required", "64 lowercase hex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %q:\n%v", want, err)
		}
	}
}

func TestLookup(t *testing.T) {
	r, err := Parse([]byte(validFile(agentJSON("bot", "pay", "lead", HashKey("ab-secret")))))
	if err != nil {
		t.Fatal(err)
	}
	a, ok := r.Lookup("ab-secret")
	if !ok || a.Name != "bot" || a.Team != "pay" || a.Owner != "lead" {
		t.Errorf("Lookup(right key) = %+v, %v", a, ok)
	}
	for _, wrong := range []string{"", "ab-wrong", HashKey("ab-secret")} { // the hash itself is not a key
		if _, ok := r.Lookup(wrong); ok {
			t.Errorf("Lookup(%q) should fail", wrong)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(path, []byte(validFile("")), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("Load of a missing file should fail")
	}
}

func TestExampleFileIsValid(t *testing.T) {
	r, err := Load("../../examples/agents.json")
	if err != nil {
		t.Fatalf("examples/agents.json is broken: %v", err)
	}
	// The demo keys printed in the docs must match the example file.
	for key, agent := range map[string]string{
		"ab-demo-invoice-bot": "invoice-bot",
		"ab-demo-refund-bot":  "refund-bot",
		"ab-demo-search-bot":  "search-bot",
	} {
		if a, ok := r.Lookup(key); !ok || a.Name != agent {
			t.Errorf("demo key %s should belong to %s", key, agent)
		}
	}
}
