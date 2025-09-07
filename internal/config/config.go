package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type Config struct {
	mu         sync.RWMutex
	Path       string           `json:"-"`
	MCPPort    string           `json:"mcp_port"`
	MaxTools   int              `json:"max_tools"`
	UseLLM     bool             `json:"use_llm"`
	LastTarget string           `json:"last_target"`
	Tools      map[string]*Tool `json:"tools"`
}

type Tool struct {
	Name        string            `json:"name"`
	Method      string            `json:"method"`
	URL         string            `json:"url"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	Description string            `json:"description"`
	CreatedAt   time.Time         `json:"created_at"`
	LastUsed    time.Time         `json:"last_used,omitempty"`
	UseCount    int               `json:"use_count"`
}

func DefaultConfig(configPath string) *Config {
	return &Config{
		Path:     configPath,
		MCPPort:  "8081",
		MaxTools: 100,
		UseLLM:   true,
		Tools:    make(map[string]*Tool),
	}
}

func GetConfigPath() string {

	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		log.Fatalf("Unsupported operating system: %s. Only macOS and Linux are supported.", runtime.GOOS)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Could not determine home directory: %v", err)
	}

	var configDir string
	if runtime.GOOS == "darwin" {
		// macOS: ~/Library/Application Support/mcpify
		configDir = filepath.Join(homeDir, "Library", "Application Support", "mcpify")
	} else {
		// Linux: ~/.config/mcpify
		if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
			configDir = filepath.Join(xdgConfig, "mcpify")
		} else {
			configDir = filepath.Join(homeDir, ".config", "mcpify")
		}
	}

	return filepath.Join(configDir, "config.json")
}

func LoadConfig(configPath string) (*Config, error) {
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return nil, err
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		cfg := DefaultConfig(configPath)
		if err := cfg.Save(configPath); err != nil {
			return nil, err
		}
		return cfg, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	cfg := DefaultConfig(configPath)
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	if cfg.Tools == nil {
		cfg.Tools = make(map[string]*Tool)
	}

	return cfg, nil
}

func (c *Config) Save(configPath string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

func (c *Config) AddTool(tool *Tool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Tools[tool.Name] = tool
}

func (c *Config) RemoveTool(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Tools, name)
}

func (c *Config) GetTool(name string) *Tool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Tools[name]
}
