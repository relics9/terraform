"""
Cloud Function: Slack Notifier
Pub/Sub経由でGCPエラーログを受け取り、Slackに通知する
"""
import base64
import json
import os
import urllib.request
import urllib.error
from datetime import datetime, timezone

import functions_framework


@functions_framework.cloud_event
def notify_slack(cloud_event):
    """Pub/SubメッセージからエラーログをSlackに通知する"""
    webhook_url = os.environ["SLACK_WEBHOOK_URL"]

    # Pub/Subメッセージをデコード
    pubsub_message = cloud_event.data.get("message", {})
    message_data = base64.b64decode(pubsub_message.get("data", "")).decode("utf-8")

    try:
        log_entry = json.loads(message_data)
    except json.JSONDecodeError:
        log_entry = {"textPayload": message_data}

    # ログ情報を抽出
    severity = log_entry.get("severity", "ERROR")
    resource = log_entry.get("resource", {})
    resource_type = resource.get("type", "unknown")
    resource_labels = resource.get("labels", {})

    # エラーメッセージを取得
    error_message = (
        log_entry.get("textPayload")
        or log_entry.get("jsonPayload", {}).get("message")
        or log_entry.get("protoPayload", {}).get("status", {}).get("message")
        or "詳細不明のエラー"
    )

    timestamp = log_entry.get("timestamp", datetime.now(timezone.utc).isoformat())
    log_name = log_entry.get("logName", "")
    trace = log_entry.get("trace", "")

    # Cloud Loggingへのリンク
    project_id = os.environ.get("PROJECT_ID", "relics9")
    logging_url = (
        f"https://console.cloud.google.com/logs/query"
        f";query=severity%3E%3DERROR"
        f"?project={project_id}"
    )

    # Slackメッセージを構築 (Block Kit)
    severity_emoji = {
        "CRITICAL": ":red_circle:",
        "ALERT": ":red_circle:",
        "EMERGENCY": ":sos:",
        "ERROR": ":warning:",
        "WARNING": ":large_yellow_circle:",
    }.get(severity, ":warning:")

    slack_message = {
        "blocks": [
            {
                "type": "header",
                "text": {
                    "type": "plain_text",
                    "text": f"{severity_emoji} GCPエラー検知: {severity}",
                },
            },
            {
                "type": "section",
                "fields": [
                    {"type": "mrkdwn", "text": f"*リソース種別:*\n{resource_type}"},
                    {"type": "mrkdwn", "text": f"*時刻:*\n{timestamp}"},
                ],
            },
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": f"*エラーメッセージ:*\n```{error_message[:500]}```",
                },
            },
            {
                "type": "section",
                "fields": [
                    {"type": "mrkdwn", "text": f"*ログ名:*\n`{log_name}`"},
                    {
                        "type": "mrkdwn",
                        "text": f"*ラベル:*\n`{json.dumps(resource_labels, ensure_ascii=False)}`",
                    },
                ],
            },
            {
                "type": "actions",
                "elements": [
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "Cloud Loggingで確認"},
                        "url": logging_url,
                        "style": "danger",
                    }
                ],
            },
            {"type": "divider"},
            {
                "type": "context",
                "elements": [
                    {
                        "type": "mrkdwn",
                        "text": ":robot_face: このメッセージのスレッド内でメンションしてください\n• `@relics9-bot fix` - AI分析 & GitHub PRを自動作成\n• `@relics9-bot issue` - AI分析 & GitHub Issueを作成",
                    }
                ],
            },
        ]
    }

    # Slackに送信
    _send_to_slack(webhook_url, slack_message)
    print(f"Slack通知送信完了: severity={severity}, resource={resource_type}")


def _send_to_slack(webhook_url: str, message: dict) -> None:
    """Slack Webhook APIにメッセージを送信する"""
    data = json.dumps(message).encode("utf-8")
    req = urllib.request.Request(
        webhook_url,
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(req) as response:
            if response.status != 200:
                raise ValueError(f"Slack APIエラー: {response.status}")
    except urllib.error.HTTPError as e:
        raise RuntimeError(f"Slack送信失敗: {e.code} {e.reason}") from e
