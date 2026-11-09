// Package demo holds a fixed, realistic code-change/doc pair so the hosted
// dashboard can trigger a real classify+generate call against the live LLM
// without needing a real GitHub webhook to arrive.
package demo

// SampleCodeContext mirrors the GetPRDiff example from the project's own README:
// a PR that adds a `limit` parameter to an existing exported function.
const SampleCodeContext = `--- File: internal/vcs/client.go ---
@@ -43,7 +43,7 @@
-func (c *Client) GetPRDiff(ctx context.Context, prNumber int) (*PRDiff, error) {
-	files, _, err := c.api.PullRequests.ListFiles(ctx, c.owner, c.repo, prNumber, &gh.ListOptions{
-		PerPage: 100,
-	})
+func (c *Client) GetPRDiff(ctx context.Context, prNumber, limit int) (*PRDiff, error) {
+	files, _, err := c.api.PullRequests.ListFiles(ctx, c.owner, c.repo, prNumber, &gh.ListOptions{
+		PerPage: limit,
+	})
`

// SampleDoc is the (now stale) documentation for the function above.
const SampleDoc = `### GetPRDiff(ctx, prNumber)

Fetches the changed files for a merged pull request. Always returns up to
the first 100 changed files.
`

// DocPath is the label shown for this sample in the dashboard.
const DocPath = "README.md (sample excerpt)"
