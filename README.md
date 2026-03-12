# GCP エラー通知 & AI 自動修正サンプル

GCPのエラーログをSlackに通知し、ClaudeによるAI分析とGitHub PR自動作成を行うサンプルです。

## アーキテクチャ

```
GCPエラーログ
    → Cloud Logging Sink
    → Pub/Sub (error-logs)
    → Cloud Function: slack-notifier
    → Slack #gcp-error-alerts

Slack @relics9-bot analyze メンション
    → Cloud Function: ai-agent
    → Claude API (エラー分析)
    → GitHub API (PR作成)
    → Slack スレッドに結果投稿
```

## 前提条件

- GCP プロジェクト (`relics9`)
- Slack App (`relics9-bot`)
- GitHub リポジトリ (`relics9/terraform`)
- Anthropic API キー (有料クレジット必要)

## セットアップ

### 1. 各種トークンの準備

以下のファイルを作成してトークンを記載する:

| ファイル | 内容 |
|---|---|
| `claude-token.json` | Anthropic API キー (`sk-ant-api03-...`) |
| `slack-token.json` | Slack Bot Token (`xoxb-...`) |
| `github-token.json` | GitHub Personal Access Token (`ghp_...`) |
| `relics9-1dc2283b85f8.json` | GCP サービスアカウントキー |

### 2. terraform.tfvars の設定

```bash
cp terraform.tfvars.example terraform.tfvars
# terraform.tfvars を編集して各値を設定
```

### 3. デプロイ

```bash
GOOGLE_APPLICATION_CREDENTIALS=relics9-1dc2283b85f8.json terraform init
GOOGLE_APPLICATION_CREDENTIALS=relics9-1dc2283b85f8.json terraform apply
```

### 4. Slack App の設定

1. [Slack API](https://api.slack.com/apps) でアプリを開く
2. **Event Subscriptions** → Request URL に `ai-agent` の URL を設定
   - URL: `https://ai-agent-6xdubcz5hq-an.a.run.app`
3. **Subscribe to bot events** に `app_mention` を追加
4. **Save Changes** → **Reinstall App**

## 動作確認

### エラー通知のテスト

```bash
CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=relics9-1dc2283b85f8.json \
gcloud pubsub topics publish error-logs \
  --project=relics9 \
  --message='{
    "severity": "ERROR",
    "resource": {
      "type": "cloud_function",
      "labels": {"function_name": "test-function", "project_id": "relics9"}
    },
    "textPayload": "テスト: NullPointerException at line 42",
    "timestamp": "2026-03-12T05:00:00Z",
    "logName": "projects/relics9/logs/cloudfunctions.googleapis.com%2Fcloud-functions"
  }'
```

→ Slack の `#gcp-error-alerts` にエラー通知が届く

### AI分析 & PR作成のテスト

1. `#gcp-error-alerts` に届いたエラー通知メッセージのスレッドを開く
2. スレッド内で `@relics9-bot analyze` とメンション
3. Claude がエラーを分析してスレッドに結果を投稿
4. 修正可能な場合は GitHub PR が自動作成される

## ログ確認

```bash
# ai-agent のログ
CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=relics9-1dc2283b85f8.json \
gcloud logging read \
  'resource.type="cloud_run_revision" resource.labels.service_name="ai-agent"' \
  --project=relics9 --limit=20

# slack-notifier のログ
CLOUDSDK_AUTH_CREDENTIAL_FILE_OVERRIDE=relics9-1dc2283b85f8.json \
gcloud logging read \
  'resource.type="cloud_run_revision" resource.labels.service_name="slack-notifier"' \
  --project=relics9 --limit=20
```

## リンク

- Slack: https://app.slack.com/client/T0AKQLV62J1
- GitHub: https://github.com/relics9
- GCP Console: https://console.cloud.google.com/home/dashboard?project=relics9
- Anthropic Console: https://console.anthropic.com
