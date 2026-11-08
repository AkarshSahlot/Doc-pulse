package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/AkarshSahlot/docpulse/internal/config"
	"github.com/AkarshSahlot/docpulse/internal/llm"
	"github.com/AkarshSahlot/docpulse/internal/mapping"
	"github.com/AkarshSahlot/docpulse/internal/vcs"
	"github.com/gin-gonic/gin"
	ghWebhooks "github.com/go-playground/webhooks/v6/github"
)

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

	llmClient, err := llm.NewClient(ctx, cfg.AnthropicAPIKey, cfg.LLMModel)
	if err != nil {
		log.Fatalf("failed to create LLM client: %v", err)
	}

	dryRun := os.Getenv("DOCPULSE_DRY_RUN") == "true"
	handler := vcs.NewWebhookHandler(llmClient, rules, dryRun)

	hook, err := ghWebhooks.New(ghWebhooks.Options.Secret(cfg.GitHubWebhookSecret))
	if err != nil {
		log.Fatalf("failed to create webhook handler: %v", err)
	}

	r := gin.Default()

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "docpulse"})
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

		if err := handler.HandleMergedPR(ctx, client, prNumber, baseBranch); err != nil {
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
