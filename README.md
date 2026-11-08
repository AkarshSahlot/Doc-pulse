# DocPulse

DocPulse is a doc-sync agent: it watches merged pull requests, figures out which
documentation files are affected by the code that changed, and uses Claude to
regenerate just the stale parts — then opens a PR with the update for human review.

The part that makes it DocPulse rather than a one-size-fits-all heuristic is
`docpulse.yaml` — a small mapping config that declares which code paths affect
which docs, instead of hardcoding "README.md near the changed file":

```yaml
mappings:
  - code: "internal/**/*.go"
    docs:
      - "README.md"
    reason: "internal package change"

  - code: "**/openapi.*"
    docs:
      - "docs/api.md"
    reason: "OpenAPI spec changed"
```

Patterns support `*` (matches within a path segment) and `**` (matches across
directories), so you can be as broad or as specific as your repo needs.

## Architecture Overview

```
┌─────────────────┐
│   GitHub Repo   │
│                 │
│  PR #42 Merged  │
└────────┬────────┘
         │
         │ Webhook Event (POST)
         │
         ▼
┌─────────────────────────────────┐
│  DocPulse Server                │
│  (Port 8080)                     │
│                                  │
│  POST /webhook/github            │
│  ├─ Validates webhook signature  │
│  ├─ Filters: action=closed       │
│  └─ Filters: merged=true         │
└────────┬─────────────────────────┘
         │
         │ Async Processing (goroutine)
         │
         ▼
┌─────────────────────────────────┐
│  WebhookHandler                 │
│                                  │
│  1. Fetch PR diff               │
│  2. Match changed paths against │
│     docpulse.yaml                │
│  3. For each matched doc:       │
│     ├─ Classify (LLM)           │
│     ├─ Generate update (LLM)    │
│     └─ Create PR                │
└────────┬─────────────────────────┘
         │
         ├─────────────────┬─────────────────┐
         ▼                 ▼                 ▼
    ┌─────────┐      ┌──────────┐      ┌──────────┐
    │ GitHub  │      │ Anthropic│      │ GitHub   │
    │   API   │      │   API    │      │   API    │
    │         │      │          │      │          │
    │ Get diff│      │ Classify │      │ Create   │
    │ Get file│      │ Generate │      │ Branch   │
    └─────────┘      └──────────┘      │ Create PR│
                                      └──────────┘
```

## End-to-End Flow

### Step 1: Setup & Configuration

1. **Set Environment Variables**
   ```bash
   cp .env.example .env
   # then fill in:
   export ANTHROPIC_API_KEY="sk-ant-..."
   export GITHUB_WEBHOOK_SECRET="your-webhook-secret"
   export GITHUB_TOKEN="ghp_..."
   export SERVER_PORT="8080"
   ```

2. **Review `docpulse.yaml`**

   Declare which code paths map to which docs — this is what the webhook
   handler uses instead of a fixed heuristic.

3. **Start the Server**
   ```bash
   go run ./cmd/server
   # Or build and run:
   go build -o docpulse-server ./cmd/server
   ./docpulse-server
   ```

4. **Verify Server is Running**
   ```bash
   curl http://localhost:8080/health
   # Response: {"status":"ok","service":"docpulse"}
   ```

### Step 2: Configure GitHub Webhook

1. **Go to your GitHub repository**
   - Settings → Webhooks → Add webhook

2. **Configure Webhook**
   - **Payload URL**: `https://your-server.com/webhook/github`
   - **Content type**: `application/json`
   - **Secret**: Same as `GITHUB_WEBHOOK_SECRET` env var
   - **Events**: Select "Pull requests" only
   - **Active**: ✓

3. **Test the Webhook**
   - GitHub will send a test ping
   - Check server logs to verify receipt

### Step 3: Trigger Event (Developer Workflow)

1. **Developer makes code changes**
   ```bash
   git checkout -b feature/add-limit-param
   # Edit internal/vcs/client.go: add a limit parameter to GetPRDiff
   git commit -m "Add limit parameter to GetPRDiff"
   git push origin feature/add-limit-param
   ```

2. **Developer opens Pull Request**
   - PR #42: "Add limit parameter to GetPRDiff"
   - Code changes: `internal/vcs/client.go`

3. **PR gets merged**
   - Maintainer reviews and merges PR #42
   - GitHub sends webhook event to your server

### Step 4: Server Processing (Automatic)

#### 4.1 Webhook Reception
```
POST /webhook/github
Headers:
  X-GitHub-Event: pull_request
  X-Hub-Signature-256: sha256=...
Body: {
  "action": "closed",
  "pull_request": {
    "number": 42,
    "merged": true,
    "base": { "ref": "main" }
  },
  "repository": {
    "full_name": "owner/repo"
  }
}
```

