package demo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// DEMO_SAMPLES points at a directory of extracted .dm_NN files.  The sample
// demos are too large to commit, so the test is skipped when it is unset.
func TestParseSamples(t *testing.T) {
	root := os.Getenv("DEMO_SAMPLES")
	if root == "" {
		t.Skip("DEMO_SAMPLES not set")
	}
	n := 0
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.Contains(filepath.Ext(path), "dm_") {
			return nil
		}
		proto, _ := strconv.Atoi(strings.TrimPrefix(filepath.Ext(path), ".dm_"))
		data, err := os.ReadFile(path)
		if err != nil {
			t.Error(err)
			return nil
		}
		d, perr := Parse(data, proto)
		if perr != nil {
			t.Errorf("%s: %v", path, perr)
			return nil
		}
		if len(d.Frames) == 0 || d.Configstrings[0] == "" {
			t.Errorf("%s: empty demo", path)
		}
		for i := 1; i < len(d.Frames); i++ {
			if d.Frames[i].ServerTime < d.Frames[i-1].ServerTime {
				t.Errorf("%s: serverTime went backwards at frame %d", path, i)
				break
			}
		}
		n++
		return nil
	})
	t.Logf("parsed %d demos", n)
}

func TestHuffmanTreeBuilds(t *testing.T) {
	if msgTree == nil || msgTree.symbol != internalNode {
		t.Fatal("static huffman tree was not built")
	}
}
