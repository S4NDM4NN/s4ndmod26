package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Revert removes the files the launcher copied, names each, and leaves everything else alone.
func TestRevertRemovesOnlyWhatWasImported(t *testing.T) {
	src := useFakeRetail(t)
	inst := t.TempDir()
	writeTestFile(t, filepath.Join(inst, "main", "demopak0.pk3"), "demo")
	writeTestFile(t, filepath.Join(inst, "main", "mine.pk3"), "a user's own pak")
	writeTestFile(t, filepath.Join(inst, "s4ndmod26", "x.pk3"), "mod")
	// one official file is already there before the import
	writeTestFile(t, filepath.Join(inst, "main", "mp_pak0.pk3"), "content of mp_pak0.pk3")

	pl, err := planRetailImport(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	if err := copyRetail(pl, inst, nil, func(c, s string) { said = append(said, s) }); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(said, "|")
	for _, want := range []string{"copied pak0.pk3", "already installed: mp_pak0.pk3", "removed demopak0.pk3"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing log %q in %v", want, said)
		}
	}
	if !canRevert(inst) {
		t.Fatal("no import record")
	}

	said = nil
	removed, kept, err := revertRetail(inst, func(c, s string) { said = append(said, s) })
	if err != nil || removed != 3 || kept != 0 {
		t.Fatalf("revert: removed %d kept %d err %v (%v)", removed, kept, err, said)
	}
	for _, n := range []string{"pak0.pk3", "mp_pakmaps0.pk3", "mp_pakmaps1.pk3"} {
		if exists(filepath.Join(inst, "main", n)) {
			t.Errorf("%s should be removed", n)
		}
	}
	for _, p := range []string{"main/mp_pak0.pk3", "main/mine.pk3", "s4ndmod26/x.pk3"} {
		if !exists(filepath.Join(inst, filepath.FromSlash(p))) {
			t.Errorf("%s must not be touched", p)
		}
	}
	if !strings.Contains(strings.Join(said, "|"), "removed pak0.pk3") {
		t.Errorf("files are not named: %v", said)
	}
}

// A file that was imported but has changed since is kept.
func TestRevertKeepsChangedFiles(t *testing.T) {
	src := useFakeRetail(t)
	inst := t.TempDir()
	pl, _ := planRetailImport(src, nil)
	if err := copyRetail(pl, inst, nil, nil); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(inst, "main", "pak0.pk3"), "something else entirely")
	_, kept, err := revertRetail(inst, func(string, string) {})
	if err != nil || kept != 1 || !exists(filepath.Join(inst, "main", "pak0.pk3")) {
		t.Fatalf("kept %d err %v", kept, err)
	}
}

// With no import record nothing is removed.
func TestRevertWithoutJournalRemovesNothing(t *testing.T) {
	useFakeRetail(t)
	inst := t.TempDir()
	writeTestFile(t, filepath.Join(inst, "main", "pak0.pk3"), "content of pak0.pk3")
	if _, _, err := revertRetail(inst, func(string, string) {}); err == nil {
		t.Fatal("expected an error")
	}
	if !exists(filepath.Join(inst, "main", "pak0.pk3")) {
		t.Fatal("file removed without a journal")
	}
}

// A failed import removes what it copied and the journal no longer lists it.
func TestFailedImportRollsBack(t *testing.T) {
	src := useFakeRetail(t)
	inst := t.TempDir()
	pl, _ := planRetailImport(src, nil)
	// break the last file after planning
	last := pl.Files[len(pl.Files)-1]
	writeTestFile(t, findFold(pl.Src, last.Name), "corrupted after the plan")
	if err := copyRetail(pl, inst, nil, nil); err == nil {
		t.Fatal("expected failure")
	}
	for _, p := range pl.Files {
		if exists(filepath.Join(inst, "main", p.Name)) {
			t.Errorf("%s left behind", p.Name)
		}
	}
	if canRevert(inst) {
		t.Error("journal still lists removed files")
	}
}

func TestRetailInstallIsRefused(t *testing.T) {
	root := t.TempDir()
	game := filepath.Join(root, "Return to Castle Wolfenstein")
	writeTestFile(t, filepath.Join(game, "WolfMP.exe"), "exe")
	for _, d := range []string{game, filepath.Join(game, "Main"), filepath.Join(game, "s4nd", "deep")} {
		if err := checkInstallDir(d); err == nil {
			t.Errorf("%s accepted", d)
		}
	}
	if err := checkInstallDir(filepath.Join(root, "S4NDMoD26")); err != nil {
		t.Errorf("a separate folder was refused: %v", err)
	}
	// a launcher install holding imported paks (no game exe) is fine
	inst := filepath.Join(root, "inst")
	writeTestFile(t, filepath.Join(inst, "main", "pak0.pk3"), "x")
	if err := checkInstallDir(inst); err != nil {
		t.Errorf("launcher install refused: %v", err)
	}
}

func TestImportSourceMustNotOverlapInstall(t *testing.T) {
	a := t.TempDir()
	for _, c := range [][2]string{{a, a}, {filepath.Join(a, "main"), a}, {a, filepath.Join(a, "sub")}} {
		if checkImportSource(c[0], c[1]) == nil {
			t.Errorf("%v accepted", c)
		}
	}
	if checkImportSource(t.TempDir(), a) != nil {
		t.Error("separate folders refused")
	}
}

func TestScanRetailPreview(t *testing.T) {
	src := useFakeRetail(t)
	inst := t.TempDir()
	// one already installed, one source file tampered with
	writeTestFile(t, filepath.Join(inst, "main", "mp_pak0.pk3"), "content of mp_pak0.pk3")
	writeTestFile(t, filepath.Join(src, "Main", "Mp_pakmaps1.pk3"), "tampered!!!!!!!!!")

	sc := scanRetail(src, inst)
	if !sc.OK || sc.Copy != 2 {
		t.Fatalf("scan = %+v", sc)
	}
	got := map[string]string{}
	for _, e := range sc.Entries {
		got[e.Name] = e.State
	}
	want := map[string]string{"pak0.pk3": "copy", "mp_pak0.pk3": "have", "mp_pakmaps0.pk3": "copy", "mp_pakmaps1.pk3": "bad"}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("%s = %q, want %q", n, got[n], w)
		}
	}
	if exists(filepath.Join(inst, journalName)) {
		t.Error("a preview must not write anything")
	}

	os.Remove(filepath.Join(src, "Main", "Pak0.pk3"))
	if sc := scanRetail(src, inst); sc.OK || !strings.Contains(sc.Reason, "pak0.pk3") {
		t.Errorf("missing required pak: %+v", sc)
	}
	if sc := scanRetail(t.TempDir(), inst); sc.OK || !strings.Contains(sc.Reason, "No RTCW files") {
		t.Errorf("empty folder: %+v", sc)
	}
	if sc := scanRetail(inst, inst); sc.OK || sc.Reason == "" {
		t.Errorf("source = install folder: %+v", sc)
	}
}
