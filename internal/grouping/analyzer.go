package grouping

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/NilayYadav/mcpify/internal/config"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

type LLMGrouper struct {
	llmClient *openai.Client
	llmModel  string
}

func NewLLMGrouper(llmKey, llmEndpoint, llmModel string) *LLMGrouper {
	client := openai.NewClient(
		option.WithBaseURL(llmEndpoint),
		option.WithAPIKey(llmKey),
	)

	return &LLMGrouper{
		llmClient: &client,
		llmModel:  llmModel,
	}
}

func (lg *LLMGrouper) GroupToolsInConfig(cfg *config.Config) error {
	cfg.ClearGroups()

	tools := make([]*config.Tool, 0, len(cfg.Tools))
	for _, tool := range cfg.Tools {
		tools = append(tools, tool)
	}

	if len(tools) == 0 {
		return nil
	}

	log.Printf("Analyzing %d tools for intelligent grouping...", len(tools))

	// Prepare tools data for LLM analysis
	toolsData := make([]map[string]interface{}, len(tools))
	for i, tool := range tools {
		toolsData[i] = map[string]interface{}{
			"name":        tool.Name,
			"method":      tool.Method,
			"path":        lg.extractPath(tool.URL),
			"description": tool.Description,
		}
	}

	toolsJSON, _ := json.MarshalIndent(toolsData, "", "  ")

	systemPrompt := `You are an API analysis expert. Group related API endpoints into logical, workflow-oriented tools.

Rules:
1. Create 3-7 groups maximum, regardless of API size
2. Group by business function/capability, not technical patterns  
3. Each group should represent what a user wants to accomplish
4. Prefer fewer, more powerful groups over many small ones
5. Important standalone endpoints (health, webhooks) can be their own group

Output ONLY valid JSON in this exact format:
{
  "groups": [
    {
      "name": "user_management", 
      "description": "Complete user lifecycle operations including creation, updates, and deletion",
      "tool_names": ["create_user", "get_user", "update_user", "delete_user", "list_users", "search_users"]
    }
  ]
}

Group names should be snake_case. Use the exact tool names from the input.`

	prompt := fmt.Sprintf("Analyze and group these API tools:\n%s", string(toolsJSON))

	chatCompletion, err := lg.llmClient.Chat.Completions.New(context.TODO(), openai.ChatCompletionNewParams{
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(systemPrompt),
			openai.UserMessage(prompt),
		},
		Model:       lg.llmModel,
		Temperature: openai.Float(0.1),
	})

	if err != nil {
		return fmt.Errorf("LLM grouping failed: %w", err)
	}

	response := chatCompletion.Choices[0].Message.Content
	log.Printf("LLM grouping response received")

	var result struct {
		Groups []struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			ToolNames   []string `json:"tool_names"`
		} `json:"groups"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return fmt.Errorf("failed to parse LLM response: %w", err)
	}

	// Add groups to config
	for _, llmGroup := range result.Groups {
		// Validate tool names exist
		validToolNames := []string{}
		for _, toolName := range llmGroup.ToolNames {
			if cfg.GetTool(toolName) != nil {
				validToolNames = append(validToolNames, toolName)
			}
		}

		if len(validToolNames) > 0 {
			group := &config.Group{
				Name:        llmGroup.Name,
				Description: llmGroup.Description,
				ToolNames:   validToolNames,
				CreatedAt:   time.Now(),
			}
			cfg.AddGroup(group)
			log.Printf("Created group '%s' with %d tools", group.Name, len(group.ToolNames))
		}
	}

	cfg.UseGrouping = true
	return cfg.Save(cfg.Path)
}

func (lg *LLMGrouper) extractPath(fullURL string) string {
	if !strings.Contains(fullURL, "://") {
		return fullURL
	}

	u, err := url.Parse(fullURL)
	if err != nil {
		return fullURL
	}
	return u.Path
}
