package browser

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	// extractPagesDefault and extractPagesMax bound how many pages (or load-more clicks) an
	// extract follows.
	extractPagesDefault = 1
	extractPagesMax     = 100
)

// Table is rows read from a page: a table or a list, with the table's column headers.
type Table struct {
	Kind    string     `json:"kind"`
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
	Score   int        `json:"score"`
}

// extractOnce is the best table or list matching what in any frame of the current page.
func (t *Tab) extractOnce(ctx context.Context, what string) (Table, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return Table{}, err
	}
	best := Table{Score: -1}
	for _, f := range frames {
		var got Table
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%s)", extractScript, jsonString(what)), &got); err != nil {
			return Table{}, err
		}
		if got.Score > best.Score {
			best = got
		}
	}
	if best.Score < 0 {
		return Table{}, fmt.Errorf("no table or list on the page matches %q; look at the page, or extract with format text", what)
	}
	return best, nil
}

// nextControl is the ref of the visible, enabled next-page or load-more control, or "".
func (t *Tab) nextControl(ctx context.Context, label string) (string, error) {
	frames, err := t.framesInOrder(ctx)
	if err != nil {
		return "", err
	}
	for _, f := range frames {
		var ref any
		if err := t.eval(ctx, f, fmt.Sprintf("(%s)(%s)", nextControlScript, jsonString(label)), &ref); err != nil {
			return "", err
		}
		if s, ok := ref.(string); ok && s != "" {
			return s, nil
		}
	}
	return "", nil
}

// ExtractOptions says what to read and how far to follow it.
type ExtractOptions struct {
	What   string
	Format string
	Pages  int
	Next   string
}

// Extracted is what an extract read: the rows as data in the asked format, and a summary.
type Extracted struct {
	Data    string
	Ext     string
	Rows    int
	Headers []string
	Pages   int
	Preview string
}

// Extract reads the table or list matching opts.What into rows, following the next-page or
// load-more control for up to opts.Pages pages and stopping when a page adds no new rows.
// Format "table" gives CSV, "json" an array of objects, "text" the page's main text.
func (t *Tab) Extract(ctx context.Context, opts ExtractOptions) (Extracted, error) {
	pages := opts.Pages
	if pages == 0 {
		pages = extractPagesDefault
	}
	if pages < 1 || pages > extractPagesMax {
		return Extracted{}, fmt.Errorf("pages must be 1 to %d", extractPagesMax)
	}
	if opts.Format == "text" {
		return t.extractText(ctx, pages, opts.Next)
	}
	if opts.Format != "table" && opts.Format != "json" {
		return Extracted{}, fmt.Errorf("format must be table, json or text (got %q)", opts.Format)
	}
	var all Table
	seen := map[string]bool{}
	followed := 0
	for page := 1; page <= pages; page++ {
		got, err := t.extractOnce(ctx, opts.What)
		if err != nil {
			if page == 1 {
				return Extracted{}, err
			}
			break
		}
		if all.Headers == nil {
			all.Kind, all.Headers = got.Kind, got.Headers
		}
		added := 0
		for _, row := range got.Rows {
			key := strings.Join(row, "\x1f")
			if !seen[key] {
				seen[key] = true
				all.Rows = append(all.Rows, row)
				added++
			}
		}
		followed = page
		if added == 0 && page > 1 || page == pages {
			break
		}
		ref, err := t.nextControl(ctx, opts.Next)
		if err != nil {
			return Extracted{}, err
		}
		if ref == "" {
			break
		}
		if err := t.Click(ctx, ref, false); err != nil {
			return Extracted{}, fmt.Errorf("following the next control after page %d: %w", page, err)
		}
	}
	return render(all, opts.Format, followed)
}

// render writes the table as CSV or JSON with a short preview.
func render(tab Table, format string, pages int) (Extracted, error) {
	out := Extracted{Rows: len(tab.Rows), Headers: tab.Headers, Pages: pages}
	switch format {
	case "table":
		var buf bytes.Buffer
		w := csv.NewWriter(&buf)
		if len(tab.Headers) > 0 {
			if err := w.Write(tab.Headers); err != nil {
				return Extracted{}, err
			}
		}
		for _, row := range tab.Rows {
			if err := w.Write(row); err != nil {
				return Extracted{}, err
			}
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return Extracted{}, err
		}
		out.Data, out.Ext = buf.String(), "csv"
	case "json":
		objects := make([]map[string]string, 0, len(tab.Rows))
		for _, row := range tab.Rows {
			obj := map[string]string{}
			for i, cell := range row {
				key := fmt.Sprintf("col%d", i+1)
				if i < len(tab.Headers) && tab.Headers[i] != "" {
					key = tab.Headers[i]
				} else if tab.Kind == "list" {
					key = "item"
				}
				obj[key] = cell
			}
			objects = append(objects, obj)
		}
		raw, err := json.MarshalIndent(objects, "", "  ")
		if err != nil {
			return Extracted{}, err
		}
		out.Data, out.Ext = string(raw), "json"
	}
	var preview []string
	for i, row := range tab.Rows {
		if i == 5 {
			break
		}
		preview = append(preview, strings.Join(row, " | "))
	}
	out.Preview = strings.Join(preview, "\n")
	return out, nil
}

// extractText reads the main text of the page, following the next control for more pages.
func (t *Tab) extractText(ctx context.Context, pages int, next string) (Extracted, error) {
	var parts []string
	followed := 0
	for page := 1; page <= pages; page++ {
		frames, err := t.framesInOrder(ctx)
		if err != nil {
			return Extracted{}, err
		}
		var text string
		if err := t.eval(ctx, frames[0], fmt.Sprintf("(%s)()", textScript), &text); err != nil {
			return Extracted{}, err
		}
		parts = append(parts, text)
		followed = page
		if page == pages {
			break
		}
		ref, err := t.nextControl(ctx, next)
		if err != nil {
			return Extracted{}, err
		}
		if ref == "" {
			break
		}
		if err := t.Click(ctx, ref, false); err != nil {
			return Extracted{}, err
		}
	}
	data := strings.Join(parts, "\n\n")
	if strings.TrimSpace(data) == "" {
		return Extracted{}, errors.New("the page has no text")
	}
	return Extracted{Data: data, Ext: "txt", Rows: len(parts), Pages: followed, Preview: clipText(data, 400, "")}, nil
}
