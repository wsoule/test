package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"inreview/internal/db"
	"inreview/internal/rdb"
)

const pageSize = 100

type LeaderboardPageData struct {
	BaseData
	Category    string
	Title       string
	Description string
	RepoRows    []db.RepoLeaderboardRow
	UserRows    []db.UserLeaderboardRow
	CleanRows   []db.CleanLeaderboardRow
	HasMore     bool
	NextOffset  int
	OGTitle     string
	OGDesc      string
	OGUrl       string
}

var leaderboardMeta = map[string][2]string{
	"speed":       {"Speed Demons", "Repos with the fastest average PR-to-merge time"},
	"graveyard":   {"PR Graveyard", "Repos with the slowest average PR-to-merge time"},
	"reviewers":   {"Review Champions", "Users who have submitted the most reviews"},
	"gatekeepers": {"Gatekeepers", "Users who request changes the most"},
	"authors":     {"Merge Masters", "Authors with the most merged PRs"},
	"oneshot":     {"One-Shot Heroes", "Repos where PRs get approved on the first try"},
}

func (h *Handler) LeaderboardPage(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	meta, ok := leaderboardMeta[category]
	if !ok {
		h.renderError(w, http.StatusNotFound, "Leaderboard Not Found",
			"\""+category+"\" is not a valid leaderboard category.")
		return
	}

	data := LeaderboardPageData{
		Category:    category,
		Title:       meta[0],
		Description: meta[1],
		OGTitle:     meta[0] + " — ngmi",
		OGDesc:      meta[1] + ". Global PR review leaderboards at ngmi.review.",
		OGUrl:       "https://ngmi.review/leaderboard/" + category,
	}

	h.populateLeaderboardData(&data, category, 0)
	data.BaseData = h.baseData(r)
	h.render(w, "leaderboard_page", data)
}

// LeaderboardRows returns additional table rows for infinite scroll (HTMX partial).
func (h *Handler) LeaderboardRows(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	if _, ok := leaderboardMeta[category]; !ok {
		http.NotFound(w, r)
		return
	}

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	data := LeaderboardPageData{Category: category}
	h.populateLeaderboardData(&data, category, offset)
	h.renderPartial(w, "leaderboard_rows", data)
}

// LeaderboardSearchData is the result returned to the search partial.
type LeaderboardSearchData struct {
	Category string
	Query    string
	Empty    bool // empty query — render nothing

	// Set when the query target isn't tracked yet
	NotTracked bool
	TrackURL   string

	// User result (reviewers / gatekeepers / authors)
	Login            string
	AvatarURL        string
	Rank             int
	TotalReviews     int
	Approvals        int
	ChangesRequested int
	MergedPRs        int
	AvgMergeTimeSecs int64

	// Repo result (speed / graveyard / oneshot)
	FullName      string
	AvgSecs       int64
	MinSecs       int64
	MaxSecs       int64
	PRCount       int
	SpeedRank     int
	GraveyardRank int
}

// LeaderboardSearch handles HTMX search requests from the leaderboard table page.
func (h *Handler) LeaderboardSearch(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	result := LeaderboardSearchData{Category: category, Query: q}

	if q == "" {
		result.Empty = true
		h.renderPartial(w, "leaderboard_search", result)
		return
	}

	isUserCategory := category == "reviewers" || category == "gatekeepers" || category == "authors"

	if isUserCategory {
		login := strings.TrimPrefix(q, "@")
		h.leaderboardUserSearch(&result, category, login)
	} else {
		h.leaderboardRepoSearch(&result, category, q)
	}

	h.renderPartial(w, "leaderboard_search", result)
}

func (h *Handler) leaderboardUserSearch(result *LeaderboardSearchData, category, login string) {
	// Check if we have any review/author data for this user.
	reviewerStats, _ := h.db.UserReviewerStats(login)
	authorStats, _ := h.db.UserAuthorStats(login)

	if reviewerStats == nil && authorStats == nil {
		result.NotTracked = true
		result.TrackURL = "/user/" + login
		return
	}

	user, _ := h.db.GetUser(login)
	if user != nil {
		result.Login = user.Login
		result.AvatarURL = user.AvatarURL
	} else {
		result.Login = login
	}

	switch category {
	case "reviewers":
		if reviewerStats == nil {
			result.NotTracked = true
			result.TrackURL = "/user/" + login
			return
		}
		result.TotalReviews = reviewerStats.TotalReviews
		result.Approvals = reviewerStats.Approvals
		result.ChangesRequested = reviewerStats.ChangesRequested
		result.Rank, _ = h.db.UserReviewerRank(login)

	case "gatekeepers":
		if reviewerStats == nil || reviewerStats.ChangesRequested == 0 {
			result.NotTracked = true
			result.TrackURL = "/user/" + login
			return
		}
		result.TotalReviews = reviewerStats.TotalReviews
		result.Approvals = reviewerStats.Approvals
		result.ChangesRequested = reviewerStats.ChangesRequested
		result.Rank, _ = h.db.UserGatekeeperRank(login)

	case "authors":
		if authorStats == nil || authorStats.MergedPRs == 0 {
			result.NotTracked = true
			result.TrackURL = "/user/" + login
			return
		}
		result.MergedPRs = authorStats.MergedPRs
		result.AvgMergeTimeSecs = authorStats.AvgMergeTimeSecs
		result.Rank, _ = h.db.UserAuthorRank(login)
	}
}

