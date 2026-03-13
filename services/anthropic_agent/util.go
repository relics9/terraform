package main

import (
	"fmt"
	"net/url"
)

func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getStrOr(m map[string]interface{}, key, defaultVal string) string {
	if v := getStr(m, key); v != "" {
		return v
	}
	return defaultVal
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func buildLoggingURL(projectID, logName, insertID, timestamp string) string {
	var query string
	if insertID != "" {
		query = fmt.Sprintf(`insertId="%s"`, insertID)
	} else if logName != "" && timestamp != "" {
		query = fmt.Sprintf(`logName="%s" timestamp="%s"`, logName, timestamp)
	} else if logName != "" {
		query = fmt.Sprintf(`logName="%s"`, logName)
	} else {
		query = "severity>=ERROR"
	}
	return fmt.Sprintf(
		"https://console.cloud.google.com/logs/query;query=%s?project=%s",
		url.PathEscape(query),
		projectID,
	)
}
