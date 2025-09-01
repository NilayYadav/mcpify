package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolRegistrar interface {
	RegisterTool(name string, method, url string, headers map[string]string, body []byte, description string) error
}

type MCPServer struct {
	mcpServer *mcp.Server
	tools     map[string]*ToolRequest
	maxTools  int
	mu        sync.RWMutex
}

type ToolRequest struct {
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Body        []byte            `json:"body"`
	Description string            `json:"description"`
	CreatedAt   time.Time         `json:"created_at"`
}

type CallParams struct {
	OverrideBody string `json:"override_body,omitempty"`
}

func NewMCPServer(name, version string, maxTools int) *MCPServer {
	return &MCPServer{
		mcpServer: mcp.NewServer(&mcp.Implementation{
			Name:    name,
			Version: version,
		}, nil),
		tools:    make(map[string]*ToolRequest),
		maxTools: maxTools,
	}
}

func (s *MCPServer) RegisterTool(name string, method, url string, headers map[string]string, body []byte, description string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.tools[name]; exists {
		return nil
	}

	// Enforce max tools limit
	if len(s.tools) >= s.maxTools {
		return fmt.Errorf("maximum number of tools (%d) reached", s.maxTools)
	}

	req := &ToolRequest{
		Method:      method,
		URL:         url,
		Headers:     headers,
		Body:        body,
		Description: description,
		CreatedAt:   time.Now(),
	}

	s.tools[name] = req

	handler := s.createToolHandler(req)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        name,
		Description: req.Description,
	}, handler)

	return nil
}

func (s *MCPServer) createToolHandler(req *ToolRequest) func(context.Context, *mcp.ServerSession, *mcp.CallToolParamsFor[CallParams]) (*mcp.CallToolResultFor[any], error) {
	return func(ctx context.Context, session *mcp.ServerSession, params *mcp.CallToolParamsFor[CallParams]) (*mcp.CallToolResultFor[any], error) {
		// Use override body if provided, otherwise use captured body
		var body []byte
		if params.Arguments.OverrideBody != "" {
			body = []byte(params.Arguments.OverrideBody)
		} else {
			body = req.Body
		}

		httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		// Set headers
		for k, v := range req.Headers {
			httpReq.Header.Set(k, v)
		}

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}

		return &mcp.CallToolResultFor[any]{
			Content: []mcp.Content{
				&mcp.TextContent{
					Text: fmt.Sprintf("Status: %d\nResponse: %s", resp.StatusCode, string(respBody)),
				},
			},
		}, nil
	}
}

func (s *MCPServer) Start(ctx context.Context, addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		tools := make([]*ToolRequest, 0, len(s.tools))
		names := make([]string, 0, len(s.tools))
		for name, tool := range s.tools {
			tools = append(tools, tool)
			names = append(names, name)
		}
		s.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tool_count": len(tools),
			"tool_names": names,
			"tools":      tools,
		})
	})

	mcpHandler := mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
		log.Printf("🔗 MCP connection request from %s to %s", request.RemoteAddr, request.URL.Path)
		return s.mcpServer
	})

	mux.Handle("/mcp", mcpHandler)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		log.Println("Shutting down MCP server...")
		srv.Shutdown(context.Background())
	}()

	log.Printf("MCP server listening on http://localhost%s", addr)
	log.Printf("MCP endpoint: http://localhost%s/mcp", addr)
	log.Printf("Debug endpoint: http://localhost%s/debug", addr)

	return srv.ListenAndServe()
}
