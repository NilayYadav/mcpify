package utils

import (
	"fmt"
	"net/http"
	"strings"
)

func HeadersToMap(h http.Header) map[string]string {
	headers := make(map[string]string)
	for k, v := range h {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	return headers
}

func GenerateToolName(method, path string) string {
	safePath := strings.ReplaceAll(path, "/", "_")
	safePath = strings.Trim(safePath, "_")
	return fmt.Sprintf("%s_%s", strings.ToLower(method), safePath)
}
