package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"inreview/internal/auth"
	"inreview/internal/db"
)

// userFromLogin builds a minimal db.User for upserting avatar on auth.
func userFromLogin(login, avatarURL string) db.User {
	return db.User{Login: login, AvatarURL: avatarURL}
}

// jsonDecode decodes the request body as JSON into v.
func jsonDecode(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// AuthLogin initiates a standard GitHub OAuth login (no app installation).
// Used by the "Login" nav button. After the OAuth code exchange the user
// lands on the dashboard; they can install the GitHub App from there if needed.
func (h *Handler) AuthLogin(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != "" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	if h.cfg.GitHubOAuthClientID == "" {
		h.renderErrorReq(w, r, http.StatusServiceUnavailable,
			"Auth Unavailable", "GitHub OAuth is not configured.")
		return
	}
	state := auth.GenerateOAuthState(r.Context(), h.cache)
	redirectURI := h.cfg.BaseURL + "/auth/github/callback"
	oauthURL := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&state=%s&scope=read:user",
		h.cfg.GitHubOAuthClientID, redirectURI, state,
	)
	http.Redirect(w, r, oauthURL, http.StatusFound)
}

// AuthGitHub initiates the GitHub App installation + OAuth flow.
// Used by the "Install GitHub App" button on the dashboard (after login).
func (h *Handler) AuthGitHub(w http.ResponseWriter, r *http.Request) {
	if h.cfg.GitHubAppSlug == "" {
		h.renderErrorReq(w, r, http.StatusServiceUnavailable,
			"Auth Unavailable", "GitHub App is not configured (GITHUB_APP_SLUG missing).")
		return
	}
	state := auth.GenerateOAuthState(r.Context(), h.cache)
	installURL := fmt.Sprintf("https://github.com/apps/%s/installations/new?state=%s",
		h.cfg.GitHubAppSlug, state)
	http.Redirect(w, r, installURL, http.StatusFound)
}

// AuthGitHubCallback handles:
//  1. App installation callbacks (installation_id + setup_action + optional OAuth code)
//  2. Pure OAuth callbacks (code + state, no installation_id) — e.g. re-login
//
// GitHub sends both installation_id AND an OAuth code+state in one redirect
// when the App has "Request user authorization during installation" enabled.
func (h *Handler) AuthGitHubCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := q.Get("code")
	state := q.Get("state")
	installationIDStr := q.Get("installation_id")
	setupAction := q.Get("setup_action")

	// Validate CSRF state when present (always present for installation flow).
	if state != "" {
		if !auth.ValidateOAuthState(r.Context(), h.cache, state) {
			h.renderErrorReq(w, r, http.StatusBadRequest, "Auth Error",
				"Invalid or expired OAuth state. Please try connecting again.")
			return
		}
	}

	// Exchange OAuth code for user identity.
	var login, avatarURL string
	if code != "" && h.cfg.GitHubOAuthClientID != "" {
		redirectURI := h.cfg.BaseURL + "/auth/github/callback"
		token, err := auth.ExchangeOAuthCode(
			h.cfg.GitHubOAuthClientID,
			h.cfg.GitHubOAuthClientSecret,
			code,
			redirectURI,
		)
		if err != nil {
			log.Printf("auth: exchange OAuth code: %v", err)
			h.renderErrorReq(w, r, http.StatusInternalServerError, "Auth Error",
				"Could not exchange OAuth code with GitHub.")
			return
		}
		login, avatarURL, err = auth.FetchGitHubLogin(token)
		if err != nil {
			log.Printf("auth: fetch GitHub login: %v", err)
			h.renderErrorReq(w, r, http.StatusInternalServerError, "Auth Error",
				"Could not retrieve your GitHub profile.")
			return
		}
	}

	// Fall back to existing session if no OAuth code (e.g. installation webhook redirect).
	if login == "" {
		login = currentUser(r)
	}

	if login == "" {
		h.renderErrorReq(w, r, http.StatusBadRequest, "Auth Error",
			"Could not identify your GitHub account. Please try again.")
		return
	}

	// Handle App installation.
	var installID *int64
	if installationIDStr != "" && (setupAction == "install" || setupAction == "update") {
		id, err := strconv.ParseInt(installationIDStr, 10, 64)
		if err == nil {
			installID = &id
			if err := h.db.UpsertInstallation(id, login); err != nil {
				log.Printf("auth: upsert installation %d: %v", id, err)
			}
			// Store avatar if we got it from OAuth
			if avatarURL != "" {
				_ = h.db.UpsertUser(userFromLogin(login, avatarURL))
			}
		}
	}

	// Create session (7-day expiry).
	sessionID := auth.GenerateSessionID()
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	if err := h.db.CreateSession(sessionID, login, installID, expiresAt); err != nil {
		log.Printf("auth: create session: %v", err)
		h.renderErrorReq(w, r, http.StatusInternalServerError, "Auth Error",
			"Could not create session.")
		return
	}

	secure := strings.HasPrefix(h.cfg.BaseURL, "https")
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60,
		Path:     "/",
	})

	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// AuthLogout clears the session from DB and deletes the cookie.
func (h *Handler) AuthLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("session_id"); err == nil {
		if err := h.db.DeleteSession(cookie.Value); err != nil {
			log.Printf("auth: delete session: %v", err)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    "",
		HttpOnly: true,
		MaxAge:   -1,
		Path:     "/",
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// GitHubWebhook handles GitHub App webhook events (install/uninstall).
func (h *Handler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	event := r.Header.Get("X-GitHub-Event")
	if event != "installation" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var payload struct {
		Action       string `json:"action"`
		Installation struct {
			ID      int64 `json:"id"`
			Account struct {
				Login string `json:"login"`
			} `json:"account"`
		} `json:"installation"`
		Sender struct {
			Login string `json:"login"`
		} `json:"sender"`
	}

	if err := jsonDecode(r, &payload); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}

	switch payload.Action {
	case "created":
		// Store under the account login (org or user).
		if err := h.db.UpsertInstallation(payload.Installation.ID, payload.Installation.Account.Login); err != nil {
			log.Printf("webhook: upsert installation %d: %v", payload.Installation.ID, err)
		}
		// If installed by a different user (e.g. user installing on an org),
		// also store under the sender's login so their dashboard can find it.
		if payload.Sender.Login != "" && payload.Sender.Login != payload.Installation.Account.Login {
			if err := h.db.UpsertInstallationForUser(payload.Installation.ID, payload.Sender.Login); err != nil {
				log.Printf("webhook: link installation %d to sender %s: %v", payload.Installation.ID, payload.Sender.Login, err)
			}
		}
	case "deleted":
		if err := h.db.DeactivateInstallation(payload.Installation.ID); err != nil {
			log.Printf("webhook: deactivate installation %d: %v", payload.Installation.ID, err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}
