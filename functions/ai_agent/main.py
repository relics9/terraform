"""
Cloud Function: AI Agent
SlackのBotメンションを受けてClaudeでエラーを分析し、GitHubにPRを作成する

Slack App設定:
  - Event Subscriptions: Request URL にこのFunctionのURLを設定
  - Subscribe to bot events: app_mention
  - OAuth Scopes: channels:history, chat:write, app_mentions:read
"""
import hashlib
import hmac
import json
import os
import time
import urllib.request
import urllib.error
from typing import Any

import functions_framework
import anthropic


# ==============================================================================
# エントリーポイント
# ==============================================================================
@functions_framework.http
def handle_slack_event(request):
    """Slack Events APIからのWebhookを処理する"""
    # Slackのリトライは無視 (3秒タイムアウトによる重複処理を防ぐ)
    if request.headers.get("X-Slack-Retry-Num"):
        return ("OK", 200)

    # Slackの署名検証
    if not _verify_slack_signature(request):
        return ("Unauthorized", 401)

    payload = request.get_json(silent=True) or {}

    # Slack URL Verification (初回設定時)
    if payload.get("type") == "url_verification":
        return (json.dumps({"challenge": payload["challenge"]}), 200, {"Content-Type": "application/json"})

    # イベント処理
    event = payload.get("event", {})
    print(f"DEBUG: payload_type={payload.get('type')}, event_type={event.get('type')}")
    if event.get("type") == "app_mention":
        # 非同期で処理 (Slackは3秒以内のレスポンスが必要)
        _process_mention(event)

    return ("OK", 200)


# ==============================================================================
# Slackメンション処理
# ==============================================================================
def _process_mention(event: dict) -> None:
    """@ai-agent メンションを解析してエラー分析を実行する"""
    channel_id = event.get("channel")
    thread_ts = event.get("thread_ts") or event.get("ts")
    text = event.get("text", "")
    bot_token = os.environ["SLACK_BOT_TOKEN"]

    # コマンド解析
    text_lower = text.lower()
    if "fix" in text_lower:
        # スレッドのメッセージ履歴を取得
        messages = _get_thread_messages(channel_id, thread_ts, bot_token)
        error_context = _extract_error_context(messages)

        # Claudeでエラー分析
        analysis = _analyze_with_claude(error_context)

        # Slackに分析結果を投稿
        _post_slack_message(
            channel_id,
            thread_ts,
            bot_token,
            f":brain: *AI エラー分析結果*\n\n{analysis['summary']}",
        )

        # 修正可能な場合はGitHub PRを作成
        if analysis.get("should_create_pr") and analysis.get("fix_suggestion"):
            pr_url = _create_github_pr(analysis)
            if pr_url:
                _post_slack_message(
                    channel_id,
                    thread_ts,
                    bot_token,
                    f":github: *自動修正PRを作成しました*\n{pr_url}",
                )

    elif "issue" in text_lower:
        # スレッドのメッセージ履歴を取得
        messages = _get_thread_messages(channel_id, thread_ts, bot_token)
        error_context = _extract_error_context(messages)

        # Claudeでエラー分析
        analysis = _analyze_with_claude(error_context)

        # Slackに分析結果を投稿
        _post_slack_message(
            channel_id,
            thread_ts,
            bot_token,
            f":brain: *AI エラー分析結果*\n\n{analysis['summary']}",
        )

        # GitHub Issueを作成
        issue_url = _create_github_issue(analysis)
        if issue_url:
            _post_slack_message(
                channel_id,
                thread_ts,
                bot_token,
                f":github: *GitHub Issueを作成しました*\n{issue_url}",
            )

    else:
        _post_slack_message(
            channel_id,
            thread_ts,
            bot_token,
            ":wave: こんにちは！使い方:\n• `@relics9-bot fix` - エラーを分析してGitHub PRを作成します\n• `@relics9-bot issue` - エラーをGitHub Issueとして登録します",
        )


