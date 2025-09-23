package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/NilayYadav/mcpify/internal/capture"
	"github.com/NilayYadav/mcpify/internal/config"
	"github.com/NilayYadav/mcpify/internal/server"
)

var mcpServer interface {
	RegisterTool(name string, method, url string, headers map[string]string, body []byte, description string) error
	Start(ctx context.Context, addr string) error
}

func main() {
	var (
		target     = flag.String("target", "", "Target server URL to observe (required)")
		mcpPort    = flag.String("mcp-port", "8081", "MCP server port")
		verbose    = flag.Bool("verbose", false, "Enable verbose logging")
		maxTools   = flag.Int("max-tools", 100, "Maximum number of tools to capture")
		useLLM     = flag.Bool("use-llm", false, "Enable LLM for tool name generation")
		mcpName    = flag.String("mcp-name", "mcpify", "Name of the MCP server")
		configPath = flag.String("config", "", "Custom config file path")
		grouping   = flag.Bool("grouping", false, "Enable intelligent grouping of endpoints using LLM")
	)
	flag.Parse()

	var finalConfigPath string
	if *configPath != "" {
		finalConfigPath = *configPath
	} else {
		finalConfigPath = config.GetConfigPath()
	}

	log.Printf("Using config file: %s", finalConfigPath)

	cfg, err := config.LoadConfig(finalConfigPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	targetURL := *target
	if targetURL == "" && cfg.LastTarget != "" {
		targetURL = cfg.LastTarget
		log.Printf("Using saved target: %s", targetURL)
	}

	if targetURL == "" {
		log.Fatal("Target server URL required. Usage: mcpify --target http://localhost:3000")
	}

	// Update config if new target provided
	if *target != "" && *target != cfg.LastTarget {
		cfg.LastTarget = *target
		cfg.Save(finalConfigPath)
	}

	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		log.Fatalf("Invalid target URL: %v", err)
	}

	if err := checkTargetServer(targetURL); err != nil {
		log.Fatalf("Target server check failed: %v", err)
	}

	llm := os.Getenv("LLM")
	llmEndpoint := os.Getenv("LLM_ENDPOINT")
	llmKey := os.Getenv("LLM_API_KEY")

	if *useLLM || *grouping {
		if llm == "" {
			log.Fatal(`LLM model required when using LLM or grouping. Set the LLM environment variable: export LLM="your-llm-model"`)
		}

		if llmEndpoint == "" {
			log.Fatal(`LLM endpoint required when using LLM or grouping. Set the LLM_ENDPOINT environment variable: export LLM_ENDPOINT="https://your-llm-provider-endpoint"`)
		}

		if llmKey == "" {
			log.Fatal(`LLM API key required when using LLM or grouping . Set the LLM_API_KEY environment variable: export LLM_API_KEY="your-api-key-here"`)
		}

		log.Printf("Using LLM model: %s", llm)
		log.Printf("Using LLM endpoint: %s", llmEndpoint)
	}

	if *grouping {
		log.Printf("Using LLM grouping with model: %s", llm)
		mcpServer = server.NewGroupedMCPServer(*mcpName, "1.0.0", cfg, llmKey, llmEndpoint, llm)
	} else {
		log.Printf("Using individual tool mode")
		mcpServer = server.NewMCPServer(*mcpName, "1.0.0", *maxTools, cfg)
	}

	endpointCapture := capture.NewEndpointCapture(parsedURL, mcpServer, *useLLM, llmKey, llmEndpoint, llm)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		addr := ":" + *mcpPort
		log.Printf("MCP server starting on http://localhost%s/mcp", addr)
		if err := mcpServer.Start(ctx, addr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("MCP server failed: %v", err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Println("Shutting down mcpify...")
		cancel()
		os.Exit(0)
	}()

	log.Printf("Observing traffic to %s", *target)
	log.Printf("Discovered endpoints will be available as MCP tools")

	if err := endpointCapture.StartCapture(*verbose); err != nil {
		log.Fatalf("Failed to start capture: %v", err)
	}
}

func checkTargetServer(target string) error {
	log.Printf("Checking target server at %s", target)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Head(target)

	if err != nil {
		return fmt.Errorf("server not reachable: %v", err)

	}
	defer resp.Body.Close()

	log.Printf("Target server response: %s", resp.Status)
	return nil
}
