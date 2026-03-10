package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrRateLimited = errors.New("rate limited")
)

const (
	prsPerPage    = 50
	reviewsPerPR  = 30
	rateLimitWarn = 50               // pause when remaining points drop below this
	rateLimitWait = 61 * time.Second // how long to pause
)

type Client struct {
	token      string
	httpClient *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// WithToken returns a new Client that shares the same HTTP client but uses a
// different auth token. Used by the worker to swap in installation tokens.
func (c *Client) WithToken(token string) *Client {
	return &Client{token: token, httpClient: c.httpClient}
}

// ── Transport ─────────────────────────────────────────────────────────────────

// graphql POSTs a GraphQL query to GitHub and decodes the data field into v.
func (c *Client) graphql(ctx context.Context, query string, variables map[string]interface{}, v interface{}) error {
	payload, err := json.Marshal(struct {
		Query     string                 `json:"query"`
		Variables map[string]interface{} `json:"variables,omitempty"`
	}{Query: query, Variables: variables})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.github.com/graphql", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "inreview.dev/1.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return errors.New("GitHub token invalid or expired")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return ErrRateLimited
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("GitHub GraphQL HTTP %s", resp.Status)
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("GraphQL: %s", envelope.Errors[0].Message)
	}
	if v == nil {
		return nil
	}
	return json.Unmarshal(envelope.Data, v)
}

