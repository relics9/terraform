# anthropic-agent

A Cloud Run service that detects GCP error logs, analyzes them with Claude, and sends Slack notifications.
Responds to Slack @mentions to automatically create GitHub PRs and Issues.

## Models

| Use case | Model |
|----------|-------|
| `@mention fix` / `@mention issue` commands | configurable via `ANTHROPIC_CLAUDE_MODEL` (default: `claude-opus-4-6`) |

## Endpoints

| Path | Purpose |
|------|---------|
| `POST /notify` | Receives Pub/Sub Push subscription messages |
| `POST /` | Receives Slack Events API events |

## Architecture

```mermaid
flowchart TD
    GCPLog["☁️ GCP Cloud Logging"]
    PubSubTopic["📨 Pub/Sub Topic<br/>error-logs"]
    PubSubSub["📬 Pub/Sub Push Subscription"]
    CloudRun["🤖 Cloud Run<br/>anthropic-agent"]
    Claude["🧠 Claude API<br/>claude-opus-4-6"]
    SlackWebhook["💬 Slack Incoming Webhook<br/>notification channel"]
    SlackEvents["💬 Slack Events API<br/>@mention"]
    SlackBot["💬 Slack Bot API<br/>chat.postMessage"]
    GitHub["🐙 GitHub API<br/>PR / Issue creation"]

    GCPLog -->|"forward severity>=ERROR logs<br/>(excluding HTTP access logs)"| PubSubTopic
    PubSubTopic --> PubSubSub
    PubSubSub -->|"POST /notify"| CloudRun

    CloudRun -->|"Block Kit notification"| SlackWebhook

    SlackEvents -->|"POST /<br/>@mention fix/issue"| CloudRun
    CloudRun -->|"read thread<br/>conversations.replies"| SlackEvents
    CloudRun -->|"error analysis request<br/>(claude-opus-4-6)"| Claude
    CloudRun -->|"create PR / Issue"| GitHub
    GitHub -->|"URL"| CloudRun
    CloudRun -->|"post result"| SlackBot
```

## Flow

### 1. Error Notification (Pub/Sub → Slack)

```
GCP Cloud Logging
  └─ detects severity>=ERROR log
      ※ HTTP access logs (run.googleapis.com/requests) are excluded
         to prevent Slackbot-LinkExpanding loop (see Loop Prevention below)
  └─ forwards to Pub/Sub topic: error-logs
     └─ Push subscription calls POST /notify
        └─ resolves repository from REPO_MAP by service name
           └─ unregistered services are skipped (logged only)
        └─ sends Block Kit notification via Slack Webhook
           ├─ Project ID / GitHub repository link
           ├─ Service name
           ├─ Error message (with method/path/query context)
           └─ View in Cloud Logging button
```

### 2. Auto-create GitHub PR (`@mention fix`)

```
Slack: @mention fix
  └─ Slack Events API → POST /
     └─ fetch thread messages
        └─ analyze error with Claude (claude-opus-4-6, evaluate should_create_pr)
           └─ if should_create_pr=true
              └─ GitHub API: create branch → update file → create PR
                 └─ post link to Slack thread
```

### 3. Create GitHub Issue (`@mention issue`)

```
Slack: @mention issue
  └─ Slack Events API → POST /
     └─ fetch thread messages
        └─ analyze error with Claude (claude-opus-4-6)
           └─ GitHub API: create Issue
              └─ post link to Slack thread
```

## Loop Prevention

Slack's internal security bot (`Slackbot-LinkExpanding`) automatically fetches URLs found in messages.
If the error message contains a Cloud Run service URL, this fetch causes an HTTP 500 → Cloud Run logs it → triggers another notification → infinite loop.

**Solution**: The Cloud Logging sink filter excludes `run.googleapis.com/requests` (HTTP access logs).
Only application-level logs (stdout/stderr from the app itself) are forwarded to Pub/Sub.

```
log_filter = "severity>=ERROR
  AND logName!=\"projects/.../logs/run.googleapis.com%2Frequests\"
  AND resource.labels.service_name!=\"anthropic-agent\"
  ..."
```

## Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `ANTHROPIC_API_KEY` | ✅ | Claude API key |
| `ANTHROPIC_CLAUDE_MODEL` | optional | Claude model ID (default: `claude-opus-4-6`) |
| `GITHUB_TOKEN` | ✅ | GitHub Personal Access Token |
| `GITHUB_USER` | ✅ | GitHub owner name (e.g. `your-github-username`) |
| `PROJECT_ID` | ✅ | GCP project ID |
| `REPO_MAP` | ✅ | Service name to repository mapping (e.g. `example-api=example,foo-svc=foo`) |
| `SLACK_BOT_NAME` | ✅ | Slack bot mention name (e.g. `@Claude AI`) |
| `SLACK_BOT_TOKEN` | ✅ | Slack Bot Token (`xoxb-...`) |
| `SLACK_SIGNING_SECRET` | ✅ | Slack Signing Secret for verifying request signatures |
| `SLACK_WEBHOOK_URL` | ✅ | Incoming Webhook URL for error notifications |

## File Structure

```
services/anthropic_agent/
├── main.go      # Entry point and HTTP routing
├── pubsub.go    # Pub/Sub handler, error analysis, Slack notification
├── slack.go     # Slack Events handler, mention processing
├── github.go    # GitHub API (PR/Issue creation, repo resolution)
├── claude.go    # Claude API calls
├── util.go      # Shared utilities
├── Dockerfile
├── go.mod
└── go.sum
```
