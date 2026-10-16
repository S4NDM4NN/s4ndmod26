package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// retail-import.json in the install folder records what an import did, so that "Revert to Demo Mode" removes
// exactly the files the launcher created and nothing else.
const journalName = "retail-import.json"

type journalFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type retailJournal struct {
	Version     int           `json:"version"`
	Created     []journalFile `json:"created"`     // copied by the launcher: Revert removes these
	Present     []string      `json:"present"`     // already there and correct: Revert leaves these
	RemovedDemo []string      `json:"removedDemo"` // demo files the import removed
}

func journalPath(dir string) string { return filepath.Join(dir, journalName) }

func loadJournal(dir string) *retailJournal {
	j := &retailJournal{Version: 1}
	if b, err := os.ReadFile(journalPath(dir)); err == nil {
		var in retailJournal
		if json.Unmarshal(b, &in) == nil && in.Version == 1 {
			j = &in
		}
	}
	return j
}

func (j *retailJournal) save(dir string) error {
	b, _ := json.MarshalIndent(j, "", "  ")
	tmp := journalPath(dir) + ".part"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, journalPath(dir))
}

func (j *retailJournal) created(name string) bool {
	for _, c := range j.Created {
		if c.Name == name {
			return true
		}
	}
	return false
}

func (j *retailJournal) addCreated(name string, size int64) {
	for i, c := range j.Created {
		if c.Name == name {
			j.Created[i].Size = size
			return
		}
	}
	j.Created = append(j.Created, journalFile{name, size})
	for i, p := range j.Present {
		if p == name {
			j.Present = append(j.Present[:i], j.Present[i+1:]...)
			break
		}
	}
}

func (j *retailJournal) addPresent(name string) {
	if j.created(name) {
		return
	}
	for _, p := range j.Present {
		if p == name {
			return
		}
	}
	j.Present = append(j.Present, name)
}

// canRevert reports whether the install has an import record with files to remove.
func canRevert(dir string) bool { return len(loadJournal(dir).Created) > 0 }

// revertRetail removes the files the journal says the launcher copied, each only if it is still the size that
// was written.  say receives one line per file, by name.  Files that were already in place, or that changed
// since, are kept.  It returns how many files were removed and how many were kept.
func revertRetail(dir string, say func(cls, text string)) (removed, kept int, err error) {
	j := loadJournal(dir)
	if len(j.Created) == 0 {
		return 0, 0, fmt.Errorf("Nothing to revert: no files in this folder were imported by the launcher.")
	}
	main := filepath.Join(dir, "main")
	var left []journalFile
	for _, c := range j.Created {
		p := filepath.Join(main, c.Name)
		st, serr := os.Stat(p)
		switch {
		case os.IsNotExist(serr):
			say("d", "already gone: "+c.Name)
			continue
		case serr != nil || !st.Mode().IsRegular():
			say("w", "kept "+c.Name+" ("+fmt.Sprint(serr)+")")
			left = append(left, c)
			kept++
			continue
		case st.Size() != c.Size:
			say("w", "kept "+c.Name+" (changed since it was imported)")
			kept++
			continue
		}
		if rerr := os.Remove(p); rerr != nil {
			say("e", "could not remove "+c.Name+": "+rerr.Error())
			left = append(left, c)
			kept++
			continue
		}
		os.Remove(p + ".part")
		say("", "removed "+c.Name)
		removed++
	}
	for _, p := range j.Present {
		say("d", "left "+p+" (it was already there)")
	}
	j.Created = left
	if len(left) == 0 && len(j.Present) == 0 {
		os.Remove(journalPath(dir))
	} else if serr := j.save(dir); serr != nil {
		return removed, kept, serr
	}
	return removed, kept, nil
}