// get performs a REST GET — kept only for search endpoints which have no GraphQL equivalent.
func (c *Client) get(ctx context.Context, path string, v interface{}) error {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "inreview.dev/1.0")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusForbidden, http.StatusTooManyRequests:
		return ErrRateLimited
	case http.StatusUnauthorized:
		return errors.New("GitHub token invalid or expired")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("GitHub API error: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ── GraphQL queries ───────────────────────────────────────────────────────────

const syncRepoQuery = `
query SyncRepo($owner: String!, $name: String!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    nameWithOwner description stargazerCount
    primaryLanguage { name }
    owner {
      login avatarUrl __typename
      ... on User         { name bio company location followers { totalCount } repositories(privacy: PUBLIC) { totalCount } }
      ... on Organization { name                                               repositories(privacy: PUBLIC) { totalCount } }
    }
    pullRequests(states: [MERGED], first: 50, after: $cursor, orderBy: {field: UPDATED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number title createdAt mergedAt additions deletions
        author { login }
        reviews(first: 30) {
          nodes { databaseId state submittedAt author { login avatarUrl } }
        }
      }
    }
  }
  rateLimit { remaining cost }
}`

const getRepoQuery = `
query GetRepo($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    nameWithOwner description stargazerCount
    primaryLanguage { name }
    owner { login }
  }
}`

const getUserQuery = `
query GetUser($login: String!) {
  repositoryOwner(login: $login) {
    login avatarUrl __typename
    ... on User         { name bio company location followers { totalCount } repositories(privacy: PUBLIC) { totalCount } }
    ... on Organization { name                                               repositories(privacy: PUBLIC) { totalCount } }
  }
}`

const getOrgReposQuery = `
query GetOrgRepos($login: String!, $first: Int!) {
  organization(login: $login) {
    repositories(first: $first, orderBy: {field: STARGAZERS, direction: DESC}, privacy: PUBLIC) {
      nodes { nameWithOwner name owner { login } description stargazerCount primaryLanguage { name } }
    }
  }
}`

const getUserReposQuery = `
query GetUserRepos($login: String!, $first: Int!) {
  user(login: $login) {
    repositories(first: $first, orderBy: {field: STARGAZERS, direction: DESC}, privacy: PUBLIC, isFork: false) {
      nodes { nameWithOwner name owner { login } description stargazerCount primaryLanguage { name } }
    }
  }
}`

const getRepoContributorsQuery = `
query GetRepoContributors($owner: String!, $name: String!, $first: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequests(states: [MERGED], first: $first, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {
        author { login }
        reviews(first: 10) {
          nodes { author { login } }
        }
      }
    }
  }
}`

// ── Public API ────────────────────────────────────────────────────────────────

// SyncRepo fetches repo metadata + all merged PRs with embedded reviews using
// paginated GraphQL. This replaces separate GetRepo + GetMergedPRs + GetPRReviews
// calls, reducing ~500 HTTP requests per repo to ~10.
func (c *Client) SyncRepo(ctx context.Context, owner, name string, maxPRs int) (*SyncResult, error) {
	result := &SyncResult{}
	var cursor *string
	firstPage := true

	for len(result.PRs) < maxPRs {
		vars := map[string]interface{}{
			"owner":  owner,
			"name":   name,
			"cursor": cursor,
		}
		var data gqlSyncResponse
		if err := c.graphql(ctx, syncRepoQuery, vars, &data); err != nil {
			return nil, err
		}
		if data.Repository == nil {
			return nil, ErrNotFound
		}
		repo := data.Repository

		// Capture repo + owner metadata from the first page only.
		if firstPage {
			lang := ""
			if repo.PrimaryLanguage != nil {
				lang = repo.PrimaryLanguage.Name
			}
			result.Repo = GHRepo{
				FullName:    repo.NameWithOwner,
				Name:        name,
				Description: repo.Description,
				Stars:       repo.StargazerCount,
				Language:    lang,
			}
			result.Repo.Owner.Login = repo.Owner.Login

			result.Owner = GHUser{
				Login:       repo.Owner.Login,
				Name:        repo.Owner.Name,
				AvatarURL:   repo.Owner.AvatarURL,
				Bio:         repo.Owner.Bio,
				Company:     repo.Owner.Company,
				Location:    repo.Owner.Location,
				Type:        repo.Owner.Typename,
				PublicRepos: repo.Owner.Repositories.TotalCount,
				Followers:   repo.Owner.Followers.TotalCount,
			}
			firstPage = false
		}

		// Pause if rate limit is low.
		if rl := data.RateLimit; rl != nil && rl.Remaining < rateLimitWarn {
			log.Printf("[graphql] rate limit low (%d remaining) — pausing %s", rl.Remaining, rateLimitWait)
			time.Sleep(rateLimitWait)
		}

		// Map PR nodes.
		for _, node := range repo.PullRequests.Nodes {
			pr := GHPRWithReviews{
				Number:    node.Number,
				Title:     node.Title,
				CreatedAt: node.CreatedAt,
				MergedAt:  node.MergedAt,
				Additions: node.Additions,
				Deletions: node.Deletions,
			}
			if node.Author != nil {
				pr.Author = node.Author.Login
			}

			for _, rev := range node.Reviews.Nodes {
				r := GHReview{
					ID:    rev.DatabaseID,
					State: rev.State,
				}
				if rev.SubmittedAt != nil {
					r.SubmittedAt = *rev.SubmittedAt
				}
				if rev.Author != nil {
					r.User.Login = rev.Author.Login
					r.User.AvatarURL = rev.Author.AvatarURL
				}
				pr.Reviews = append(pr.Reviews, r)
			}

			result.PRs = append(result.PRs, pr)
			if len(result.PRs) >= maxPRs {
				break
			}
		}

		if !repo.PullRequests.PageInfo.HasNextPage || len(result.PRs) >= maxPRs {
			break
		}
		cur := repo.PullRequests.PageInfo.EndCursor
		cursor = &cur
	}

	return result, nil
}

// GetRepo fetches basic repository metadata.
func (c *Client) GetRepo(ctx context.Context, owner, name string) (*GHRepo, error) {
	var data gqlGetRepoResponse
	if err := c.graphql(ctx, getRepoQuery, map[string]interface{}{
		"owner": owner, "name": name,
	}, &data); err != nil {
		return nil, err
	}
	if data.Repository == nil {
		return nil, ErrNotFound
	}
	r := data.Repository
	lang := ""
	if r.PrimaryLanguage != nil {
		lang = r.PrimaryLanguage.Name
	}
	repo := &GHRepo{
		FullName:    r.NameWithOwner,
		Name:        name,
		Description: r.Description,
		Stars:       r.StargazerCount,
		Language:    lang,
	}
	repo.Owner.Login = r.Owner.Login
	return repo, nil
}

// GetUser fetches a user or org by login.
func (c *Client) GetUser(ctx context.Context, login string) (*GHUser, error) {
	var data gqlUserResponse
	if err := c.graphql(ctx, getUserQuery, map[string]interface{}{
		"login": login,
	}, &data); err != nil {
		return nil, err
	}
	if data.RepositoryOwner == nil {
		return nil, ErrNotFound
	}
	u := data.RepositoryOwner
	return &GHUser{
		Login:       u.Login,
		Name:        u.Name,
		AvatarURL:   u.AvatarURL,
		Bio:         u.Bio,
		Company:     u.Company,
		Location:    u.Location,
		Type:        u.Typename,
		PublicRepos: u.Repositories.TotalCount,
		Followers:   u.Followers.TotalCount,
	}, nil
}

// GetOrgRepos fetches top public repos for an org sorted by stars.
func (c *Client) GetOrgRepos(ctx context.Context, org string, limit int) ([]GHRepo, error) {
	var data gqlOrgReposResponse
	if err := c.graphql(ctx, getOrgReposQuery, map[string]interface{}{
		"login": org, "first": limit,
	}, &data); err != nil {
		return nil, err
	}
	if data.Organization == nil {
		return nil, ErrNotFound
	}
	return mapRepoNodes(data.Organization.Repositories.Nodes), nil
}

// GetUserRepos fetches top public repos for a user sorted by stars.
func (c *Client) GetUserRepos(ctx context.Context, username string, limit int) ([]GHRepo, error) {
	var data gqlUserReposResponse
	if err := c.graphql(ctx, getUserReposQuery, map[string]interface{}{
		"login": username, "first": limit,
	}, &data); err != nil {
		return nil, err
	}
	if data.User == nil {
		return nil, ErrNotFound
	}
	return mapRepoNodes(data.User.Repositories.Nodes), nil
}

// GetRepoContributors returns distinct logins of PR authors + reviewers
// from the most recent prLimit merged PRs.
func (c *Client) GetRepoContributors(ctx context.Context, owner, name string, prLimit int) ([]string, error) {
	var data struct {
		Repository *struct {
			PullRequests struct {
				Nodes []struct {
					Author  *gqlActor `json:"author"`
					Reviews struct {
						Nodes []struct {
							Author *gqlActor `json:"author"`
						} `json:"nodes"`
					} `json:"reviews"`
				} `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"repository"`
	}
	if err := c.graphql(ctx, getRepoContributorsQuery, map[string]interface{}{
		"owner": owner, "name": name, "first": prLimit,
	}, &data); err != nil {
		return nil, err
	}
	if data.Repository == nil {
		return nil, ErrNotFound
	}
	seen := make(map[string]bool)
	var logins []string
	for _, pr := range data.Repository.PullRequests.Nodes {
		if pr.Author != nil && pr.Author.Login != "" && !seen[pr.Author.Login] {
			seen[pr.Author.Login] = true
			logins = append(logins, pr.Author.Login)
		}
		for _, rev := range pr.Reviews.Nodes {
			if rev.Author != nil && rev.Author.Login != "" && !seen[rev.Author.Login] {
				seen[rev.Author.Login] = true
				logins = append(logins, rev.Author.Login)
			}
		}
	}
	return logins, nil
}

// SearchRepos searches GitHub for repositories (REST — search API has no GraphQL equivalent).
func (c *Client) SearchRepos(ctx context.Context, query string, limit int) ([]GHRepo, error) {
	var result struct {
		Items []GHRepo `json:"items"`
	}
	path := fmt.Sprintf("/search/repositories?q=%s+fork:false&sort=stars&order=desc&per_page=%d",
		url.QueryEscape(query), limit)
	if err := c.get(ctx, path, &result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

// GetReviewedRepos returns the distinct repo full names where login has reviewed a PR (REST search).
func (c *Client) GetReviewedRepos(ctx context.Context, login string, limit int) ([]string, error) {
	var result struct {
		Items []struct {
			RepositoryURL string `json:"repository_url"`
		} `json:"items"`
	}
	path := fmt.Sprintf("/search/issues?q=type:pr+reviewed-by:%s&sort=updated&order=desc&per_page=%d",
		url.QueryEscape(login), limit)
	if err := c.get(ctx, path, &result); err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var repos []string
	for _, item := range result.Items {
		// repository_url is like https://api.github.com/repos/owner/repo
		const prefix = "https://api.github.com/repos/"
		fullName := strings.TrimPrefix(item.RepositoryURL, prefix)
		if fullName != "" && !seen[fullName] {
			seen[fullName] = true
			repos = append(repos, fullName)
		}
	}
	return repos, nil
}

// SearchUsers searches GitHub for users and orgs (REST).
func (c *Client) SearchUsers(ctx context.Context, query string, limit int) ([]GHUser, error) {
	var result struct {
		Items []GHUser `json:"items"`
	}
	path := fmt.Sprintf("/search/users?q=%s&per_page=%d", url.QueryEscape(query), limit)
	if err := c.get(ctx, path, &result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func mapRepoNodes(nodes []gqlRepoNode) []GHRepo {
	out := make([]GHRepo, 0, len(nodes))
	for _, n := range nodes {
		lang := ""
		if n.PrimaryLanguage != nil {
			lang = n.PrimaryLanguage.Name
		}
		r := GHRepo{
			FullName:    n.NameWithOwner,
			Name:        n.Name,
			Description: n.Description,
			Stars:       n.StargazerCount,
			Language:    lang,
		}
		r.Owner.Login = n.Owner.Login
		out = append(out, r)
	}
	return out
}