func (h *Handler) leaderboardRepoSearch(result *LeaderboardSearchData, category, query string) {
	// Accept both "owner/repo" and partial name — try exact match first.
	repo, _ := h.db.GetRepo(query)
	if repo == nil {
		// Fallback: search by partial name.
		repos, _ := h.db.SearchRepos(query, 1)
		if len(repos) > 0 {
			repo = &repos[0]
		}
	}

	if repo == nil || repo.MergedPRCount == 0 {
		trackPath := "/repo/" + query
		if repo != nil {
			trackPath = "/repo/" + repo.FullName
		}
		result.NotTracked = true
		result.TrackURL = trackPath
		return
	}

	result.FullName = repo.FullName
	result.AvgSecs = repo.AvgMergeTimeSecs
	result.MinSecs = repo.MinMergeTimeSecs
	result.MaxSecs = repo.MaxMergeTimeSecs
	result.PRCount = repo.MergedPRCount
	result.SpeedRank, _ = h.db.RepoSpeedRank(repo.FullName)
	result.GraveyardRank, _ = h.db.RepoGraveyardRank(repo.FullName)
}

// cachedLeaderboardRows is the cacheable subset of LeaderboardPageData.
type cachedLeaderboardRows struct {
	RepoRows  []db.RepoLeaderboardRow
	UserRows  []db.UserLeaderboardRow
	CleanRows []db.CleanLeaderboardRow
	HasMore   bool
	NextOffset int
}

// populateLeaderboardData fetches one page of rows into data starting at offset.
// Results are cached in Redis for rdb.CacheTTL.
func (h *Handler) populateLeaderboardData(data *LeaderboardPageData, category string, offset int) {
	cacheKey := fmt.Sprintf("lb:%s:%d", category, offset)
	ctx := context.Background()

	// Try cache first.
	if raw, ok := h.cache.Get(ctx, cacheKey); ok {
		var cached cachedLeaderboardRows
		if json.Unmarshal(raw, &cached) == nil {
			data.RepoRows = cached.RepoRows
			data.UserRows = cached.UserRows
			data.CleanRows = cached.CleanRows
			data.HasMore = cached.HasMore
			data.NextOffset = cached.NextOffset
			return
		}
	}

	// Cache miss — query the DB.
	fetch := pageSize + 1
	var cached cachedLeaderboardRows

	switch category {
	case "speed":
		rows, _ := h.db.FullLeaderboardRepoSpeed("ASC", fetch, offset)
		cached.HasMore = len(rows) == fetch
		if cached.HasMore {
			rows = rows[:pageSize]
		}
		cached.RepoRows = rows
	case "graveyard":
		rows, _ := h.db.FullLeaderboardRepoSpeed("DESC", fetch, offset)
		cached.HasMore = len(rows) == fetch
		if cached.HasMore {
			rows = rows[:pageSize]
		}
		cached.RepoRows = rows
	case "reviewers":
		rows, _ := h.db.FullLeaderboardReviewers(fetch, offset)
		cached.HasMore = len(rows) == fetch
		if cached.HasMore {
			rows = rows[:pageSize]
		}
		cached.UserRows = rows
	case "gatekeepers":
		rows, _ := h.db.FullLeaderboardGatekeepers(fetch, offset)
		cached.HasMore = len(rows) == fetch
		if cached.HasMore {
			rows = rows[:pageSize]
		}
		cached.UserRows = rows
	case "authors":
		rows, _ := h.db.FullLeaderboardAuthors(fetch, offset)
		cached.HasMore = len(rows) == fetch
		if cached.HasMore {
			rows = rows[:pageSize]
		}
		cached.UserRows = rows
	case "oneshot":
		rows, _ := h.db.FullLeaderboardCleanApprovals(fetch, offset)
		cached.HasMore = len(rows) == fetch
		if cached.HasMore {
			rows = rows[:pageSize]
		}
		cached.CleanRows = rows
	}

	if cached.HasMore {
		cached.NextOffset = offset + pageSize
	}

	// Store in cache only when we have results (avoid caching empty state during initial sync).
	hasResults := len(cached.RepoRows) > 0 || len(cached.UserRows) > 0 || len(cached.CleanRows) > 0
	if hasResults {
		if raw, err := json.Marshal(cached); err == nil {
			h.cache.Set(ctx, cacheKey, raw, rdb.LeaderboardCacheTTL)
		}
	}

	data.RepoRows = cached.RepoRows
	data.UserRows = cached.UserRows
	data.CleanRows = cached.CleanRows
	data.HasMore = cached.HasMore
	data.NextOffset = cached.NextOffset
}

