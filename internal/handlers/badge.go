package handlers

import (
	"fmt"
	"html"
	"math"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Badge returns a shields.io-style flat SVG badge showing avg merge time for a repo.
func (h *Handler) Badge(w http.ResponseWriter, r *http.Request) {
	owner := chi.URLParam(r, "owner")
	name := chi.URLParam(r, "name")
	fullName := owner + "/" + name

	repo, _ := h.db.GetRepo(fullName)
	if repo != nil && repo.SyncStatus != "done" {
		h.worker.Queue(fullName, false)
	}

	label := "ngmi"
	value := "no data"
	color := "#555"

	if repo != nil && repo.AvgMergeTimeSecs > 0 {
		value = "avg " + formatDuration(repo.AvgMergeTimeSecs)
		switch {
		case repo.AvgMergeTimeSecs < 86400: // < 1 day — fast
			color = "#3fb950"
		case repo.AvgMergeTimeSecs < 604800: // < 1 week — ok
			color = "#d29922"
		default: // slow
			color = "#f85149"
		}
	}

	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "max-age=3600, s-maxage=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write([]byte(badgeSVG(label, value, color)))
}

// badgeSVG generates a flat shields.io-compatible SVG badge.
func badgeSVG(label, value, color string) string {
	const charWidth = 6.5
	const pad = 10
	lw := int(math.Ceil(float64(len(label))*charWidth)) + pad
	rw := int(math.Ceil(float64(len(value))*charWidth)) + pad
	tw := lw + rw
	lx := lw / 2
	rx := lw + rw/2
	el := html.EscapeString(label)
	ev := html.EscapeString(value)
	return fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20">`+
			`<title>%s: %s</title>`+
			`<rect width="%d" height="20" fill="#555"/>`+
			`<rect x="%d" width="%d" height="20" fill="%s"/>`+
			`<g fill="#fff" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="11" text-anchor="middle">`+
			`<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>`+
			`<text x="%d" y="14">%s</text>`+
			`<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>`+
			`<text x="%d" y="14">%s</text>`+
			`</g></svg>`,
		tw, el, ev,
		tw,
		lw, rw, color,
		lx, el, lx, el,
		rx, ev, rx, ev,
	)
}
