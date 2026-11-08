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

const landingPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>DocPulse</title>
<style>
  :root { color-scheme: light dark; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    max-width: 640px;
    margin: 4rem auto;
    padding: 0 1.5rem;
    line-height: 1.6;
  }
  h1 { font-size: 1.75rem; margin-bottom: 0.25rem; }
  .tag { color: #888; margin-top: 0; }
  code {
    background: rgba(127,127,127,0.15);
    padding: 0.15em 0.4em;
    border-radius: 4px;
    font-size: 0.9em;
  }
  ul { padding-left: 1.2rem; }
  a { color: #3b82f6; }
  footer { margin-top: 3rem; color: #888; font-size: 0.85rem; }
</style>
</head>
<body>
  <h1>DocPulse</h1>
  <p class="tag">A doc-sync agent: watches merged pull requests, figures out which docs are
  affected by the code that changed, and uses an LLM to regenerate just the stale parts —
  then opens a PR with the update for human review.</p>

  <p>Routes on this server:</p>
  <ul>
    <li><code>GET /health</code> &mdash; service status</li>
    <li><code>POST /webhook/github</code> &mdash; GitHub pull_request webhook endpoint</li>
  </ul>

  <p><a href="https://github.com/AkarshSahlot/Doc-pulse">Source on GitHub</a></p>

  <footer>Running on Fly.io</footer>
</body>
</html>`

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

	dryRun := os.Getenv("DOCPULSE_DRY_RUN") == "true"
	handler := vcs.NewWebhookHandler(llmClient, rules, dryRun)

	hook, err := ghWebhooks.New(ghWebhooks.Options.Secret(cfg.GitHubWebhookSecret))
	if err != nil {
		log.Fatalf("failed to create webhook handler: %v", err)
	}

	r := gin.Default()

	r.GET("/", func(c *gin.Context) {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(landingPageHTML))
	})

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
