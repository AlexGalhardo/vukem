package studio

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// exportRows streams the current grid view of a table as JSON Lines (or CSV
// with format=csv), at most exportMax rows, the same cap and trailer as the
// MongoDB export. It reads, so it works without --write. A CSV cell that
// starts with =, +, - or @ gets a leading quote.
func (s *Studio) exportRows(w http.ResponseWriter, r *http.Request) {
	src := s.sql(r)
	if src == nil {
		http.NotFound(w, r)
		return
	}

	format := r.URL.Query().Get("format")
	if format != "" && format != "json" && format != "csv" {
		s.fail(w, r, inputError("format must be json or csv"))
		return
	}

	q := r.URL.Query()
	query := RowQuery{
		Table: r.PathValue("table"), Filters: filtersFrom(q), Sort: q.Get("sort"), Desc: q.Get("desc") == "1",
		PerPage: 200, Full: true,
	}

	// The first page is read before the response starts, so a refused table,
	// filter or sort is a normal error page.
	pg, err := src.Rows(r.Context(), query)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	limit := s.exportMax
	if limit <= 0 {
		limit = defaultExportMax
	}

	ext, ctype := "jsonl", "application/x-ndjson; charset=utf-8"
	if format == "csv" {
		ext, ctype = "csv", "text/csv; charset=utf-8"
	}

	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", exportFilename(query.Table, ext)))
	w.Header().Set("Trailer", "X-Studio-Truncated")

	t, _ := src.Table(r.Context(), query.Table)
	keyset := query.Sort == "" && len(t.PrimaryKey) > 0

	truncated, err := s.streamRows(r, w, src, query, pg, limit, format == "csv", keyset)
	if err != nil {
		s.cfg.Log.WarnContext(r.Context(), "studio export failed", "table", query.Table, "cause", err)
	}

	w.Header().Set("X-Studio-Truncated", strconv.FormatBool(truncated))
}

// streamRows writes pg and the pages after it until the rows or limit run out.
func (s *Studio) streamRows(
	r *http.Request,
	w http.ResponseWriter,
	src SQLSource,
	q RowQuery,
	pg RowPage,
	limit int,
	asCSV, keyset bool,
) (bool, error) {
	var cw *csv.Writer

	if asCSV {
		cw = csv.NewWriter(w)
		defer cw.Flush()

		head := make([]string, len(pg.Columns))
		for i, c := range pg.Columns {
			head[i] = c.Name
		}

		if err := cw.Write(head); err != nil {
			return false, err
		}
	}

	written, page := 0, 1

	for {
		for _, row := range pg.Rows {
			if written >= limit {
				return true, nil
			}

			if err := writeRow(w, cw, pg.Columns, row); err != nil {
				return false, err
			}

			written++
		}

		if !pg.HasMore {
			break
		}

		// Keyset follows the cursor; offset paging stops at the page cap.
		switch {
		case keyset && pg.Next == nil:
			return true, nil // the last key could not be used as a cursor
		case keyset:
			q.After = pg.Next
		case page >= maxGridPage:
			return true, nil
		default:
			page++
			q.Page = page
		}

		var err error
		if pg, err = src.Rows(r.Context(), q); err != nil {
			return false, err
		}
	}

	if cw != nil {
		cw.Flush()
		return false, cw.Error()
	}

	return false, nil
}

func writeRow(w http.ResponseWriter, cw *csv.Writer, cols []Column, row []Cell) error {
	if cw != nil {
		cells := make([]string, len(row))
		for i, c := range row {
			if !c.Null {
				cells[i] = csvCell(c.Text)
			}
		}

		return cw.Write(cells)
	}

	var b bytes.Buffer

	b.WriteByte('{')

	for i, c := range cols {
		if i > 0 {
			b.WriteByte(',')
		}

		name, _ := json.Marshal(c.Name) //nolint:errchkjson // a string always marshals
		b.Write(name)
		b.WriteByte(':')

		if row[i].Null {
			b.WriteString("null")
			continue
		}

		text, _ := json.Marshal(row[i].Text) //nolint:errchkjson // a string always marshals
		b.Write(text)
	}

	b.WriteString("}\n")

	_, err := w.Write(b.Bytes())

	return err
}
