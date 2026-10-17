package main

import (
	"net/http/httptest"
	"os"
	"testing"
)

func TestPathsReal(t *testing.T) {
	dir := os.Getenv("RPL_DIR")
	if dir == "" {
		t.Skip()
	}
	h := gamePathsHandler(dir)
	req := httptest.NewRequest("GET", "/api/games/a/paths", nil)
	req.SetPathValue("match", "a")
	w := httptest.NewRecorder()
	h(w, req)
	t.Logf("status %d bytes %d", w.Code, w.Body.Len())
	os.WriteFile(dir+"/paths.json", w.Body.Bytes(), 0644)
}
