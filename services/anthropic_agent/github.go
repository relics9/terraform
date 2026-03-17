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

// resolveRepo returns the repository name for the given service name from the REPO_MAP env var.
// REPO_MAP format: "example-api=example,foo-svc=foo"
// Returns an empty string if no mapping is found.
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
	owner := os.Getenv("GITHUB_USER")
	repo := resolveRepo(getStr(analysis, "service_name"))
	if repo == "" {
		log.Printf("GitHub PR creation failed: could not resolve repo for service_name=%s", getStr(analysis, "service_name"))
		return ""
	}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)

	headers := githubHeaders(token)
	branchName := fmt.Sprintf("fix/auto-%d", time.Now().Unix())

	// Get SHA of the default branch
	refResp, err := githubRequest("GET", baseURL+"/git/ref/heads/main", headers, nil)
	if err != nil {
		log.Printf("GitHub ref fetch error: %v", err)
		return ""
	}
	sha := refResp["object"].(map[string]interface{})["sha"].(string)

	// Create new branch
	if _, err := githubRequest("POST", baseURL+"/git/refs", headers, map[string]interface{}{
		"ref": "refs/heads/" + branchName,
		"sha": sha,
	}); err != nil {
		log.Printf("GitHub branch creation error: %v", err)
		return ""
	}

	// Update file if specified
	committed := false
	if fix, ok := analysis["fix_suggestion"].(map[string]interface{}); ok {
		filePath := getStr(fix, "file_path")
		if filePath != "" && !strings.Contains(filePath, " ") && !strings.Contains(filePath, "(") {
			codeSnippet := getStrOr(fix, "code_snippet", "# Auto-generated fix\n")
			updateData := map[string]interface{}{
				"message": fmt.Sprintf("fix: %s", getStrOr(analysis, "pr_title", "Auto-fix from AI agent")),
				"content": base64.StdEncoding.EncodeToString([]byte(codeSnippet)),
				"branch":  branchName,
			}
			// Get existing file SHA (required for updates)
			if existing, err := githubRequest("GET", baseURL+"/contents/"+filePath, headers, nil); err == nil {
				if fileSHA, ok := existing["sha"].(string); ok {
					updateData["sha"] = fileSHA
				}
			}
			if _, err := githubRequest("PUT", baseURL+"/contents/"+filePath, headers, updateData); err != nil {
				log.Printf("GitHub file update error: %v", err)
			} else {
				committed = true
			}
		}
	}

	// No commits on branch — clean up and abort
	if !committed {
		log.Printf("GitHub PR creation skipped: no file committed to branch %s", branchName)
		githubRequest("DELETE", baseURL+"/git/refs/heads/"+branchName, headers, nil)
		return ""
	}

	// Create PR
	fix, _ := analysis["fix_suggestion"].(map[string]interface{})
	fixDesc := ""
	if fix != nil {
		fixDesc = getStr(fix, "description")
	}
	prBody := fmt.Sprintf(`## 🤖 Auto-fix PR by AI Agent

### Root Cause
%s

### Fix Description
%s

### Analysis Summary
%s

---
*This PR was automatically created by the AI Agent upon detecting a GCP error log. Model: claude-opus-4-6*`,
		getStr(analysis, "root_cause"), fixDesc, getStr(analysis, "summary"))

	prResp, err := githubRequest("POST", baseURL+"/pulls", headers, map[string]interface{}{
		"title": getStrOr(analysis, "pr_title", "fix: Auto-fix from AI agent"),
		"body":  prBody,
		"head":  branchName,
		"base":  "main",
	})
	if err != nil {
		log.Printf("GitHub PR creation error: %v", err)
		return ""
	}

	htmlURL, _ := prResp["html_url"].(string)
	return htmlURL
}

func createGitHubIssue(analysis map[string]interface{}) string {
	token := os.Getenv("GITHUB_TOKEN")
	owner := os.Getenv("GITHUB_USER")
	repo := resolveRepo(getStr(analysis, "service_name"))
	if token == "" || owner == "" || repo == "" {
		log.Printf("GitHub Issue creation failed: missing config GITHUB_TOKEN=%v GITHUB_USER=%v repo=%q service_name=%q",
			token != "", owner != "", repo, getStr(analysis, "service_name"))
		return ""
	}
	baseURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)

	issueBody := fmt.Sprintf(`## 🤖 Error Report by AI Agent

### Root Cause
%s

### Analysis Summary
%s

### Severity
%s

---
*This Issue was automatically created by the AI Agent upon detecting a GCP error log. Model: claude-opus-4-6*`,
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
		title = "bug: GCP error detected by AI agent"
	}

	resp, err := githubRequest("POST", baseURL+"/issues", githubHeaders(token), map[string]interface{}{
		"title": title,
		"body":  issueBody,
	})
	if err != nil {
		log.Printf("GitHub Issue creation error: %v", err)
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
		log.Printf("[GitHub OUT] error: %v", err)
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
