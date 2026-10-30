package demostore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretA, secretB = "aaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbb"

func writeKeys(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "keys")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadKeysAndLookup(t *testing.T) {
	k, err := LoadKeys(writeKeys(t, "# who may upload\n\ntravis "+secretA+"\n  sandy\t"+secretB+"  \n"), "")
	if err != nil {
		t.Fatal(err)
	}
	if k.Len() != 2 {
		t.Fatalf("%d keys, want 2", k.Len())
	}
	for secret, want := range map[string]string{secretA: "travis", secretB: "sandy"} {
		if got, ok := k.Lookup(secret); !ok || got != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", secret, got, ok, want)
		}
	}
	for _, bad := range []string{"", "nope", secretA + "x", secretA[:len(secretA)-1], strings.ToUpper(secretA)} {
		if label, ok := k.Lookup(bad); ok {
			t.Errorf("Lookup(%q) accepted as %q", bad, label)
		}
	}
}

func TestLegacyKeyIsDefault(t *testing.T) {
	k, err := LoadKeys("", secretA)
	if err != nil {
		t.Fatal(err)
	}
	if label, ok := k.Lookup(secretA); !ok || label != "default" {
		t.Errorf("the single shared key = %q, %v; want default", label, ok)
	}
	if none, _ := LoadKeys("", ""); none.Len() != 0 {
		t.Errorf("no keys configured, got %d", none.Len())
	}
}

func TestLoadKeysRejects(t *testing.T) {
	for name, body := range map[string]string{
		"a key that is too short": "travis short\n",
		"a bad label":             "tra vis " + secretA + "\n",
		"a label with a slash":    "../x " + secretA + "\n",
		"a line with one field":   "travis\n",
		"a label twice":           "travis " + secretA + "\ntravis " + secretB + "\n",
		"one key for two people":  "travis " + secretA + "\nsandy " + secretA + "\n",
	} {
		if _, err := LoadKeys(writeKeys(t, body), ""); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := LoadKeys(filepath.Join(t.TempDir(), "missing"), ""); err == nil {
		t.Error("a missing keys file was accepted")
	}
	// the legacy key must also be long enough and not collide
	if _, err := LoadKeys(writeKeys(t, "default "+secretA+"\n"), secretB); err == nil {
		t.Error("a file label that collides with the legacy key's label was accepted")
	}
}
