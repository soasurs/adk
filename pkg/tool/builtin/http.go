package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"soasurs.dev/soasurs/adk/pkg/tool"
)

func HTTPTool() tool.Tool {
	return tool.NewTool(
		"http_request",
		"Make HTTP requests to external APIs",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method": map[string]any{
					"type":        "string",
					"description": "HTTP method (GET, POST, PUT, DELETE, etc.)",
					"enum":        []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"},
				},
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to send the request to",
				},
				"headers": map[string]any{
					"type":        "object",
					"description": "HTTP headers as key-value pairs",
				},
				"body": map[string]any{
					"type":        "string",
					"description": "Request body (for POST/PUT/PATCH)",
				},
				"timeout": map[string]any{
					"type":        "integer",
					"description": "Request timeout in seconds",
				},
			},
			"required": []string{"method", "url"},
		},
		func(ctx context.Context, args map[string]any) (any, error) {
			method, ok := args["method"].(string)
			if !ok {
				return nil, fmt.Errorf("method is required and must be a string")
			}

			url, ok := args["url"].(string)
			if !ok {
				return nil, fmt.Errorf("url is required and must be a string")
			}

			timeout := 30 * time.Second
			if t, ok := args["timeout"].(int); ok {
				timeout = time.Duration(t) * time.Second
			}

			var bodyReader io.Reader
			if body, ok := args["body"].(string); ok && body != "" {
				bodyReader = strings.NewReader(body)
			}

			req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
			if err != nil {
				return nil, fmt.Errorf("create request: %w", err)
			}

			if headers, ok := args["headers"].(map[string]any); ok {
				for k, v := range headers {
					if s, ok := v.(string); ok {
						req.Header.Set(k, s)
					}
				}
			}

			if bodyReader != nil && req.Header.Get("Content-Type") == "" {
				req.Header.Set("Content-Type", "application/json")
			}

			client := &http.Client{Timeout: timeout}
			resp, err := client.Do(req)
			if err != nil {
				return nil, fmt.Errorf("execute request: %w", err)
			}
			defer resp.Body.Close()

			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("read response: %w", err)
			}

			result := map[string]any{
				"status":     resp.Status,
				"statusCode": resp.StatusCode,
				"headers":    resp.Header,
				"body":       string(respBody),
			}

			var jsonBody any
			if err := json.Unmarshal(respBody, &jsonBody); err == nil {
				result["json"] = jsonBody
			}

			return result, nil
		},
	)
}
