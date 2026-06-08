package replay

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunScanner scans replayDir for .txt files without a matching .json analysis
// and processes each one. Runs until the process exits.
func RunScanner(replayDir string) {
	for {
		processNew(replayDir)
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