**Server Actions:**
- ✅ Validates webhook signature via `go-playground/webhooks`
- ✅ Parses payload as a `pull_request` event
- ✅ Checks: `action == "closed"` AND `merged == true`
- ✅ Returns `202 Accepted` immediately (async processing)

#### 4.2 Background Processing (Goroutine)

**Step 1: Fetch PR Diff**
```go
diff := client.GetPRDiff(ctx, 42)
// Returns:
// {
//   Files: [
//     {
//       Filename: "internal/vcs/client.go",
//       Patch: "@@ -43,6 +43,6 @@\n func (c *Client) GetPRDiff(ctx context.Context, prNumber int) {\n+func (c *Client) GetPRDiff(ctx context.Context, prNumber, limit int) {\n",
//       Status: "modified"
//     }
//   ]
// }
```

**Step 2: Match Changed Paths Against `docpulse.yaml`**
```go
matches := rules.DocsForAll(changedPaths)
// "internal/vcs/client.go" matches rule `internal/**/*.go` → ["README.md"]
```

**Step 3: Build Code Context**
```go
codeContext := "--- File: internal/vcs/client.go ---\n@@ -43,6 +43,6 @@\n func (c *Client) GetPRDiff(ctx context.Context, prNumber int) {\n+func (c *Client) GetPRDiff(ctx context.Context, prNumber, limit int) {\n\n\n"
```

**Step 4: Process Each Matched Doc**

For each matched doc (e.g., `README.md`):

**4a. Fetch Current Doc Content**
```go
docContent := client.GetFileContent(ctx, "README.md", "main")
```

