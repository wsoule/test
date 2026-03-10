package github

import "time"

// ── Public types (used by worker and handlers) ────────────────────────────────

type GHRepo struct {
	FullName    string `json:"full_name"`
	Name        string `json:"name"`
	Owner       struct {
		Login string `json:"login"`
	} `json:"owner"`
	Description string `json:"description"`
	Stars       int    `json:"stargazers_count"`
	Language    string `json:"language"`
}

// GHUser is returned by GetUser and embedded in other types.
type GHUser struct {
	Login       string `json:"login"`
	Name        string `json:"name"`
	AvatarURL   string `json:"avatar_url"`
	Bio         string `json:"bio"`
	PublicRepos int    `json:"public_repos"`
	Followers   int    `json:"followers"`
	Company     string `json:"company"`
	Location    string `json:"location"`
	Type        string `json:"type"` // "User" or "Organization"
}

// GHReview is a single review event on a PR.
type GHReview struct {
	ID          int64 `json:"id"`
	User        struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	} `json:"user"`
	State       string    `json:"state"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// GHPRWithReviews is a merged PR with its reviews pre-fetched (returned by SyncRepo).
type GHPRWithReviews struct {
	Number    int
	Title     string
	Author    string // login
	CreatedAt time.Time
	MergedAt  *time.Time
	Reviews   []GHReview
	Additions int
	Deletions int
}

// SyncResult is the full output of one GraphQL-based repo sync.
type SyncResult struct {
	Repo  GHRepo
	Owner GHUser
	PRs   []GHPRWithReviews
}

// ── Internal GraphQL response types ──────────────────────────────────────────

type gqlSyncResponse struct {
	Repository *gqlSyncRepo  `json:"repository"`
	RateLimit  *gqlRateLimit `json:"rateLimit"`
}

type gqlSyncRepo struct {
	NameWithOwner   string    `json:"nameWithOwner"`
	Description     string    `json:"description"`
	StargazerCount  int       `json:"stargazerCount"`
	PrimaryLanguage *gqlLang  `json:"primaryLanguage"`
	Owner           gqlOwner  `json:"owner"`
	PullRequests    gqlPRPage `json:"pullRequests"`
}

type gqlLang struct {
	Name string `json:"name"`
}

type gqlOwner struct {
	Login        string `json:"login"`
	Typename     string `json:"__typename"`
	AvatarURL    string `json:"avatarUrl"`
	Name         string `json:"name"`
	Bio          string `json:"bio"`
	Company      string `json:"company"`
	Location     string `json:"location"`
	Followers    struct{ TotalCount int `json:"totalCount"` } `json:"followers"`
	Repositories struct{ TotalCount int `json:"totalCount"` } `json:"repositories"`
}

type gqlPRPage struct {
	PageInfo struct {
		HasNextPage bool   `json:"hasNextPage"`
		EndCursor   string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []gqlPR `json:"nodes"`
}

type gqlPR struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	CreatedAt time.Time  `json:"createdAt"`
	MergedAt  *time.Time `json:"mergedAt"`
	Additions int        `json:"additions"`
	Deletions int        `json:"deletions"`
	Author    *gqlActor  `json:"author"`
	Reviews   struct {
		Nodes []gqlReview `json:"nodes"`
	} `json:"reviews"`
}

type gqlActor struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl"`
}

type gqlReview struct {
	DatabaseID  int64      `json:"databaseId"`
	Author      *gqlActor  `json:"author"`
	State       string     `json:"state"`
	SubmittedAt *time.Time `json:"submittedAt"`
}

type gqlRateLimit struct {
	Remaining int `json:"remaining"`
	Cost      int `json:"cost"`
}

type gqlGetRepoResponse struct {
	Repository *struct {
		NameWithOwner   string   `json:"nameWithOwner"`
		Description     string   `json:"description"`
		StargazerCount  int      `json:"stargazerCount"`
		PrimaryLanguage *gqlLang `json:"primaryLanguage"`
		Owner           struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

type gqlUserResponse struct {
	RepositoryOwner *struct {
		Login        string `json:"login"`
		AvatarURL    string `json:"avatarUrl"`
		Typename     string `json:"__typename"`
		Name         string `json:"name"`
		Bio          string `json:"bio"`
		Company      string `json:"company"`
		Location     string `json:"location"`
		Followers    struct{ TotalCount int `json:"totalCount"` } `json:"followers"`
		Repositories struct{ TotalCount int `json:"totalCount"` } `json:"repositories"`
	} `json:"repositoryOwner"`
}

type gqlRepoNode struct {
	NameWithOwner   string   `json:"nameWithOwner"`
	Name            string   `json:"name"`
	Owner           struct{ Login string `json:"login"` } `json:"owner"`
	Description     string   `json:"description"`
	StargazerCount  int      `json:"stargazerCount"`
	PrimaryLanguage *gqlLang `json:"primaryLanguage"`
}

type gqlOrgReposResponse struct {
	Organization *struct {
		Repositories struct{ Nodes []gqlRepoNode `json:"nodes"` } `json:"repositories"`
	} `json:"organization"`
}

type gqlUserReposResponse struct {
	User *struct {
		Repositories struct{ Nodes []gqlRepoNode `json:"nodes"` } `json:"repositories"`
	} `json:"user"`
}
