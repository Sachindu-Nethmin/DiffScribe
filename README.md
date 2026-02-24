# DiffScribe

> A GitHub Action that automatically fills PR description templates using AI — powered by GitHub Models and written in Go.

## How It Works

1. A contributor opens a Pull Request with an empty or unfilled description.
2. DiffScribe detects that the description still contains template placeholders.
3. It fetches the PR diff from the GitHub API.
4. It sends the diff + template to **GitHub Models** (`gpt-4o-mini`) to generate a filled description.
5. It updates the PR body in-place and posts a comment reminding the author to review the auto-filled content.

```
PR opened with empty description
        │
        ▼
DiffScribe checks for unfilled placeholders
        │
   unfilled? ──No──► Skip (description already filled)
        │
       Yes
        │
        ▼
Fetch PR diff via GitHub API
        │
        ▼
Call GitHub Models gpt-4o-mini
        │
        ▼
Patch PR body + post review comment
```

## Setup

### 1. Copy the workflow file

Add `.github/workflows/diffscribe.yml` to your repository. It uses the built-in `GITHUB_TOKEN` — **no extra secrets needed**.

### 2. Add (or keep) the PR template

Place your PR template at `.github/pull_request_template.md`. Use HTML comment placeholders like `<!-- describe your changes -->` so DiffScribe can detect unfilled sections.

### 3. Enable workflow permissions

In your repository settings:
**Settings → Actions → General → Workflow permissions** → select **Read and write permissions**.

### 4. Done

Open a PR with an empty description and watch DiffScribe fill it automatically.

## Detection Logic

DiffScribe considers a PR description **unfilled** if any of these are true:

| Condition | Example |
|---|---|
| Body is empty | `""` |
| Body equals the raw template exactly | contributor didn't change anything |
| Body still has more than 3 `<!--` comment placeholders | most sections untouched |

## Project Structure

```
DiffScribe/
├── .github/
│   ├── workflows/
│   │   └── diffscribe.yml          ← GitHub Action workflow
│   └── pull_request_template.md   ← Sample PR template
├── main.go                         ← Go core logic
├── go.mod                          ← Go module config
└── README.md
```

## Configuration

| Environment Variable | Source | Description |
|---|---|---|
| `GITHUB_TOKEN` | `secrets.GITHUB_TOKEN` (auto) | GitHub API auth + GitHub Models auth |
| `GITHUB_REPOSITORY` | `github.repository` (auto) | `owner/repo` |
| `PR_NUMBER` | `github.event.pull_request.number` (auto) | PR number |
| `PR_BODY` | `github.event.pull_request.body` (auto) | Current PR description |

## Limitations

- The PR diff is truncated to **8000 characters** to stay within model context limits. Large PRs may have some sections left unfilled.
- DiffScribe only runs on `opened` and `reopened` events, not on subsequent pushes.
- Sections that cannot be inferred from the diff (e.g., manual testing steps, screenshots) are left as-is with their placeholder comments.

## Tech Stack

- **Go** — core script language
- **GitHub Models** (`gpt-4o-mini`) — AI inference (free with GitHub account)
- **GitHub Actions** — CI/CD runner
- **GitHub REST API** — fetch diff, update PR body, post comments
