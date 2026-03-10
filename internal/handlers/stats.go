package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"inreview/internal/db"
	"inreview/internal/rdb"
)

// StatsData is passed to the stats page template.
type StatsData struct {
	BaseData
	Overall       db.GlobalOverallStats
	SizeChartJSON template.JS
	TimeChartJSON template.JS
	Trim          int
	MinStars      int
	MinContribs   int
	OGTitle       string
	OGDesc        string
	OGUrl         string
}

// statsChartPayload is marshaled to JSON and embedded in the stats page.
type statsChartPayload struct {
	Labels               []string  `json:"labels"`
	PRCounts             []int     `json:"prCounts"`
	AvgHours             []float64 `json:"avgHours"`
	MedianHours          []float64 `json:"medianHours"`
	ApprovalRate         []float64 `json:"approvalRate"`
	ChangesRequestedRate []float64 `json:"changesRequestedRate"`
	AvgChangesRequested  []float64 `json:"avgChangesRequested"`
}

// timeChartPayload holds monthly time-series data for line charts.
type timeChartPayload struct {
	Labels               []string  `json:"labels"`
	PRCounts             []int     `json:"prCounts"`
	OpenedCounts         []int     `json:"openedCounts"`
	MergeVsOpenRate      []float64 `json:"mergeVsOpenRate"`
	AvgSize              []float64 `json:"avgSize"`
	MedianSize           []float64 `json:"medianSize"`
	AvgHours             []float64 `json:"avgHours"`
	MedianHours          []float64 `json:"medianHours"`
	ChangesRequestedRate []float64 `json:"changesRequestedRate"`
	AvgFirstReviewHours  []float64 `json:"avgFirstReviewHours"`
	MedFirstReviewHours  []float64 `json:"medFirstReviewHours"`
	UnreviewedMergeRate  []float64 `json:"unreviewedMergeRate"`
	LinesPerContrib      []float64 `json:"linesPerContrib"`
}

func parseTrim(r *http.Request) (trim int, cutoffPct float64) {
	trim, _ = strconv.Atoi(r.URL.Query().Get("trim"))
	if trim < 0 || trim > 20 {
		trim = 0
	}
	cutoffPct = 1.0 - float64(trim)/100.0
	return
}

