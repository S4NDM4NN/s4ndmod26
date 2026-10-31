package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"s4ndmod26/status-api/demostore"
)

// Series: matches linked in order (the games of a final, a best-of-five).
// Anyone can read them.  A key links matches it has uploaded demos of, and
// the key that made a series is the one that renames, reorders or dissolves it.

const (
	maxSeriesMatches = 20
	maxSeriesName    = 80
)

func (s *demoService) listSeries(w http.ResponseWriter, r *http.Request) {
	limit, offset := 30, 0
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
		offset = n
	}
	ss, total, err := s.db.ListSeries(r.Context(), limit, offset)
	if err != nil {
		log.Printf("demos: list series: %v", err)
		demoFail(w, http.StatusInternalServerError, "could not list the series")
		return
	}
	demoJSON(w, http.StatusOK, map[string]interface{}{"series": ss, "total": total})
}

func (s *demoService) getSeries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !matchIDRe.MatchString(id) {
		demoFail(w, http.StatusNotFound, "no such series")
		return
	}
	sr, err := s.db.GetSeries(r.Context(), id)
	if err != nil {
		log.Printf("demos: series %s: %v", id, err)
		demoFail(w, http.StatusInternalServerError, "could not read the series")
		return
	}
	if sr == nil {
		demoFail(w, http.StatusNotFound, "no such series")
		return
	}
	demoJSON(w, http.StatusOK, sr)
}

type seriesBody struct {
	Name    string   `json:"name"`
	Matches []string `json:"matches"`
}

// cleanSeriesName trims a name and drops control characters.
func cleanSeriesName(n string) string {
	n = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, n)
	n = strings.Join(strings.Fields(n), " ")
	if r := []rune(n); len(r) > maxSeriesName {
		n = string(r[:maxSeriesName])
	}
	return n
}

// saveSeries makes a series (POST) or replaces one's name and matches (PUT).
func (s *demoService) saveSeries(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.uploader(r)
	if !ok {
		demoFail(w, http.StatusForbidden, "bad upload key")
		return
	}
	var b seriesBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&b); err != nil {
		demoFail(w, http.StatusBadRequest, "send {\"name\": ..., \"matches\": [...]} as JSON")
		return
	}
	b.Name = cleanSeriesName(b.Name)
	if b.Name == "" {
		demoFail(w, http.StatusBadRequest, "give the series a name")
		return
	}
	if len(b.Matches) == 0 || len(b.Matches) > maxSeriesMatches {
		demoFail(w, http.StatusBadRequest, "a series has between 1 and "+strconv.Itoa(maxSeriesMatches)+" matches")
		return
	}
	seen := map[string]bool{}
	for _, m := range b.Matches {
		if !matchIDRe.MatchString(m) || seen[m] {
			demoFail(w, http.StatusBadRequest, "the matches must be distinct match ids")
			return
		}
		seen[m] = true
	}
	var id string
	var err error
	if r.Method == http.MethodPost {
		id, err = s.db.CreateSeries(r.Context(), owner, b.Name, b.Matches)
	} else {
		id = r.PathValue("id")
		if !matchIDRe.MatchString(id) {
			demoFail(w, http.StatusNotFound, "no such series")
			return
		}
		err = s.db.UpdateSeries(r.Context(), owner, id, b.Name, b.Matches)
	}
	if !s.seriesError(w, err) {
		return
	}
	demoJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *demoService) deleteSeries(w http.ResponseWriter, r *http.Request) {
	owner, ok := s.uploader(r)
	if !ok {
		demoFail(w, http.StatusForbidden, "bad upload key")
		return
	}
	id := r.PathValue("id")
	if !matchIDRe.MatchString(id) {
		demoFail(w, http.StatusNotFound, "no such series")
		return
	}
	if !s.seriesError(w, s.db.DeleteSeries(r.Context(), owner, id)) {
		return
	}
	demoJSON(w, http.StatusOK, map[string]bool{"removed": true})
}

// seriesError answers for a failed series change and reports whether there was no failure.
func (s *demoService) seriesError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, demostore.ErrSeriesNotFound):
		demoFail(w, http.StatusNotFound, "no such series")
	case errors.Is(err, demostore.ErrNoSuchMatch):
		demoFail(w, http.StatusNotFound, "one of those matches does not exist")
	case errors.Is(err, demostore.ErrNotYours):
		// not found and not yours look alike for the series itself
		msg := "you can only link matches you have uploaded a demo of"
		if i := strings.Index(err.Error(), ": "); i >= 0 && strings.Contains(err.Error(), "you have no demo") {
			msg = err.Error()[i+2:]
		}
		demoFail(w, http.StatusForbidden, msg)
	default:
		log.Printf("demos: series: %v", err)
		demoFail(w, http.StatusInternalServerError, "could not save the series")
	}
	return false
}
