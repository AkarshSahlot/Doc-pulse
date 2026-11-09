package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/AkarshSahlot/docpulse/internal/config"
	"github.com/AkarshSahlot/docpulse/internal/demo"
	"github.com/AkarshSahlot/docpulse/internal/llm"
	"github.com/AkarshSahlot/docpulse/internal/mapping"
	"github.com/AkarshSahlot/docpulse/internal/store"
	"github.com/AkarshSahlot/docpulse/internal/vcs"
	"github.com/gin-gonic/gin"
	ghWebhooks "github.com/go-playground/webhooks/v6/github"
)

//go:embed dashboard.html
var dashboardHTML string

const demoCooldown = 10 * time.Second

func main() {
	ctx := context.Background()

	cfg, err := config.MustLoadForServer()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	rules, err := mapping.Load(cfg.MappingFile)
	if err != nil {
		log.Fatalf("failed to load mapping file: %v", err)
	}

	llmClient, err := llm.NewClient(ctx, cfg.GeminiAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("failed to create LLM client: %v", err)
	}

	events := store.New(cfg.DataFile, 200)

	dryRun := os.Getenv("DOCPULSE_DRY_RUN") == "true"
	handler := vcs.NewWebhookHandler(llmClient, rules, dryRun, events)

	hook, err := ghWebhooks.New(ghWebhooks.Options.Secret(cfg.GitHubWebhookSecret))
	if err != nil {
		log.Fatalf("failed to create webhook handler: %v", err)
	}

	r := gin.Default()

	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(dashboardHTML))
	})

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "docpulse"})
	})

	r.GET("/api/events", func(c *gin.Context) {
		c.JSON(http.StatusOK, events.List())
	})

	var demoMu sync.Mutex
	var lastDemo time.Time

	r.POST("/api/demo-sync", func(c *gin.Context) {
		demoMu.Lock()
		if wait := demoCooldown - time.Since(lastDemo); wait > 0 {
			demoMu.Unlock()
			c.JSON(http.StatusTooManyRequests, gin.H{"error": fmt.Sprintf("try again in %.0fs", wait.Seconds())})
			return
		}
		lastDemo = time.Now()
		demoMu.Unlock()

		evt := runDemoSync(c.Request.Context(), llmClient)
		events.Add(evt)
		c.JSON(http.StatusOK, evt)
	})

	r.POST("/webhook/github", func(c *gin.Context) {
		payload, err := hook.Parse(c.Request, ghWebhooks.PullRequestEvent)
		if err != nil {
			if err == ghWebhooks.ErrEventNotFound {
				c.JSON(http.StatusOK, gin.H{"message": "event type not handled"})
				return
			}
			log.Printf("[docpulse] error parsing webhook: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook payload"})
			return
		}

		switch p := payload.(type) {
		case ghWebhooks.PullRequestPayload:
			handlePullRequestEvent(c, p, handler)
		default:
			c.JSON(http.StatusOK, gin.H{"message": "event type not handled"})
		}
	})

	addr := fmt.Sprintf(":%s", cfg.ServerPort)
	log.Printf("DocPulse server starting on %s (dry-run=%v)", addr, dryRun)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

// runDemoSync runs a real classify+generate call against a fixed sample
// code/doc pair, so the dashboard can demonstrate the pipeline live without
// a real GitHub webhook.
func runDemoSync(ctx context.Context, llmClient *llm.Client) store.Event {
	evt := store.Event{Source: "demo", DocPath: demo.DocPath}

	needsUpdate, reason, err := llmClient.ClassifyChange(ctx, demo.SampleCodeContext)
	if err != nil {
		evt.Status = "error"
		evt.Error = err.Error()
		return evt
	}
	evt.NeedsUpdate = needsUpdate
	evt.Reason = reason

	if !needsUpdate {
		evt.Status = "skipped"
		return evt
	}

	updatedDoc, err := llmClient.UpdateDocumentation(ctx, demo.SampleCodeContext, demo.SampleDoc)
	if err != nil {
		evt.Status = "error"
		evt.Error = err.Error()
		return evt
	}

	evt.Status = "dry_run"
	evt.DryRun = true
	evt.UpdatedDoc = updatedDoc
	return evt
}

func handlePullRequestEvent(c *gin.Context, payload ghWebhooks.PullRequestPayload, handler *vcs.WebhookHandler) {
	action := payload.Action
	merged := payload.PullRequest.Merged

	log.Printf("[docpulse] received PR event: action=%s, merged=%v, repo=%s, pr=#%d",
		action, merged, payload.Repository.FullName, int(payload.PullRequest.Number))

	if action != "closed" || !merged {
		c.JSON(http.StatusOK, gin.H{"message": "PR not merged, skipping"})
		return
	}

	repoFullName := payload.Repository.FullName
	prNumber := int(payload.PullRequest.Number)
	baseBranch := payload.PullRequest.Base.Ref

	c.JSON(http.StatusAccepted, gin.H{"message": "processing merged PR", "pr": prNumber})

	go func() {
		ctx := context.Background()

		githubToken := os.Getenv("GITHUB_TOKEN")
		if githubToken == "" {
			log.Printf("[docpulse] GITHUB_TOKEN not set, cannot process PR #%d", prNumber)
			return
		}

		owner, repo := parseRepoFullName(repoFullName)
		client := vcs.NewClient(githubToken, owner, repo)

		if err := handler.HandleMergedPR(ctx, client, repoFullName, prNumber, baseBranch); err != nil {
			log.Printf("[docpulse] error processing PR #%d: %v", prNumber, err)
		} else {
			log.Printf("[docpulse] finished processing PR #%d", prNumber)
		}
	}()
}

func parseRepoFullName(fullName string) (owner, repo string) {
	for i, c := range fullName {
		if c == '/' {
			return fullName[:i], fullName[i+1:]
		}
	}
	return fullName, ""
}