func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	trim, cutoffPct := parseTrim(r)
	minStars, _ := strconv.Atoi(r.URL.Query().Get("min_stars"))
	if minStars < 0 {
		minStars = 0
	}
	minContribs, _ := strconv.Atoi(r.URL.Query().Get("min_contribs"))
	if minContribs < 0 {
		minContribs = 0
	}

	// ── Cache check ────────────────────────────────────────────────
	cacheKey := fmt.Sprintf("stats:v4:%d:%d:%d", trim, minStars, minContribs)
	if h.cache != nil {
		if raw, ok := h.cache.Get(r.Context(), cacheKey); ok {
			var data StatsData
			if json.Unmarshal(raw, &data) == nil {
				data.BaseData = h.baseData(r) // not cached — set per-request
				h.render(w, "stats", data)
				return
			}
		}
	}

	// ── Parallel DB queries ────────────────────────────────────────
	type overallRes struct {
		v   db.GlobalOverallStats
		err error
	}
	type bucketsRes struct {
		v   []db.GlobalSizeBucket
		err error
	}
	type pointsRes struct {
		v   []db.TimeSeriesPoint
		err error
	}

	type openedRes struct {
		v   []db.TimeSeriesPoint
		err error
	}

	overallCh := make(chan overallRes, 1)
	bucketsCh := make(chan bucketsRes, 1)
	pointsCh := make(chan pointsRes, 1)
	openedCh := make(chan openedRes, 1)

	go func() {
		v, err := h.db.GlobalOverallStats(minStars, minContribs)
		overallCh <- overallRes{v, err}
	}()
	go func() {
		v, err := h.db.GlobalSizeChartData(cutoffPct, minStars, minContribs)
		bucketsCh <- bucketsRes{v, err}
	}()
	go func() {
		v, err := h.db.GlobalTimeSeriesData(cutoffPct, minStars, minContribs)
		pointsCh <- pointsRes{v, err}
	}()
	go func() {
		v, err := h.db.GlobalOpenedSeriesData(minStars, minContribs)
		openedCh <- openedRes{v, err}
	}()

	overall := <-overallCh
	buckets := <-bucketsCh
	points := <-pointsCh
	opened := <-openedCh

	data := StatsData{
		Overall:     overall.v,
		Trim:        trim,
		MinStars:    minStars,
		MinContribs: minContribs,
		OGTitle:     "Global PR Stats — ngmi",
		OGDesc:  "How PR size affects review time and changes requested, across all repos tracked on ngmi.",
		OGUrl:   "https://ngmi.review/stats",
	}

	if len(buckets.v) > 0 {
		payload := statsChartPayload{}
		for _, b := range buckets.v {
			payload.Labels = append(payload.Labels, b.Label)
			payload.PRCounts = append(payload.PRCounts, b.PRCount)
			payload.AvgHours = append(payload.AvgHours, roundTo1(b.AvgSecs/3600))
			payload.MedianHours = append(payload.MedianHours, roundTo1(b.MedianSecs/3600))
			payload.ApprovalRate = append(payload.ApprovalRate, roundTo1(b.ApprovalRate))
			payload.ChangesRequestedRate = append(payload.ChangesRequestedRate, roundTo1(b.ChangesRequestedRate))
			payload.AvgChangesRequested = append(payload.AvgChangesRequested, roundTo1(b.AvgChangesRequested))
		}
		if raw, err := json.Marshal(payload); err == nil {
			data.SizeChartJSON = template.JS(raw)
		}
	}

	if len(points.v) > 0 {
		tp := timeChartPayload{}
		for _, p := range points.v {
			tp.Labels = append(tp.Labels, p.Label)
			tp.PRCounts = append(tp.PRCounts, p.PRCount)
			tp.AvgSize = append(tp.AvgSize, roundTo1(p.AvgSize))
			tp.MedianSize = append(tp.MedianSize, roundTo1(p.MedianSize))
			tp.AvgHours = append(tp.AvgHours, roundTo1(p.AvgSecs/3600))
			tp.MedianHours = append(tp.MedianHours, roundTo1(p.MedianSecs/3600))
			tp.ChangesRequestedRate = append(tp.ChangesRequestedRate, roundTo1(p.ChangesRequestedRate))
			tp.AvgFirstReviewHours = append(tp.AvgFirstReviewHours, roundTo1(p.AvgFirstReviewSecs/3600))
			tp.MedFirstReviewHours = append(tp.MedFirstReviewHours, roundTo1(p.MedFirstReviewSecs/3600))
			tp.UnreviewedMergeRate = append(tp.UnreviewedMergeRate, roundTo1(p.UnreviewedRate))
			tp.LinesPerContrib = append(tp.LinesPerContrib, roundTo1(p.LinesPerContrib))
		}
		// Build opened-per-month and merge rate aligned to merge-month labels.
		openedMap := make(map[string]int)
		for _, p := range opened.v {
			openedMap[p.Label] = p.PRCount
		}
		for i, label := range tp.Labels {
			oc := openedMap[label]
			tp.OpenedCounts = append(tp.OpenedCounts, oc)
			rate := 0.0
			if oc > 0 {
				rate = roundTo1(float64(tp.PRCounts[i]) / float64(oc) * 100)
			}
			tp.MergeVsOpenRate = append(tp.MergeVsOpenRate, rate)
		}
		if raw, err := json.Marshal(tp); err == nil {
			data.TimeChartJSON = template.JS(raw)
		}
	}

	// ── Cache store ────────────────────────────────────────────────
	if h.cache != nil {
		if raw, err := json.Marshal(data); err == nil {
			h.cache.Set(r.Context(), cacheKey, raw, rdb.CacheTTL)
		}
	}

	data.BaseData = h.baseData(r)
	h.render(w, "stats", data)
}
