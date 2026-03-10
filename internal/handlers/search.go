package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"inreview/internal/db"
)

type SearchResult struct {
	Type         string // "repo", "user", "org"
	Name         string // display name
	FullName     string // owner/repo or login
	Description  string
	Stars        int
	AvatarURL    string
	Language     string
	MergedPRs    int
	AvgMergeTime int64
	SpeedRank    int
	IsCached     bool
}

type SearchData struct {
	Query   string
	Results []SearchResult
}

func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		h.renderPartial(w, "search_results", SearchData{})
		return
	}

	data := SearchData{Query: query}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	// If query looks like owner/repo, prioritise that
	if strings.Contains(query, "/") {
		parts := strings.SplitN(query, "/", 2)
		owner, name := parts[0], parts[1]
		fullName := owner + "/" + name

		// Ensure it's in our DB (triggers sync on first encounter)
		existing, _ := h.db.GetRepo(fullName)
		if existing == nil {
			// Verify it exists on GitHub and seed it
			if ghRepo, err := h.gh.GetRepo(ctx, owner, name); err == nil {
				h.db.UpsertRepo(db.Repo{
					FullName:    fullName,
					Owner:       owner,
					Name:        ghRepo.Name,
					Description: ghRepo.Description,
					Stars:       ghRepo.Stars,
					Language:    ghRepo.Language,
					SyncStatus:  "pending",
				})
				existing, _ = h.db.GetRepo(fullName)
			}
		}
		if existing != nil {
			h.worker.Queue(fullName, false)
			rank, _ := h.db.RepoSpeedRank(fullName)
			data.Results = append(data.Results, SearchResult{
				Type:         "repo",
				Name:         existing.Name,
				FullName:     existing.FullName,
				Description:  existing.Description,
				Stars:        existing.Stars,
				Language:     existing.Language,
				MergedPRs:    existing.MergedPRCount,
				AvgMergeTime: existing.AvgMergeTimeSecs,
				SpeedRank:    rank,
				IsCached:     existing.SyncStatus == "done",
			})
		}
		h.renderPartial(w, "search_results", data)
		return
	}

	// Otherwise: search GitHub for repos + users in parallel
	repoCh := make(chan []SearchResult, 1)
	userCh := make(chan []SearchResult, 1)

	go func() {
		ghRepos, err := h.gh.SearchRepos(ctx, query, 5)
		if err != nil {
			repoCh <- nil
			return
		}
		var results []SearchResult
		for _, r := range ghRepos {
			cached, _ := h.db.GetRepo(r.FullName)
			res := SearchResult{
				Type:        "repo",
				Name:        r.Name,
				FullName:    r.FullName,
				Description: r.Description,
				Stars:       r.Stars,
				Language:    r.Language,
			}
			if cached != nil {
				res.MergedPRs = cached.MergedPRCount
				res.AvgMergeTime = cached.AvgMergeTimeSecs
				res.IsCached = cached.SyncStatus == "done"
				res.SpeedRank, _ = h.db.RepoSpeedRank(r.FullName)
			}
			results = append(results, res)
		}
		repoCh <- results
	}()

	go func() {
		ghUsers, err := h.gh.SearchUsers(ctx, query, 5)
		if err != nil {
			userCh <- nil
			return
		}
		var results []SearchResult
		for _, u := range ghUsers {
			kind := "user"
			if u.Type == "Organization" {
				kind = "org"
			}
			results = append(results, SearchResult{
				Type:        kind,
				Name:        u.Name,
				FullName:    u.Login,
				Description: u.Bio,
				AvatarURL:   u.AvatarURL,
			})
		}
		userCh <- results
	}()

	repoResults := <-repoCh
	userResults := <-userCh

	// Users/orgs first, then repos
	data.Results = append(data.Results, userResults...)
	data.Results = append(data.Results, repoResults...)

	h.renderPartial(w, "search_results", data)
}