**4b. Classify (LLM Call #1)**
```go
needsUpdate, reason := llmClient.ClassifyChange(ctx, codeContext)
// LLM Response: "YES - GetPRDiff signature changed, added limit parameter"
// needsUpdate = true
```

**4c. Generate Updated Documentation (LLM Call #2)**
```go
updatedDoc := llmClient.UpdateDocumentation(ctx, codeContext, docContent)
// LLM Response:
// <updated_doc>
// ...
// GetPRDiff(ctx, prNumber, limit) now accepts a limit parameter
// ...
// </updated_doc>
```

If `DOCPULSE_DRY_RUN=true`, processing stops here — the classification and the
would-be diff are logged, but nothing is written and no PR is opened.

**4d. Create Pull Request**
```go
// 1. Create branch: docpulse/update-readme-md-pr-42
// 2. Update README.md with new content
// 3. Commit: "docs: update README.md (triggered by PR #42)"
// 4. Open PR: "docs: update README.md (triggered by PR #42)"
```

### Step 5: Result

**New Pull Request Created:**
- **Title**: `docs: update README.md (triggered by PR #42)`
- **Branch**: `docpulse/update-readme-md-pr-42`
- **Description**:
  ```
  This PR was automatically generated by DocPulse.

  It updates `README.md` to reflect changes made in PR #42.

  Please review the changes carefully before merging.
  ```
- **Changes**: Updated `README.md` with the new function signature

**Developer/Maintainer:**
- Reviews the doc update PR
- Merges if changes look correct
- Documentation stays in sync! ✅

## Code Flow Details

### Entry Point: `cmd/server/main.go`

```go
main()
├─ config.MustLoadForServer() → load env + .env
├─ mapping.Load(docpulse.yaml) → compile code→doc rules
├─ llm.NewClient() → Anthropic client
├─ vcs.NewWebhookHandler() → wire mapping + LLM client + dry-run flag
├─ Setup Gin router
│  ├─ GET /health
│  └─ POST /webhook/github (validated via go-playground/webhooks)
└─ Start server on :SERVER_PORT
```

### Webhook Handler: `internal/vcs/webhook.go`

```go
HandleMergedPR()
├─ GetPRDiff() → fetch changed files for the merged PR
├─ rules.DocsForAll(changedPaths) → resolve matching docs from docpulse.yaml
└─ For each matched doc:
   └─ syncDoc()
      ├─ GetFileContent() → current doc content
      ├─ ClassifyChange() → LLM: "Does this need update?"
      ├─ UpdateDocumentation() → LLM: "Generate updated doc"
      ├─ (dry-run: log and stop here)
      └─ CreateDocUpdatePR() → create branch + commit + PR
```

### GitHub Client: `internal/vcs/client.go`

```go
GetPRDiff()
└─ GitHub API: GET /repos/{owner}/{repo}/pulls/{pr}/files

GetFileContent()
└─ GitHub API: GET /repos/{owner}/{repo}/contents/{path}

CreateDocUpdatePR()
├─ Git API: POST /repos/{owner}/{repo}/git/refs (create branch)
├─ Contents API: PUT /repos/{owner}/{repo}/contents/{path} (update file)
└─ PR API: POST /repos/{owner}/{repo}/pulls (create PR)
```

### Mapping Config: `internal/mapping/mapping.go`

```go
Load(docpulse.yaml)
└─ Compile each rule's `code` glob ("*", "**") into a regexp

DocsFor(path) / DocsForAll(paths)
└─ Return deduplicated {DocPath, Reason} matches for one or more changed paths
```

### LLM Client: `internal/llm/client.go` + `prompts.go`

```go
ClassifyChange(codeContext)
└─ Anthropic Messages API → "YES"/"NO" + one-line reason

UpdateDocumentation(codeContext, docContent)
└─ Anthropic Messages API → full regenerated doc, extracted from <updated_doc> tags
```

## CLI

The CLI runs the same classify/update pipeline locally, without a webhook or a
GitHub token:

```bash
# Check which docs a given code path maps to
go run ./cmd/cli map --code=internal/vcs/webhook.go

# Regenerate a single doc from a single code file
go run ./cmd/cli sync --code=internal/vcs/webhook.go --doc=README.md --write

# See what sync would do without writing anything
go run ./cmd/cli sync --code=internal/vcs/webhook.go --doc=README.md --dry-run
```

## Testing Locally

### 1. Use ngrok to expose local server

```bash
# Terminal 1: Start server
go run ./cmd/server

# Terminal 2: Expose to internet
ngrok http 8080
# Copy the HTTPS URL: https://abc123.ngrok.io
```

### 2. Configure GitHub webhook with ngrok URL

- Payload URL: `https://abc123.ngrok.io/webhook/github`
- Secret: Your `GITHUB_WEBHOOK_SECRET`

### 3. Test with a real PR

- Create a test PR in your repo
- Merge it
- Watch server logs for processing
- Check for new PR opened by DocPulse

## Project Layout

```
cmd/
  server/   webhook server entrypoint
  cli/      local sync/map commands
internal/
  config/   environment + .env loading
  mapping/  docpulse.yaml parsing and glob matching
  analyzer/ changed-file classification (code vs. non-code)
  vcs/      GitHub API client + webhook handler
  llm/      Claude client and prompts
```

## Environment Variables Reference

| Variable | Required | Description |
|----------|----------|--------------|
| `ANTHROPIC_API_KEY` | Yes | Anthropic Claude API key |
| `GITHUB_WEBHOOK_SECRET` | Yes (server) | Secret for webhook signature validation |
| `GITHUB_TOKEN` | Yes (server) | GitHub personal access token (for API calls) |
| `LLM_MODEL` | No | Model to use (default: `claude-sonnet-4-5`) |
| `SERVER_PORT` | No | Port to listen on (default: `8080`) |
| `DOCPULSE_MAPPING_FILE` | No | Path to the mapping config (default: `docpulse.yaml`) |
| `DOCPULSE_DRY_RUN` | No | When `true`, classify and log without writing or opening PRs |

## Troubleshooting

### Webhook not received
- ✅ Check server is running and accessible
- ✅ Verify webhook URL is correct
- ✅ Check GitHub webhook delivery logs (Settings → Webhooks → Recent Deliveries)
- ✅ Verify webhook secret matches

### No doc PR created
- ✅ Check `GITHUB_TOKEN` is set and has correct permissions
- ✅ Verify token has `repo` scope
- ✅ Confirm the changed paths actually match a rule in `docpulse.yaml`
- ✅ Check server logs for errors
- ✅ Ensure PR was actually merged (not just closed)
- ✅ Check `DOCPULSE_DRY_RUN` isn't accidentally set to `true`

### LLM errors
- ✅ Verify `ANTHROPIC_API_KEY` is valid
- ✅ Check API quota/rate limits
- ✅ Verify `LLM_MODEL` name is correct

## Security Considerations

1. **Webhook Secret**: Always use a strong secret and validate signatures
2. **GitHub Token**: Use fine-grained tokens with minimal permissions
3. **Rate Limiting**: Consider adding rate limiting to prevent abuse
4. **Error Handling**: Don't expose sensitive info in error messages
5. **Production**: Use GitHub App installation tokens instead of personal tokens

## Future Enhancements

- [ ] Use GitHub App instead of personal tokens
- [ ] Support multiple documentation formats (OpenAPI, etc.)
- [ ] Add retry logic for failed API calls
- [ ] Add metrics/monitoring
- [ ] Support for private repositories
