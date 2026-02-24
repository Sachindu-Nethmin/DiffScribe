import ballerina/http;
import ballerina/io;
import ballerina/log;
import ballerina/os;

const string GITHUB_API_BASE = "https://api.github.com";
const string GITHUB_MODELS_BASE = "https://models.inference.ai.azure.com";
const int MAX_DIFF_SIZE = 8000;
const int UNFILLED_COMMENT_THRESHOLD = 3;

public function main() returns error? {
    // Read required environment variables
    string token = os:getEnv("GITHUB_TOKEN");
    string repository = os:getEnv("GITHUB_REPOSITORY");
    string prNumber = os:getEnv("PR_NUMBER");
    string prBody = os:getEnv("PR_BODY");

    if token == "" || repository == "" || prNumber == "" {
        log:printError("Required environment variables (GITHUB_TOKEN, GITHUB_REPOSITORY, PR_NUMBER) are not set.");
        return error("Missing required environment variables");
    }

    // Initialize HTTP clients
    http:Client githubApiClient = check new (GITHUB_API_BASE, {
        httpVersion: http:HTTP_1_1
    });
    http:Client modelsClient = check new (GITHUB_MODELS_BASE, {
        httpVersion: http:HTTP_1_1
    });

    // Read the PR template
    string template = check io:fileReadString(".github/pull_request_template.md");

    // Check if the PR description needs filling
    if !isTemplateUnfilled(prBody, template) {
        log:printInfo("PR description appears to be already filled. Skipping DiffScribe.");
        return;
    }

    log:printInfo("PR description is unfilled. Fetching diff...");

    // Fetch the PR diff
    string diff = check fetchPrDiff(githubApiClient, repository, prNumber, token);
    if diff.length() > MAX_DIFF_SIZE {
        diff = diff.substring(0, MAX_DIFF_SIZE) + "\n\n... (diff truncated to fit context window)";
    }

    // Generate the filled description using GitHub Models AI
    log:printInfo("Calling GitHub Models API (gpt-4o-mini) to fill PR description...");
    string filledDescription = check generateDescription(modelsClient, template, prBody, diff, token);

    // Update the PR body in-place
    check updatePrBody(githubApiClient, repository, prNumber, filledDescription, token);
    log:printInfo("PR description updated successfully.");

    // Post a comment to notify the author
    check postComment(githubApiClient, repository, prNumber, token);
    log:printInfo("Comment posted on PR. DiffScribe completed successfully.");
}

// Returns true if the PR body is considered unfilled (empty, matches template, or has many placeholders).
function isTemplateUnfilled(string body, string template) returns boolean {
    string trimmedBody = body.trim();

    if trimmedBody == "" {
        return true;
    }

    if trimmedBody == template.trim() {
        return true;
    }

    // Count remaining HTML comment placeholders
    int commentCount = 0;
    string remaining = body;
    while remaining.includes("<!--") {
        int? idx = remaining.indexOf("<!--");
        if idx is () {
            break;
        }
        commentCount += 1;
        remaining = remaining.substring(idx + 4);
    }

    return commentCount > UNFILLED_COMMENT_THRESHOLD;
}

// Fetches the raw unified diff for a PR from the GitHub API.
function fetchPrDiff(http:Client client, string repo, string prNum, string token) returns string|error {
    map<string|string[]> headers = {
        "Authorization": "Bearer " + token,
        "Accept": "application/vnd.github.v3.diff",
        "X-GitHub-Api-Version": "2022-11-28"
    };

    http:Response response = check client->get("/repos/" + repo + "/pulls/" + prNum, headers);
    int statusCode = response.statusCode;
    if statusCode != 200 {
        return error(string `GitHub API returned status ${statusCode} when fetching diff`);
    }
    return check response.getTextPayload();
}

// Calls the GitHub Models API to produce a filled PR description from the diff.
function generateDescription(http:Client client, string template, string currentBody, string diff, string token) returns string|error {
    string prompt = string `You are helping fill out a Pull Request description template based on the code diff provided.

## PR Template
${template}

## Current PR Description (may be empty or still showing template placeholders)
${currentBody}

## Code Diff
${diff}

## Instructions
1. Fill in ONLY the sections that can be reasonably inferred from the diff above.
2. For any section you cannot determine from the diff, preserve the original placeholder comment (e.g., <!-- describe your changes here -->).
3. Return ONLY the filled template content. Do not add any extra commentary outside the template.
4. Preserve the template's exact markdown structure, headings, and checklist format.`;

    json requestBody = {
        "model": "gpt-4o-mini",
        "messages": [
            {
                "role": "system",
                "content": "You are an expert software engineer who writes clear, concise, and helpful Pull Request descriptions."
            },
            {
                "role": "user",
                "content": prompt
            }
        ],
        "max_tokens": 2000,
        "temperature": 0.3
    };

    map<string|string[]> headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json"
    };

    http:Response response = check client->post("/chat/completions", requestBody, headers);
    int statusCode = response.statusCode;
    if statusCode != 200 {
        string errBody = check response.getTextPayload();
        return error(string `GitHub Models API returned status ${statusCode}: ${errBody}`);
    }

    json responseJson = check response.getJsonPayload();
    return (check responseJson.choices[0].message.content).toString();
}

// Patches the PR body via the GitHub REST API.
function updatePrBody(http:Client client, string repo, string prNum, string body, string token) returns error? {
    json requestBody = {"body": body};
    map<string|string[]> headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
        "X-GitHub-Api-Version": "2022-11-28"
    };

    http:Response response = check client->patch("/repos/" + repo + "/pulls/" + prNum, requestBody, headers);
    int statusCode = response.statusCode;
    if statusCode != 200 {
        string errBody = check response.getTextPayload();
        return error(string `Failed to update PR body. Status ${statusCode}: ${errBody}`);
    }
}

// Posts a comment on the PR informing the author that DiffScribe filled the description.
function postComment(http:Client client, string repo, string prNum, string token) returns error? {
    string commentBody = string `### 🤖 DiffScribe Auto-filled PR Description

This PR description was automatically filled by **DiffScribe** based on the code diff.

Please review each section and:
- Correct anything that was inferred incorrectly
- Fill in sections that could not be determined from the diff (marked with placeholder comments)
- Add any additional context that would help reviewers

---
*Powered by [DiffScribe](https://github.com/DiffScribe) using GitHub Models (gpt-4o-mini)*`;

    json requestBody = {"body": commentBody};
    map<string|string[]> headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
        "X-GitHub-Api-Version": "2022-11-28"
    };

    http:Response response = check client->post("/repos/" + repo + "/issues/" + prNum + "/comments", requestBody, headers);
    int statusCode = response.statusCode;
    if statusCode != 201 {
        string errBody = check response.getTextPayload();
        return error(string `Failed to post comment. Status ${statusCode}: ${errBody}`);
    }
}
