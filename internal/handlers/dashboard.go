package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"inreview/internal/auth"
	"inreview/internal/db"
)

// DashboardData is passed to the dashboard template.
type DashboardData struct {
	BaseData
	Login          string
	AvatarURL      string
	TrackedRepos   []db.Repo
	AvailableRepos []string // full_names from GitHub installation, not yet tracked
	HasInstall     bool
	InstallURL     string
	// Required by layout.html
	OGTitle string
	OGDesc  string
	OGUrl   string
}

// Dashboard shows the user's connected repos and allows tracking/untracking.
func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	login := currentUser(r)
	instID := installationID(r)

	data := DashboardData{
		BaseData:   h.baseData(r),
		Login:      login,
		InstallURL: fmt.Sprintf("https://github.com/apps/%s/installations/new", h.cfg.GitHubAppSlug),
	}

	// Fetch avatar from DB if available.
	if u, err := h.db.GetUser(login); err == nil && u != nil {
		data.AvatarURL = u.AvatarURL
	}

	// Try to find the installation (from session context, or fall back to DB).
	if instID == 0 {
		if id, err := h.db.GetInstallationByLogin(login); err == nil && id != nil {
			instID = *id
		}
	}

	if instID != 0 {
		data.HasInstall = true

		// Load repos already synced for this user's installation first,
		// so we can correctly identify which GitHub repos are not yet tracked.
		if repos, err := h.db.UserOwnedTrackedRepos(login); err == nil {
			data.TrackedRepos = repos
		}

		// Fetch the list of repos accessible via the GitHub App installation
		// and show any that aren't tracked yet.
		if h.cfg.GitHubAppID != 0 && h.cfg.GitHubAppPrivateKey != "" {
			key, err := auth.ParsePrivateKey(h.cfg.GitHubAppPrivateKey)
			if err != nil {
				log.Printf("dashboard: parse private key: %v", err)
			} else {
				appJWT, err := auth.GenerateAppJWT(h.cfg.GitHubAppID, key)
				if err != nil {
					log.Printf("dashboard: generate app JWT: %v", err)
				} else {
					token, _, err := auth.GetInstallationToken(appJWT, fmt.Sprintf("%d", instID))
					if err != nil {
						log.Printf("dashboard: get installation token: %v", err)
					} else {
						repoNames, err := auth.ListInstallationRepos(token)
						if err != nil {
							log.Printf("dashboard: list installation repos: %v", err)
						} else {
							tracked := make(map[string]bool)
							for _, rp := range data.TrackedRepos {
								tracked[rp.FullName] = true
							}
							for _, name := range repoNames {
								if !tracked[name] {
									data.AvailableRepos = append(data.AvailableRepos, name)
								}
							}
						}
					}
				}
			}
		}
	}

	h.render(w, "dashboard", data)
}

// AddRepo triggers a sync for a repo the user has access to via their installation.
func (h *Handler) AddRepo(w http.ResponseWriter, r *http.Request) {
	login := currentUser(r)
	fullName := r.FormValue("repo")
	if fullName == "" || !strings.Contains(fullName, "/") {
		http.Error(w, "invalid repo", http.StatusBadRequest)
		return
	}
	parts := strings.SplitN(fullName, "/", 2)
	owner, name := parts[0], parts[1]

	_ = h.db.UpsertRepo(db.Repo{
		FullName:   fullName,
		Owner:      owner,
		Name:       name,
		OrgName:    owner,
		SyncStatus: "pending",
	})
	if err := h.db.TrackRepoForUser(login, fullName); err != nil {
		log.Printf("dashboard: track repo for user %s: %v", login, err)
	}
	h.worker.Queue(fullName, true)
	log.Printf("dashboard: %s queued %s for sync", login, fullName)

	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// RemoveRepo stops tracking a repo (removes sync data but keeps the entry).
// For simplicity we just redirect back to dashboard; a fuller implementation
// would DELETE the repo and its PRs/reviews from the DB.
func (h *Handler) RemoveRepo(w http.ResponseWriter, r *http.Request) {
	// chi path params are {owner}/{name} but we keep this simple for now.
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}
