package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"inreview/internal/db"
)

const dataLimit = 50

// DataExplorerData is passed to the data page template and all data-tab partials.
type DataExplorerData struct {
	BaseData
	ActiveTab    string
	Repos        []db.Repo
	ReposTotal   int
	PRs          []db.PullRequest
	PRsTotal     int
	Reviews      []db.Review
	ReviewsTotal int
	Users        []db.User
	UsersTotal   int
	Page         int
	Offset       int
	HasPrev      bool
	HasNext      bool
	PrevURL      string
	NextURL      string
	Search       string
	SortBy       string
	Status       string
	Author       string
	Reviewer     string
	State        string
	RepoFilter   string
	OGTitle      string
	OGDesc       string
	OGUrl        string
}

func parseDataQuery(r *http.Request) (page, offset int, search, sortBy, status, author, reviewer, state, repo string) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	offset = page * dataLimit
	search = r.URL.Query().Get("search")
	sortBy = r.URL.Query().Get("sort")
	status = r.URL.Query().Get("status")
	author = r.URL.Query().Get("author")
	reviewer = r.URL.Query().Get("reviewer")
	state = r.URL.Query().Get("state")
	repo = r.URL.Query().Get("repo")
	return
}

func setPagination(d *DataExplorerData, baseURL string, total, page, offset int, extra string) {
	d.HasPrev = page > 0
	d.HasNext = offset+dataLimit < total
	if d.HasPrev {
		d.PrevURL = fmt.Sprintf("%s?page=%d%s", baseURL, page-1, extra)
	}
	if d.HasNext {
		d.NextURL = fmt.Sprintf("%s?page=%d%s", baseURL, page+1, extra)
	}
}

func (h *Handler) DataExplorer(w http.ResponseWriter, r *http.Request) {
	page, offset, search, sortBy, status, _, _, _, _ := parseDataQuery(r)
	if status == "" {
		status = "done" // default to showing only synced repos on initial load
	}
	repos, total, _ := h.db.ListReposFiltered(dataLimit, offset, sortBy, search, status)

	extra := ""
	if search != "" {
		extra += "&search=" + search
	}
	if sortBy != "" {
		extra += "&sort=" + sortBy
	}
	if status != "" {
		extra += "&status=" + status
	}

	data := DataExplorerData{
		BaseData:   h.baseData(r),
		ActiveTab:  "repos",
		Repos:      repos,
		ReposTotal: total,
		Page:       page,
		Offset:     offset,
		Search:     search,
		SortBy:     sortBy,
		Status:     status,
		OGTitle:    "Data Explorer — ngmi",
		OGDesc:     "Browse all tracked repos, pull requests, reviews, and users.",
		OGUrl:      "https://ngmi.review/data",
	}
	setPagination(&data, "/data/repos", total, page, offset, extra)
	h.render(w, "data", data)
}

func (h *Handler) DataRepos(w http.ResponseWriter, r *http.Request) {
	page, offset, search, sortBy, status, _, _, _, _ := parseDataQuery(r)
	repos, total, _ := h.db.ListReposFiltered(dataLimit, offset, sortBy, search, status)

	extra := ""
	if search != "" {
		extra += "&search=" + search
	}
	if sortBy != "" {
		extra += "&sort=" + sortBy
	}
	if status != "" {
		extra += "&status=" + status
	}

	data := DataExplorerData{
		ActiveTab:  "repos",
		Repos:      repos,
		ReposTotal: total,
		Page:       page,
		Offset:     offset,
		Search:     search,
		SortBy:     sortBy,
		Status:     status,
	}
	setPagination(&data, "/data/repos", total, page, offset, extra)
	h.renderPartial(w, "data_repos", data)
}

func (h *Handler) DataPRs(w http.ResponseWriter, r *http.Request) {
	page, offset, _, sortBy, _, author, _, _, repo := parseDataQuery(r)
	prs, total, _ := h.db.ListPRsFiltered(dataLimit, offset, repo, author, sortBy)

	extra := ""
	if author != "" {
		extra += "&author=" + author
	}
	if sortBy != "" {
		extra += "&sort=" + sortBy
	}
	if repo != "" {
		extra += "&repo=" + repo
	}

	data := DataExplorerData{
		ActiveTab:  "prs",
		PRs:        prs,
		PRsTotal:   total,
		Page:       page,
		Offset:     offset,
		Author:     author,
		SortBy:     sortBy,
		RepoFilter: repo,
	}
	setPagination(&data, "/data/prs", total, page, offset, extra)
	h.renderPartial(w, "data_prs", data)
}

func (h *Handler) DataReviews(w http.ResponseWriter, r *http.Request) {
	page, offset, _, _, _, _, reviewer, state, _ := parseDataQuery(r)
	reviews, total, _ := h.db.ListReviewsFiltered(dataLimit, offset, reviewer, state)

	extra := ""
	if reviewer != "" {
		extra += "&reviewer=" + reviewer
	}
	if state != "" {
		extra += "&state=" + state
	}

	data := DataExplorerData{
		ActiveTab:    "reviews",
		Reviews:      reviews,
		ReviewsTotal: total,
		Page:         page,
		Offset:       offset,
		Reviewer:     reviewer,
		State:        state,
	}
	setPagination(&data, "/data/reviews", total, page, offset, extra)
	h.renderPartial(w, "data_reviews", data)
}

func (h *Handler) DataUsers(w http.ResponseWriter, r *http.Request) {
	page, offset, search, _, _, _, _, _, _ := parseDataQuery(r)
	users, total, _ := h.db.ListUsersFiltered(dataLimit, offset, search)

	extra := ""
	if search != "" {
		extra += "&search=" + search
	}

	data := DataExplorerData{
		ActiveTab:  "users",
		Users:      users,
		UsersTotal: total,
		Page:       page,
		Offset:     offset,
		Search:     search,
	}
	setPagination(&data, "/data/users", total, page, offset, extra)
	h.renderPartial(w, "data_users", data)
}