# ==============================================================================
# Claude APIでエラー分析
# ==============================================================================
def _analyze_with_claude(error_context: str) -> dict[str, Any]:
    """Claude APIを使ってエラーを分析し、修正案を生成する"""
    client = anthropic.Anthropic(api_key=os.environ["ANTHROPIC_API_KEY"])

    prompt = f"""あなたはGCPのエラーを分析するエキスパートエンジニアです。
以下のエラーログを分析して、JSON形式で回答してください。

エラーコンテキスト:
{error_context}

以下のJSON形式で回答してください:
{{
  "summary": "エラーの概要と原因の説明（Slack表示用、Markdownで）",
  "root_cause": "根本原因の特定",
  "severity": "critical/high/medium/low",
  "should_create_pr": true/false,
  "pr_title": "PRタイトル（should_create_pr=trueの場合）",
  "pr_description": "PR説明（should_create_pr=trueの場合）",
  "fix_suggestion": {{
    "file_path": "修正するファイルパス",
    "description": "修正内容の説明",
    "code_snippet": "修正コードの例"
  }}
}}

注意:
- should_create_pr はコードの修正が明確に特定できる場合のみ true にしてください
- 設定やインフラの問題はコメントのみで PR は作成しないでください
"""

    response = client.messages.create(
        model="claude-opus-4-6",
        max_tokens=2000,
        messages=[{"role": "user", "content": prompt}],
    )

    response_text = response.content[0].text

    # JSONを抽出
    try:
        # コードブロックがある場合は除去
        if "```json" in response_text:
            response_text = response_text.split("```json")[1].split("```")[0]
        elif "```" in response_text:
            response_text = response_text.split("```")[1].split("```")[0]
        else:
            # { } の範囲を探してJSONを抽出
            start = response_text.find("{")
            end = response_text.rfind("}") + 1
            if start != -1 and end > start:
                response_text = response_text[start:end]

        return json.loads(response_text.strip())
    except (json.JSONDecodeError, IndexError):
        return {
            "summary": response_text,
            "should_create_pr": False,
        }


# ==============================================================================
# GitHub PR作成
# ==============================================================================
def _create_github_pr(analysis: dict) -> str | None:
    """GitHub APIを使ってPRを作成する"""
    github_token = os.environ["GITHUB_TOKEN"]
    owner = os.environ["GITHUB_OWNER"]
    repo = os.environ["GITHUB_REPO"]
    base_url = f"https://api.github.com/repos/{owner}/{repo}"

    headers = {
        "Authorization": f"Bearer {github_token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "Content-Type": "application/json",
    }

    fix = analysis.get("fix_suggestion", {})

    # ブランチ名を生成
    branch_name = f"fix/auto-{int(time.time())}"

    try:
        # デフォルトブランチのSHAを取得
        default_branch_sha = _github_request(
            f"{base_url}/git/ref/heads/main", headers=headers
        )["object"]["sha"]

        # 新しいブランチを作成
        _github_request(
            f"{base_url}/git/refs",
            headers=headers,
            method="POST",
            data={"ref": f"refs/heads/{branch_name}", "sha": default_branch_sha},
        )

        # ファイルが指定されている場合は更新 (スペース等の無効文字を含む場合はスキップ)
        file_path = fix.get("file_path")
        if file_path and " " not in file_path and "(" not in file_path:
            # 既存ファイルのSHAを取得 (更新に必要)
            try:
                existing_file = _github_request(
                    f"{base_url}/contents/{file_path}", headers=headers
                )
                file_sha = existing_file.get("sha")
                existing_content = existing_file.get("content", "")
            except Exception:
                file_sha = None
                existing_content = ""

            # ファイルを更新/作成
            import base64
            code_snippet = fix.get("code_snippet", "# Auto-generated fix\n")
            update_data = {
                "message": f"fix: {analysis.get('pr_title', 'Auto-fix from AI agent')}",
                "content": base64.b64encode(code_snippet.encode()).decode(),
                "branch": branch_name,
            }
            if file_sha:
                update_data["sha"] = file_sha

            _github_request(
                f"{base_url}/contents/{file_path}",
                headers=headers,
                method="PUT",
                data=update_data,
            )

        # PRを作成
        pr_body = f"""## 🤖 AI Agent による自動修正PR

### 根本原因
{analysis.get('root_cause', '詳細は分析サマリーを参照')}

### 修正内容
{fix.get('description', '')}

### 分析サマリー
{analysis.get('summary', '')}

---
*このPRはGCPエラーログを検知したAI Agentによって自動作成されました*
"""

        pr_response = _github_request(
            f"{base_url}/pulls",
            headers=headers,
            method="POST",
            data={
                "title": analysis.get("pr_title", "fix: Auto-fix from AI agent"),
                "body": pr_body,
                "head": branch_name,
                "base": "main",
            },
        )

        return pr_response.get("html_url")

    except Exception as e:
        print(f"GitHub PR作成エラー: {e}")
        return None


