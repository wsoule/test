package handlers

import (
	"net/http"

	"inreview/internal/db"
)

// BlogData is passed to the blog page template and the blog_stats partial.
type BlogData struct {
	BaseData
	LiveStats    db.GlobalOverallStats
	TopReviewers []db.LeaderboardEntry
	TopSpeed     []db.LeaderboardEntry
	TotalRepos   int
	TotalPRs     int
	TotalReviews int
	OGTitle      string
	OGDesc       string
	OGUrl        string
}

func (h *Handler) Blog(w http.ResponseWriter, r *http.Request) {
	overall, _ := h.db.GlobalOverallStats(0, 0)
	topReviewers, _ := h.db.LeaderboardReviewers(5)
	topSpeed, _ := h.db.LeaderboardReposBySpeed("ASC", 5)
	totalRepos, totalPRs, totalReviews := h.db.TotalStats()

	data := BlogData{
		BaseData:     h.baseData(r),
		LiveStats:    overall,
		TopReviewers: topReviewers,
		TopSpeed:     topSpeed,
		TotalRepos:   totalRepos,
		TotalPRs:     totalPRs,
		TotalReviews: totalReviews,
		OGTitle:      "PR Review Time: What the Data Says — ngmi",
		OGDesc:       "Analysis of PR review patterns across thousands of GitHub repositories.",
		OGUrl:        "https://ngmi.review/blog",
	}
	h.render(w, "blog", data)
}

// BlogLiveStats serves the live stats partial for HTMX polling.
func (h *Handler) BlogLiveStats(w http.ResponseWriter, r *http.Request) {
	overall, _ := h.db.GlobalOverallStats(0, 0)
	topReviewers, _ := h.db.LeaderboardReviewers(5)
	topSpeed, _ := h.db.LeaderboardReposBySpeed("ASC", 5)
	totalRepos, totalPRs, totalReviews := h.db.TotalStats()

	data := BlogData{
		LiveStats:    overall,
		TopReviewers: topReviewers,
		TopSpeed:     topSpeed,
		TotalRepos:   totalRepos,
		TotalPRs:     totalPRs,
		TotalReviews: totalReviews,
	}
	h.renderPartial(w, "blog_stats", data)
}
