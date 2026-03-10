package handlers

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"inreview/internal/db"
)

type OrgData struct {
	BaseData
	Org             *db.User
	Repos           []db.Repo
	ReviewerBoard   []db.LeaderboardEntry
	GatekeeperBoard []db.LeaderboardEntry
	TotalMergedPRs  int
	TotalReviews    int
	IsSyncing       bool
	TimeChartJSON   template.JS
	Trim            int
	OGTitle         string
	OGDesc          string
	OGUrl           string
}

func (h *Handler) Org(w http.ResponseWriter, r *http.Request) {
	orgName := chi.URLParam(r, "org")

	trim, cutoffPct := parseTrim(r)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	ghUser, err := h.gh.GetUser(ctx, orgName)
	if err != nil {
		// If the org is cached in the DB, serve from there instead of erroring.
		if cached, dbErr := h.db.GetUser(orgName); dbErr == nil && cached != nil && cached.IsOrg {
			// Fall through with ghUser = nil
		} else {
			h.renderGHError(w, r, err, "Org Not Found",
				"Could not find the organization "+orgName+" on GitHub. Check the spelling and try again.")
			return
		}
	}

	// Redirect if it's actually a user (only when we have fresh GitHub data).
	if ghUser != nil && ghUser.Type != "Organization" {
		http.Redirect(w, r, "/user/"+orgName, http.StatusFound)
		return
	}

	if ghUser != nil {
		h.db.UpsertUser(db.User{
			Login:       ghUser.Login,
			Name:        ghUser.Name,
			AvatarURL:   ghUser.AvatarURL,
			Bio:         ghUser.Bio,
			PublicRepos: ghUser.PublicRepos,
			Followers:   ghUser.Followers,
			IsOrg:       true,
		})
	}

	// Queue top org repos for sync
	go func() {
		repos, err := h.gh.GetOrgRepos(context.Background(), orgName, 20)
		if err != nil {
			return
		}
		for _, repo := range repos {
			h.db.UpsertRepo(db.Repo{
				FullName:    repo.FullName,
				Owner:       repo.Owner.Login,
				Name:        repo.Name,
				Description: repo.Description,
				Stars:       repo.Stars,
				Language:    repo.Language,
				OrgName:     orgName,
				SyncStatus:  "pending",
			})
			h.worker.Queue(repo.FullName, false)
		}
	}()

	org, _ := h.db.GetUser(orgName)
	if org == nil && ghUser != nil {
		org = &db.User{
			Login:     ghUser.Login,
			Name:      ghUser.Name,
			AvatarURL: ghUser.AvatarURL,
			IsOrg:     true,
		}
	}
	if org == nil {
		org = &db.User{Login: orgName, IsOrg: true}
	}

	repos, _ := h.db.OrgRepos(orgName)

	// Compute aggregate stats and check if any repo is still syncing.
	var totalPRs int
	isSyncing := false
	for _, rp := range repos {
		totalPRs += rp.MergedPRCount
		if rp.SyncStatus == "syncing" || h.worker.IsSyncing(rp.FullName) {
			isSyncing = true
		}
	}

	data := OrgData{
		Org:            org,
		Repos:          repos,
		TotalMergedPRs: totalPRs,
		IsSyncing:      isSyncing,
		Trim:           trim,
	}
	data.ReviewerBoard, _ = h.db.OrgReviewerLeaderboard(orgName, 10)
	data.GatekeeperBoard, _ = h.db.OrgGatekeeperLeaderboard(orgName, 10)

	if points, err := h.db.OrgTimeSeriesData(orgName, cutoffPct); err == nil && len(points) > 0 {
		tp := timeChartPayload{}
		for _, p := range points {
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
		if raw, err := json.Marshal(tp); err == nil {
			data.TimeChartJSON = template.JS(raw)
		}
	}

	data.OGTitle = orgName + " — ngmi"
	data.OGUrl = "https://ngmi.review/org/" + orgName

	data.BaseData = h.baseData(r)
	h.db.RecordVisit("/org/"+orgName, "org", orgName)
	h.render(w, "org", data)
}