def _create_github_issue(analysis: dict) -> str | None:
    """GitHub APIを使ってIssueを作成する"""
    github_token = os.environ["GITHUB_TOKEN"]
    owner = os.environ["GITHUB_OWNER"]
    repo = os.environ["GITHUB_REPO"]
    base_url = f"https://api.github.com/repos/{owner}/{repo}"

    headers = {
        "Authorization": f"Bearer {github_token}",
        "Accept": "application/vnd.github+json",
        "X-GitHub-Api-Version": "2022-11-28",
        "Content-Type": "application/json",
    }

    issue_body = f"""## 🤖 AI Agent によるエラー報告

### 根本原因
{analysis.get('root_cause', '詳細は分析サマリーを参照')}

### 分析サマリー
{analysis.get('summary', '')}

### 深刻度
{analysis.get('severity', 'unknown')}

---
*このIssueはGCPエラーログを検知したAI Agentによって自動作成されました*
"""

    try:
        issue_response = _github_request(
            f"{base_url}/issues",
            headers=headers,
            method="POST",
            data={
                "title": analysis.get("pr_title", "bug: GCPエラー検知 from AI agent"),
                "body": issue_body,
                "labels": ["bug", "ai-detected"],
            },
        )
        return issue_response.get("html_url")
    except Exception as e:
        print(f"GitHub Issue作成エラー: {e}")
        return None


# ==============================================================================
# ユーティリティ関数
# ==============================================================================
def _verify_slack_signature(request) -> bool:
    """Slackからのリクエストの署名を検証する"""
    # 開発/テスト時はスキップ (本番では必ず有効にする)
    slack_signing_secret = os.environ.get("SLACK_SIGNING_SECRET")
    if not slack_signing_secret:
        print("警告: SLACK_SIGNING_SECRETが未設定です")
        return True

    timestamp = request.headers.get("X-Slack-Request-Timestamp", "")
    signature = request.headers.get("X-Slack-Signature", "")

    # リプレイ攻撃防止 (5分以上前のリクエストを拒否)
    if abs(time.time() - int(timestamp)) > 300:
        return False

    sig_basestring = f"v0:{timestamp}:{request.get_data(as_text=True)}"
    expected_sig = (
        "v0="
        + hmac.new(
            slack_signing_secret.encode(),
            sig_basestring.encode(),
            hashlib.sha256,
        ).hexdigest()
    )

    return hmac.compare_digest(expected_sig, signature)


def _get_thread_messages(channel_id: str, thread_ts: str, bot_token: str) -> list[dict]:
    """Slackスレッドのメッセージ一覧を取得する"""
    url = f"https://slack.com/api/conversations.replies?channel={channel_id}&ts={thread_ts}"
    req = urllib.request.Request(
        url,
        headers={"Authorization": f"Bearer {bot_token}"},
    )
    with urllib.request.urlopen(req) as resp:
        data = json.loads(resp.read())
    return data.get("messages", [])


def _extract_error_context(messages: list[dict]) -> str:
    """スレッドメッセージからエラーコンテキストを抽出する"""
    texts = []
    for msg in messages:
        text = msg.get("text", "")
        if text:
            texts.append(text)
    return "\n\n".join(texts[:10])  # 最大10件


def _post_slack_message(channel_id: str, thread_ts: str, bot_token: str, text: str) -> None:
    """Slackにメッセージを投稿する"""
    data = json.dumps({
        "channel": channel_id,
        "thread_ts": thread_ts,
        "text": text,
    }).encode("utf-8")
    req = urllib.request.Request(
        "https://slack.com/api/chat.postMessage",
        data=data,
        headers={
            "Authorization": f"Bearer {bot_token}",
            "Content-Type": "application/json",
        },
        method="POST",
    )
    with urllib.request.urlopen(req) as resp:
        result = json.loads(resp.read())
    if not result.get("ok"):
        print(f"Slack投稿エラー: {result.get('error')}")


def _github_request(url: str, headers: dict, method: str = "GET", data: dict = None) -> dict:
    """GitHub APIにリクエストを送る"""
    body = json.dumps(data).encode("utf-8") if data else None
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    with urllib.request.urlopen(req) as resp:
        return json.loads(resp.read())
