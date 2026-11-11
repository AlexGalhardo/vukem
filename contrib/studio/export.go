package studio

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// defaultExportMax is the most rows one export streams.
const defaultExportMax = 100_000

// filenameRe matches what a download name may not contain.
var filenameRe = regexp.MustCompile(`[^A-Za-z0-9_.-]`) //nolint:gochecknoglobals // compiled once

// exportFilename is a safe download name: letters, digits, "_", "." and "-".
func exportFilename(target, ext string) string {
	name := strings.Trim(filenameRe.ReplaceAllString(target, "_"), ".")
	if name == "" {
		name = "export"
	}

	return name + "." + ext
}

// csvCell neutralizes spreadsheet formulas: a cell that starts with =, +, - or
// @ is prefixed with a quote, so a spreadsheet shows it as text.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s
	}

	return s
}

// writeCSV writes header and rows as CSV with formula-safe cells.
func writeCSV(w *csv.Writer, header []string, rows [][]string) error {
	if err := w.Write(header); err != nil {
		return err
	}

	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			cells[i] = csvCell(c)
		}

		if err := w.Write(cells); err != nil {
			return err
		}
	}

	w.Flush()

	return w.Error()
}

// exportDocuments streams the current MongoDB view as JSON Lines, at most
// exportMax documents. It reads, so it works without --write. When the cap is
// reached the trailer X-Studio-Truncated says so.
func (s *Studio) exportDocuments(w http.ResponseWriter, r *http.Request) {
	m := s.mongo(r)
	if m == nil {
		http.NotFound(w, r)
		return
	}

	if f := r.URL.Query().Get("format"); f != "" && f != "json" {
		s.fail(w, r, inputError("format must be json"))
		return
	}

	coll, q := r.PathValue("collection"), r.URL.Query()
	query := DocQuery{Collection: coll, Filters: filtersFrom(q), Desc: q.Get("desc") == "1", PerPage: 200}

	// Check the collection and the filters before the response starts.
	if _, err := m.Documents(r.Context(), DocQuery{Collection: coll, Filters: query.Filters, PerPage: 1}); err != nil {
		s.fail(w, r, err)
		return
	}

	limit := s.exportMax
	if limit <= 0 {
		limit = defaultExportMax
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exportFilename(coll, "jsonl")))
	w.Header().Set("Trailer", "X-Studio-Truncated")

	truncated, err := s.streamDocs(r.Context(), w, m, query, limit)
	if err != nil {
		s.cfg.Log.WarnContext(r.Context(), "studio export failed", "collection", coll)
	}

	w.Header().Set("X-Studio-Truncated", strconv.FormatBool(truncated))
}

// streamDocs pages through the documents and writes one JSON line each.
func (s *Studio) streamDocs(
	ctx context.Context,
	w http.ResponseWriter,
	m MongoSource,
	q DocQuery,
	limit int,
) (bool, error) {
	written := 0

	for {
		pg, err := m.Documents(ctx, q)
		if err != nil {
			return false, err
		}

		for _, d := range pg.Docs {
			if written >= limit {
				return true, nil
			}

			line, err := compactJSON(d.JSON)
			if err != nil {
				return false, err
			}

			if _, err := w.Write(append(line, '\n')); err != nil {
				return false, err
			}

			written++
		}

		if !pg.HasMore || pg.Next == "" {
			return false, nil
		}

		q.After = pg.Next
	}
}

func compactJSON(pretty string) ([]byte, error) {
	var v json.RawMessage
	if err := json.Unmarshal([]byte(pretty), &v); err != nil {
		return nil, err
	}

	return json.Marshal(v)
}
