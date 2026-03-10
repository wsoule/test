// seed queues a list of repos for sync via the InReview worker.
// Run while the main server is running — the server's workers do the actual syncing.
//
//	go run ./cmd/seed
package main

import (
	"context"
	"log"
	"time"

	"github.com/joho/godotenv"

	"inreview/internal/config"
	"inreview/internal/db"
	"inreview/internal/github"
	"inreview/internal/rdb"
	"inreview/internal/worker"
)

var repos = []string{
	"golang/go",
	"facebook/react",
	"microsoft/vscode",
	"kubernetes/kubernetes",
	"vercel/next.js",
	"rust-lang/rust",
	"vuejs/vue",
	"angular/angular",
	"django/django",
	"rails/rails",
	"tensorflow/tensorflow",
	"pytorch/pytorch",
	"denoland/deno",
	"astro-build/astro",
	"sveltejs/svelte",
	"laravel/laravel",
	"expressjs/express",
	"fastapi/fastapi",
	"tailwindlabs/tailwindcss",
	"vitejs/vite",
}

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	// Open DB read-only to check which repos are already synced.
	// We do NOT start workers here — the main server owns all SQLite writes.
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
	ghClient := github.NewClient(cfg.GitHubToken)
	w := worker.New(ghClient, database, cache, cfg.GitHubAppID, cfg.GitHubAppPrivateKey)

	queued := 0
	for _, repo := range repos {
		existing, _ := database.GetRepo(repo)
		if existing != nil && existing.SyncStatus == "done" {
			log.Printf("skip %s (already synced)", repo)
			continue
		}
		w.Queue(repo, false)
		log.Printf("queued %s", repo)
		queued++
		time.Sleep(200 * time.Millisecond)
	}

	if queued == 0 {
		log.Println("nothing to seed — all repos already synced")
		return
	}

	log.Printf("queued %d repos — main server workers will process them", queued)

	// Poll Redis until all enqueued repos are no longer in-progress.
	ctx := context.Background()
	for {
		pending := 0
		for _, repo := range repos {
			if cache.QIsInProgress(ctx, repo) {
				pending++
			}
		}
		if pending == 0 {
			break
		}
		log.Printf("waiting… %d repo(s) still syncing", pending)
		time.Sleep(10 * time.Second)
	}
	log.Println("seed complete")
}
