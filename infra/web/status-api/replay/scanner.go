package replay

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RetentionConfig controls how long replay files are kept on disk.
// Ages are compared against the date encoded in the filename.
// -1 disables that tier.
type RetentionConfig struct {
	MaxAgeDays    int // delete all 3 files (.rpl/.txt/.json) when age >= N days
	RplMaxAgeDays int // delete only .rpl when age >= N days (keeps .json/.txt for history)
}

// RunScanner scans replayDir for .txt files without a matching .json analysis
// and processes each one. Runs until the process exits.
func RunScanner(replayDir string, cfg RetentionConfig) {
	for {
		processNew(replayDir)
		pruneOld(replayDir, cfg)
		time.Sleep(30 * time.Second)
	}
}

func processNew(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("replay scanner: read dir %s: %v", dir, err)
		return
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".txt")
		jsonPath := filepath.Join(dir, base+".json")
		if _, err := os.Stat(jsonPath); err == nil {
			continue // already analyzed
		}

		rplPath := filepath.Join(dir, base+".rpl")
		if _, err := os.Stat(rplPath); err != nil {
			continue // .rpl missing
		}
		txtPath := filepath.Join(dir, base+".txt")

		log.Printf("replay scanner: analyzing %s", base)
		if err := analyzeAndWrite(rplPath, txtPath, jsonPath); err != nil {
			log.Printf("replay scanner: %s: %v", base, err)
		} else {
			log.Printf("replay scanner: wrote %s.json", base)
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func pruneOld(dir string, cfg RetentionConfig) {
	if cfg.MaxAgeDays < 0 && cfg.RplMaxAgeDays < 0 {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		log.Printf("replay pruner: read dir: %v", err)
		return
	}

	now := time.Now()

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".json")

		t, err := time.ParseInLocation("replays_20060102_150405", base, time.Local)
		if err != nil {
			continue
		}
		ageDays := int(now.Sub(t).Hours() / 24)

		if cfg.MaxAgeDays >= 0 && ageDays >= cfg.MaxAgeDays {
			for _, ext := range []string{".rpl", ".txt", ".json"} {
				path := filepath.Join(dir, base+ext)
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					log.Printf("replay pruner: remove %s: %v", path, err)
				}
			}
			log.Printf("replay pruner: removed %s (age %dd >= %dd)", base, ageDays, cfg.MaxAgeDays)
			continue
		}

		if cfg.RplMaxAgeDays >= 0 && ageDays >= cfg.RplMaxAgeDays {
			rpl := filepath.Join(dir, base+".rpl")
			if err := os.Remove(rpl); err != nil && !os.IsNotExist(err) {
				log.Printf("replay pruner: remove %s: %v", rpl, err)
			} else if err == nil {
				log.Printf("replay pruner: removed %s.rpl (age %dd >= %dd)", base, ageDays, cfg.RplMaxAgeDays)
			}
		}
	}
}

func analyzeAndWrite(rplPath, txtPath, jsonPath string) error {
	r, err := Parse(rplPath)
	if err != nil {
		return err
	}

	analysis := Analyze(r, txtPath)
	data, err := MarshalJSON(analysis)
	if err != nil {
		return err
	}

	tmp := jsonPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, jsonPath)
}
