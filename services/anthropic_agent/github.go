package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// resolveRepo は REPO_MAP 環境変数からサービス名に対応するリポジトリ名を返す。
// REPO_MAP 形式: "example-api=example,foo-svc=foo"
// 見つからない場合は空文字を返す。
func resolveRepo(serviceName string) string {
	if repoMap := os.Getenv("REPO_MAP"); repoMap != "" {
		for _, entry := range strings.Split(repoMap, ",") {
			parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == serviceName {
				log.Printf("[resolveRepo] REPO_MAP hit: %s -> %s", serviceName, parts[1])
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func createGitHubPR(analysis map[string]interface{}) string {
	token := os.Getenv("GITHUB_TOKEN")
	owner := os.Getenv("GITHUB_OWNER")
	repo := resolveRepo(getStr(analysis, "service_name"))
	if repo == "" {
		log.Printf("GitHub PR作成エラー: リポジトリ解決失敗 service_name=%s", getStr(analysis, "service_name"))
		return ""
	}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)

	headers := githubHeaders(token)
	branchName := fmt.Sprintf("fix/auto-%d", time.Now().Unix())

	// デフォルトブランチのSHAを取得
	refResp, err := githubRequest("GET", baseURL+"/git/ref/heads/main", headers, nil)
	if err != nil {
		log.Printf("GitHub ref取得エラー: %v", err)
		return ""
	}
	sha := refResp["object"].(map[string]interface{})["sha"].(string)

	// 新しいブランチを作成
	if _, err := githubRequest("POST", baseURL+"/git/refs", headers, map[string]interface{}{
		"ref": "refs/heads/" + branchName,
		"sha": sha,
	}); err != nil {
		log.Printf("GitHub ブランチ作成エラー: %v", err)
		return ""
	}

	// ファイルが指定されている場合は更新
	if fix, ok := analysis["fix_suggestion"].(map[string]interface{}); ok {
		filePath := getStr(fix, "file_path")
		if filePath != "" && !strings.Contains(filePath, " ") && !strings.Contains(filePath, "(") {
			codeSnippet := getStrOr(fix, "code_snippet", "# Auto-generated fix\n")
			updateData := map[string]interface{}{
				"message": fmt.Sprintf("fix: %s", getStrOr(analysis, "pr_title", "Auto-fix from AI agent")),
				"content": base64.StdEncoding.EncodeToString([]byte(codeSnippet)),
				"branch":  branchName,
			}
			// 既存ファイルのSHAを取得 (更新に必要)
			if existing, err := githubRequest("GET", baseURL+"/contents/"+filePath, headers, nil); err == nil {
				if fileSHA, ok := existing["sha"].(string); ok {
					updateData["sha"] = fileSHA
				}
			}
			if _, err := githubRequest("PUT", baseURL+"/contents/"+filePath, headers, updateData); err != nil {
				log.Printf("GitHub ファイル更新エラー: %v", err)
			}
		}
	}

	// PRを作成
	fix, _ := analysis["fix_suggestion"].(map[string]interface{})
	fixDesc := ""
	if fix != nil {
		fixDesc = getStr(fix, "description")
	}
	prBody := fmt.Sprintf(`## 🤖 AI Agent による自動修正PR

### 根本原因
%s

### 修正内容
%s

### 分析サマリー
%s

---
*このPRはGCPエラーログを検知したAI Agentによって自動作成されました*`,
		getStr(analysis, "root_cause"), fixDesc, getStr(analysis, "summary"))

	prResp, err := githubRequest("POST", baseURL+"/pulls", headers, map[string]interface{}{
		"title": getStrOr(analysis, "pr_title", "fix: Auto-fix from AI agent"),
		"body":  prBody,
		"head":  branchName,
		"base":  "main",
	})
	if err != nil {
		log.Printf("GitHub PR作成エラー: %v", err)
		return ""
	}

	htmlURL, _ := prResp["html_url"].(string)
	return htmlURL
}

func createGitHubIssue(analysis map[string]interface{}) string {
	token := os.Getenv("GITHUB_TOKEN")
	owner := os.Getenv("GITHUB_OWNER")
	repo := resolveRepo(getStr(analysis, "service_name"))
	if token == "" || owner == "" || repo == "" {
		log.Printf("GitHub Issue作成エラー: 環境変数未設定 GITHUB_TOKEN=%v GITHUB_OWNER=%v repo=%q service_name=%q",
			token != "", owner != "", repo, getStr(analysis, "service_name"))
		return ""
	}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)

	issueBody := fmt.Sprintf(`## 🤖 AI Agent によるエラー報告

### 根本原因
%s

### 分析サマリー
%s

### 深刻度
%s

---
*このIssueはGCPエラーログを検知したAI Agentによって自動作成されました*`,
		getStr(analysis, "root_cause"),
		getStr(analysis, "summary"),
		getStrOr(analysis, "severity", "unknown"))

	title := getStr(analysis, "pr_title")
	if title == "" {
		title = getStr(analysis, "root_cause")
		if len(title) > 80 {
			title = title[:80]
		}
	}
	if title == "" {
		title = "bug: GCPエラー検知 from AI agent"
	}

	resp, err := githubRequest("POST", baseURL+"/issues", githubHeaders(token), map[string]interface{}{
		"title": title,
		"body":  issueBody,
	})
	if err != nil {
		log.Printf("GitHub Issue作成エラー: %v", err)
		return ""
	}

	htmlURL, _ := resp["html_url"].(string)
	return htmlURL
}

func githubHeaders(token string) map[string]string {
	return map[string]string{
		"Authorization":        "Bearer " + token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
		"Content-Type":         "application/json",
	}
}

func githubRequest(method, url string, headers map[string]string, data interface{}) (map[string]interface{}, error) {
	var body io.Reader
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	log.Printf("[GitHub OUT] %s %s", method, url)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[GitHub OUT] エラー: %v", err)
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		limit := 500
		if len(respBody) < limit {
			limit = len(respBody)
		}
		log.Printf("[GitHub OUT] HTTP %d: %s", resp.StatusCode, string(respBody[:limit]))
		return nil, fmt.Errorf("HTTP Error %d: %s", resp.StatusCode, string(respBody[:limit]))
	}

	log.Printf("[GitHub OUT] HTTP %d OK", resp.StatusCode)
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}
