package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NilayYadav/mcpify/internal/config"
	"github.com/NilayYadav/mcpify/internal/grouping"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GroupedMCPServer struct {
	mcpServer *mcp.Server
	grouper   *grouping.LLMGrouper
	config    *config.Config
	mu        sync.RWMutex
}

type GroupCallParams struct {
	Method      string            `json:"method"`
	Path        string            `json:"path,omitempty"`
	RequestBody string            `json:"request_body,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

func NewGroupedMCPServer(name, version string, cfg *config.Config, llmKey, llmEndpoint, llmModel string) *GroupedMCPServer {
	server := &GroupedMCPServer{
		mcpServer: mcp.NewServer(&mcp.Implementation{
			Name:    name,
			Version: version,
		}, nil),
		grouper: grouping.NewLLMGrouper(llmKey, llmEndpoint, llmModel),
		config:  cfg,
	}

	// Load existing groups or create them
	server.setupGroups()
	return server
}

func (s *GroupedMCPServer) RegisterTool(name string, method, url string, headers map[string]string, body []byte, description string) error {
	tool := &config.Tool{
		Name:        name,
		Method:      method,
		URL:         url,
		Headers:     headers,
		Body:        string(body),
		Description: description,
		CreatedAt:   time.Now(),
	}

	s.config.AddTool(tool)

	if err := s.config.Save(s.config.Path); err != nil {
		log.Printf("Failed to save config: %v", err)
	}

	// Trigger regrouping in background (only if we have enough tools)
	if len(s.config.Tools) >= 5 { // Only regroup when we have enough tools
		go s.rebuildGroups()
	}

	return nil
}

func (s *GroupedMCPServer) setupGroups() {
	if s.config.UseGrouping && len(s.config.Groups) > 0 {
		// Load existing groups from config
		s.loadGroupsFromConfig()
	} else if len(s.config.Tools) >= 3 {
		// Create initial groups
		s.rebuildGroups()
	}
}

func (s *GroupedMCPServer) loadGroupsFromConfig() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, group := range s.config.Groups {
		tools := s.config.GetToolsInGroup(group.Name)
		if len(tools) > 0 {
			handler := s.createGroupHandler(group.Name)
			mcp.AddTool(s.mcpServer, &mcp.Tool{
				Name:        group.Name,
				Description: s.generateToolDescription(group, tools),
			}, handler)

			log.Printf("Loaded group: %s with %d tools", group.Name, len(tools))
		}
	}
}

func (s *GroupedMCPServer) rebuildGroups() {
	if err := s.grouper.GroupToolsInConfig(s.config); err != nil {
		log.Printf("Failed to group tools: %v", err)
		return
	}

	// Reload groups from config
	s.loadGroupsFromConfig()
}

func (s *GroupedMCPServer) generateToolDescription(group *config.Group, tools []*config.Tool) string {
	description := group.Description + "\n\n"
	description += "Available endpoints:\n"

	for _, tool := range tools {
		description += fmt.Sprintf("- %s %s\n", tool.Method, tool.URL)
	}

	description += "\nUsage: Specify 'method' (GET/POST/PUT/DELETE) and optionally 'path' for specific endpoint. "
	description += "Include 'request_body' and 'headers' as needed."

	return description
}

func (s *GroupedMCPServer) createGroupHandler(groupName string) func(context.Context, *mcp.ServerSession, *mcp.CallToolParamsFor[GroupCallParams]) (*mcp.CallToolResultFor[any], error) {
	return func(ctx context.Context, session *mcp.ServerSession, params *mcp.CallToolParamsFor[GroupCallParams]) (*mcp.CallToolResultFor[any], error) {

		// Find the right tool
		tool, err := s.selectTool(groupName, params.Arguments)
		if err != nil {
			return nil, fmt.Errorf("tool selection failed: %w", err)
		}

		// Execute the request
		result, err := s.executeRequest(ctx, tool, params.Arguments)
		if err != nil {
			return nil, err
		}

		// Update usage stats
		s.updateUsageStats(groupName, tool)

		return result, nil
	}
}

func (s *GroupedMCPServer) selectTool(groupName string, params GroupCallParams) (*config.Tool, error) {
	tools := s.config.GetToolsInGroup(groupName)
	if len(tools) == 0 {
		return nil, fmt.Errorf("no tools found in group %s", groupName)
	}

	// Method is required
	if params.Method == "" {
		return nil, fmt.Errorf("method parameter is required")
	}

	// If path is specified, find exact match
	if params.Path != "" {
		for _, tool := range tools {
			if strings.EqualFold(tool.Method, params.Method) && strings.Contains(tool.URL, params.Path) {
				return tool, nil
			}
		}
		return nil, fmt.Errorf("no tool found for method %s and path %s", params.Method, params.Path)
	}

	// Find first tool with matching method
	for _, tool := range tools {
		if strings.EqualFold(tool.Method, params.Method) {
			return tool, nil
		}
	}

	return nil, fmt.Errorf("no tool found for method %s", params.Method)
}

func (s *GroupedMCPServer) executeRequest(ctx context.Context, tool *config.Tool, params GroupCallParams) (*mcp.CallToolResultFor[any], error) {
	// Prepare request body
	var body []byte
	if params.RequestBody != "" {
		body = []byte(params.RequestBody)
	} else {
		body = []byte(tool.Body)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, tool.Method, tool.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers (tool defaults, then override with params)
	for k, v := range tool.Headers {
		httpReq.Header.Set(k, v)
	}
	for k, v := range params.Headers {
		httpReq.Header.Set(k, v)
	}

	// Execute request
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

func (s *GroupedMCPServer) updateUsageStats(groupName string, tool *config.Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if configTool := s.config.Tools[tool.Name]; configTool != nil {
		configTool.UseCount++
		configTool.LastUsed = time.Now()
	}

	if configGroup := s.config.Groups[groupName]; configGroup != nil {
		configGroup.UseCount++
		configGroup.LastUsed = time.Now()
	}
}

func (s *GroupedMCPServer) Start(ctx context.Context, addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/debug", func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		groups := make(map[string]*config.Group)
		for name, group := range s.config.Groups {
			groups[name] = group
		}
		s.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"group_count": len(groups),
			"groups":      groups,
			"tools_count": len(s.config.Tools),
		})
	})

	mcpHandler := mcp.NewSSEHandler(func(request *http.Request) *mcp.Server {
		log.Printf("🔗 MCP connection from %s", request.RemoteAddr)
		return s.mcpServer
	})

	mux.Handle("/mcp", mcpHandler)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		log.Println("Shutting down server...")
		srv.Shutdown(context.Background())
	}()

	log.Printf("MCP server with grouping on http://localhost%s", addr)
	log.Printf("Debug: http://localhost%s/debug", addr)

	return srv.ListenAndServe()
}
