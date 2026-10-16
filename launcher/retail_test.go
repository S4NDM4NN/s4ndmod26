package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The browser importer (wasm/shell.html) and the launcher must accept exactly the same official files.
func TestRetailTableMatchesBrowser(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "wasm", "shell.html"))
	if err != nil {
		t.Skip("wasm/shell.html not available")
	}
	re := regexp.MustCompile(`"([a-z0-9_]+\.pk3)": \[(\d+), "([0-9a-f]{64})"\]`)
	web := map[string]retailPak{}
	for _, m := range re.FindAllStringSubmatch(string(b), -1) {
		n, _ := strconv.ParseInt(m[2], 10, 64)
		web[m[1]] = retailPak{m[1], n, m[3]}
	}
	if len(web) != len(retailPaks) {
		t.Fatalf("browser lists %d paks, launcher %d", len(web), len(retailPaks))
	}
	for _, p := range retailPaks {
		if web[p.Name] != p {
			t.Errorf("%s differs: browser %+v launcher %+v", p.Name, web[p.Name], p)
		}
	}
}

// useFakeRetail swaps the table for tiny files and returns a source folder holding them.
func useFakeRetail(t *testing.T) (src string) {
	old, oldReq := retailPaks, retailRequired
	t.Cleanup(func() { retailPaks, retailRequired = old, oldReq })
	src = t.TempDir()
	retailPaks, retailRequired = nil, []string{"pak0.pk3", "mp_pak0.pk3"}
	for _, n := range []string{"pak0.pk3", "mp_pak0.pk3", "mp_pakmaps0.pk3", "mp_pakmaps1.pk3"} {
		content := "content of " + n
		sum := sha256.Sum256([]byte(content))
		retailPaks = append(retailPaks, retailPak{n, int64(len(content)), hex.EncodeToString(sum[:])})
		// the real files are in a folder called Main, with any capitalisation
		writeTestFile(t, filepath.Join(src, "Main", strings.ToUpper(n[:1])+n[1:]), content)
	}
	return src
}

func writeTestFile(t *testing.T, p, c string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRetailImport(t *testing.T) {
	src := useFakeRetail(t)
	inst := t.TempDir()
	writeTestFile(t, filepath.Join(inst, "main", "demopak0.pk3"), "demo")

	// a corrupt optional pack is skipped, not copied
	writeTestFile(t, filepath.Join(src, "Main", "Mp_pakmaps1.pk3"), "tampered!!!!!!!!!")
	pl, err := planRetailImport(src, nil) // pointing at the game folder, not Main
	if err != nil {
		t.Fatal(err)
	}
	if len(pl.Files) != 3 || len(pl.Skipped) != 1 || pl.Skipped[0] != "mp_pakmaps1.pk3" {
		t.Fatalf("plan = %+v", pl)
	}
	if retailInstalled(inst) {
		t.Fatal("nothing imported yet")
	}
	if err := copyRetail(pl, inst, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !retailInstalled(inst) {
		t.Fatal("retail not detected after import")
	}
	if _, err := os.Stat(filepath.Join(inst, "main", "demopak0.pk3")); err == nil {
		t.Error("demopak0 should be removed")
	}
	if _, err := os.Stat(filepath.Join(inst, "main", "mp_pakmaps1.pk3")); err == nil {
		t.Error("skipped pack was copied")
	}

	// the manifest of a retail install drops the demo pak and prunes it
	m := &Manifest{Files: []ManifestFile{{Path: "main/demopak0.pk3"}, {Path: "s4ndmod26/x.pk3"}}}
	applyEdition(m, inst)
	if len(m.Files) != 1 || m.Files[0].Path != "s4ndmod26/x.pk3" || len(m.Prune) != 1 || m.Prune[0] != "main/demopak0.pk3" {
		t.Errorf("retail manifest = %+v", m)
	}
	if _, _, err := revertRetail(inst, func(string, string) {}); err != nil {
		t.Fatal(err)
	}
	m = &Manifest{Files: []ManifestFile{{Path: "main/demopak0.pk3"}}}
	applyEdition(m, inst)
	if len(m.Files) != 1 || len(m.Prune) != 0 {
		t.Errorf("demo manifest = %+v", m)
	}
}

func TestRetailImportRejectsWrongOrMissing(t *testing.T) {
	src := useFakeRetail(t)
	writeTestFile(t, filepath.Join(src, "Main", "Mp_pak0.pk3"), "modded pak!!")
	if _, err := planRetailImport(src, nil); err == nil || !strings.Contains(err.Error(), "mp_pak0.pk3") {
		t.Fatalf("modified required pak accepted: %v", err)
	}
	os.Remove(filepath.Join(src, "Main", "Mp_pak0.pk3"))
	if _, err := planRetailImport(src, nil); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing required pak accepted: %v", err)
	}
}

func TestPlanDoesNotPruneRetailWhenDemoWanted(t *testing.T) {
	// Prune must not delete a file the manifest still lists
	m := &Manifest{Files: []ManifestFile{{Path: "main/demopak0.pk3", SHA256: strings.Repeat("0", 64)}}, Prune: []string{"main/demopak0.pk3"}}
	inst := t.TempDir()
	writeTestFile(t, filepath.Join(inst, "main", "demopak0.pk3"), "x")
	if p := MakePlan(m, inst); len(p.Prune) != 0 {
		t.Errorf("pruned a listed file: %v", p.Prune)
	}
}
