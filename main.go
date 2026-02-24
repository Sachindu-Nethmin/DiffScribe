package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const (
	githubAPIBase            = "https://api.github.com"
	githubModelsBase         = "https://models.inference.ai.azure.com"
	maxDiffSize              = 8000
	unfilledCommentThreshold = 3
)

func main() {
	token := os.Getenv("GITHUB_TOKEN")
	repository := os.Getenv("GITHUB_REPOSITORY")
	prNumber := os.Getenv("PR_NUMBER")
	prBody := os.Getenv("PR_BODY")

	if token == "" || repository == "" || prNumber == "" {
		log.Fatal("Required environment variables (GITHUB_TOKEN, GITHUB_REPOSITORY, PR_NUMBER) are not set.")
	}

	templateBytes, err := os.ReadFile(".github/pull_request_template.md")
	if err != nil {
		log.Fatalf("Failed to read PR template: %v", err)
	}
	template := string(templateBytes)

	if !isTemplateUnfilled(prBody, template) {
		log.Println("PR description appears to be already filled. Skipping DiffScribe.")
		return
	}

	log.Println("PR description is unfilled. Posting notice comment...")
	if err := postUnfilledNotice(repository, prNumber, token); err != nil {
		log.Printf("Warning: failed to post unfilled notice: %v", err)
	}

	log.Println("Fetching diff...")

	diff, err := fetchPrDiff(repository, prNumber, token)
	if err != nil {
		log.Fatalf("Failed to fetch PR diff: %v", err)
	}
	if len(diff) > maxDiffSize {
		diff = diff[:maxDiffSize] + "\n\n... (diff truncated to fit context window)"
	}

	log.Println("Calling GitHub Models API (gpt-4o-mini) to fill PR description...")
	filledDescription, err := generateDescription(template, prBody, diff, token)
	if err != nil {
		log.Fatalf("Failed to generate description: %v", err)
	}

	if err := updatePrBody(repository, prNumber, filledDescription, token); err != nil {
		log.Fatalf("Failed to update PR body: %v", err)
	}
	log.Println("PR description updated successfully.")

	if err := postComment(repository, prNumber, token); err != nil {
		log.Fatalf("Failed to post comment: %v", err)
	}
	log.Println("Comment posted on PR. DiffScribe completed successfully.")
}

// isTemplateUnfilled returns true if the PR body is considered unfilled
// (empty, matches template exactly, or still has many placeholder comments).
func isTemplateUnfilled(body, template string) bool {
	trimmed := strings.TrimSpace(body)

	if trimmed == "" {
		return true
	}

	if trimmed == strings.TrimSpace(template) {
		return true
	}

	return strings.Count(body, "<!--") > unfilledCommentThreshold
}

// fetchPrDiff fetches the raw unified diff for a PR from the GitHub API.
func fetchPrDiff(repo, prNum, token string) (string, error) {
	url := fmt.Sprintf("%s/repos/%s/pulls/%s", githubAPIBase, repo, prNum)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github.v3.diff")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d when fetching diff", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// generateDescription calls the GitHub Models API to produce a filled PR description.
func generateDescription(template, currentBody, diff, token string) (string, error) {
	prompt := fmt.Sprintf(`You are helping fill out a Pull Request description template based on the code diff provided.

## PR Template
%s

## Current PR Description (may be empty or still showing template placeholders)
%s

## Code Diff
%s

## Instructions
1. Fill in ONLY the sections that can be reasonably inferred from the diff above.
2. For any section you cannot determine from the diff, preserve the original placeholder comment (e.g., <!-- describe your changes here -->).
3. Return ONLY the filled template content. Do not add any extra commentary outside the template.
4. Preserve the template's exact markdown structure, headings, and checklist format.`, template, currentBody, diff)

	reqBody := map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "You are an expert software engineer who writes clear, concise, and helpful Pull Request descriptions.",
			},
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"max_tokens":  2000,
		"temperature": 0.3,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, githubModelsBase+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub Models API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices returned from GitHub Models API")
	}
	return result.Choices[0].Message.Content, nil
}

// updatePrBody patches the PR body via the GitHub REST API.
func updatePrBody(repo, prNum, body, token string) error {
	reqBody := map[string]string{"body": body}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/repos/%s/pulls/%s", githubAPIBase, repo, prNum)
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update PR body. Status %d: %s", resp.StatusCode, string(errBody))
	}
	return nil
}

// postUnfilledNotice posts a comment as soon as an unfilled template is detected,
// informing the author that DiffScribe will fill the description automatically.
func postUnfilledNotice(repo, prNum, token string) error {
	commentBody := `### ⚠️ PR Template Not Filled Out

This PR description template has **not been filled out**.

**DiffScribe** has detected that the description still contains unfilled placeholders. It will now automatically analyse the code diff and fill in the PR description.

> ⏳ Please wait — DiffScribe is processing the diff and will update the PR description shortly.

---
*Powered by [DiffScribe](https://github.com/DiffScribe) using GitHub Models (gpt-4o-mini)*`

	return postIssueComment(repo, prNum, token, commentBody)
}

// postComment posts a comment on the PR informing the author that DiffScribe filled the description.
func postComment(repo, prNum, token string) error {
	commentBody := `### ✅ DiffScribe — PR Description Auto-filled

**DiffScribe** has automatically filled the PR description based on the code diff.

Please review each section and:
- Correct anything that was inferred incorrectly
- Fill in sections that could not be determined from the diff (marked with placeholder comments)
- Add any additional context that would help reviewers

---
*Powered by [DiffScribe](https://github.com/DiffScribe) using GitHub Models (gpt-4o-mini)*`

	return postIssueComment(repo, prNum, token, commentBody)
}

// postIssueComment is the shared helper that POSTs a comment body to the GitHub issues comments API.
func postIssueComment(repo, prNum, token, body string) error {
	reqBody := map[string]string{"body": body}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/repos/%s/issues/%s/comments", githubAPIBase, repo, prNum)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to post comment. Status %d: %s", resp.StatusCode, string(errBody))
	}
	return nil
}
