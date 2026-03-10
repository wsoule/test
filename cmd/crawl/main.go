// crawl discovers GitHub repos via BFS graph expansion and queues them for sync.
// State is saved after each repo so the crawl can be stopped and resumed with
// --state-file. Use --rate to stay within your GitHub API quota across hours.
//
//	railway run go run ./cmd/crawl
//	railway run go run ./cmd/crawl --rate 4500 --state-file crawl.json --depth 3
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"inreview/internal/config"
	"inreview/internal/db"
	"inreview/internal/github"
	"inreview/internal/rdb"
	"inreview/internal/worker"
)

const defaultSeeds = "golang/go,facebook/react,microsoft/vscode,kubernetes/kubernetes," +
	"vercel/next.js,rust-lang/rust,vuejs/vue,angular/angular,django/django,rails/rails," +
	"tensorflow/tensorflow,pytorch/pytorch,denoland/deno,astro-build/astro,sveltejs/svelte," +
	"laravel/laravel,expressjs/express,fastapi/fastapi,tailwindlabs/tailwindcss,vitejs/vite"

// crawlState is the full BFS state written to disk after each repo is processed.
// Load it on restart to pick up exactly where the crawl left off.
type crawlState struct {
	VisitedRepos map[string]bool   `json:"visited_repos"`
	VisitedOrgs  map[string]bool   `json:"visited_orgs"`
	VisitedUsers map[string]bool   `json:"visited_users"`
	OwnerTypes   map[string]string `json:"owner_types"`
	Pending      []string          `json:"pending"`   // repos left to process in current depth
	Next         []string          `json:"next"`      // repos discovered for next depth
	Depth        int               `json:"depth"`
	MaxDepth     int               `json:"max_depth"`
}

func main() {
	seedsFlag := flag.String("seeds", defaultSeeds, "comma-separated owner/repo seeds (ignored if --state-file exists)")
	maxDepth := flag.Int("depth", 2, "BFS hops from seed repos (ignored if --state-file exists)")
	orgLimit := flag.Int("org-limit", 20, "max repos fetched per org")
	userLimit := flag.Int("user-limit", 10, "max repos fetched per user (owned + reviewed)")
	prLimit := flag.Int("pr-limit", 50, "PRs to scan per repo for contributor discovery")
	rate := flag.Int("rate", 4500, "GitHub API requests per hour (leave headroom for main server workers)")
	stateFile := flag.String("state-file", "", "path to JSON state file for resumable crawl")
	flag.Parse()

	callSleep := time.Hour / time.Duration(*rate)
	log.Printf("rate: %d req/hr → %.0fms between calls", *rate, callSleep.Seconds()*1000)

	_ = godotenv.Load()
	cfg := config.Load()

	database, err := db.New(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	cache, err := rdb.New(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to redis: %v", err)
	}
	defer cache.Close()

	// Worker is used only to push to the Redis queue — Start() is intentionally
	// not called so no extra goroutines compete with the main server's workers.
	gh := github.NewClient(cfg.GitHubToken)
	w := worker.New(gh, database, cache, cfg.GitHubAppID, cfg.GitHubAppPrivateKey)

	ctx := context.Background()

	state := initState(*seedsFlag, *maxDepth, *stateFile)
	log.Printf("starting at depth %d/%d — %d repos pending, %d already visited",
		state.Depth, state.MaxDepth, len(state.Pending), len(state.VisitedRepos))

	save := func() {
		if *stateFile != "" {
			if err := saveState(*stateFile, state); err != nil {
				log.Printf("warn: failed to save state: %v", err)
			}
		}
	}

	for len(state.Pending) > 0 {
		repo := state.Pending[0]
		state.Pending = state.Pending[1:]

		if state.VisitedRepos[repo] {
			continue
		}
		state.VisitedRepos[repo] = true

		w.Queue(repo, false)

		if state.Depth < state.MaxDepth {
			parts := strings.SplitN(repo, "/", 2)
			if len(parts) != 2 {
				log.Printf("skip malformed repo %q", repo)
				save()
				continue
			}
			owner, name := parts[0], parts[1]

			// Expand org/user — one lookup per unique owner across all repos.
			if !state.VisitedOrgs[owner] {
				state.VisitedOrgs[owner] = true

				if _, known := state.OwnerTypes[owner]; !known {
					time.Sleep(callSleep)
					u, err := gh.GetUser(ctx, owner)
					if err != nil {
						log.Printf("GetUser %s: %v", owner, err)
						state.OwnerTypes[owner] = "User"
					} else {
						state.OwnerTypes[owner] = u.Type
					}
				}

				time.Sleep(callSleep)
				var orgRepos []github.GHRepo
				if state.OwnerTypes[owner] == "Organization" {
					orgRepos, err = gh.GetOrgRepos(ctx, owner, *orgLimit)
				} else {
					orgRepos, err = gh.GetUserRepos(ctx, owner, *orgLimit)
				}
				if err != nil {
					log.Printf("repos for %s: %v", owner, err)
				} else {
					for _, r := range orgRepos {
						state.Next = append(state.Next, r.FullName)
					}
				}
			}

			// Discover contributors then find their repos.
			time.Sleep(callSleep)
			contributors, err := gh.GetRepoContributors(ctx, owner, name, *prLimit)
			if err != nil {
				log.Printf("contributors %s/%s: %v", owner, name, err)
			} else {
				for _, login := range contributors {
					if state.VisitedUsers[login] {
						continue
					}
					state.VisitedUsers[login] = true

					time.Sleep(callSleep)
					userRepos, err := gh.GetUserRepos(ctx, login, *userLimit)
					if err != nil {
						log.Printf("user repos %s: %v", login, err)
					} else {
						for _, r := range userRepos {
							state.Next = append(state.Next, r.FullName)
						}
					}

					time.Sleep(callSleep)
					reviewed, err := gh.GetReviewedRepos(ctx, login, *userLimit)
					if err != nil {
						log.Printf("reviewed repos %s: %v", login, err)
					} else {
						state.Next = append(state.Next, reviewed...)
					}
				}
			}
		}

		log.Printf("[depth %d] expanded %q — %d pending, %d next, %d visited total",
			state.Depth, repo, len(state.Pending), len(state.Next), len(state.VisitedRepos))

		save()

		// Advance depth when current wave is exhausted.
		if len(state.Pending) == 0 {
			if state.Depth >= state.MaxDepth || len(state.Next) == 0 {
				break
			}
			state.Depth++
			state.Pending = deduplicate(state.Next)
			state.Next = nil
			log.Printf("[advancing to depth %d] %d repos in frontier", state.Depth, len(state.Pending))
			save()
		}
	}

	log.Printf("crawl complete — %d repos visited, %d orgs, %d users",
		len(state.VisitedRepos), len(state.VisitedOrgs), len(state.VisitedUsers))
}

func initState(seeds string, maxDepth int, stateFile string) *crawlState {
	if stateFile != "" {
		if data, err := os.ReadFile(stateFile); err == nil {
			var s crawlState
			if err := json.Unmarshal(data, &s); err == nil {
				log.Printf("resumed state from %s", stateFile)
				return &s
			}
		}
	}
	return &crawlState{
		VisitedRepos: make(map[string]bool),
		VisitedOrgs:  make(map[string]bool),
		VisitedUsers: make(map[string]bool),
		OwnerTypes:   make(map[string]string),
		Pending:      parseSeeds(seeds),
		MaxDepth:     maxDepth,
	}
}

func saveState(path string, s *crawlState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parseSeeds(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func deduplicate(repos []string) []string {
	seen := make(map[string]bool, len(repos))
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}
