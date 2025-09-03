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
	"github.com/NilayYadav/mcpify/internal/server"
)

func main() {
	var (
		target   = flag.String("target", "", "Target server URL to observe (required)")
		mcpPort  = flag.String("mcp-port", "8081", "MCP server port")
		verbose  = flag.Bool("verbose", false, "Enable verbose logging")
		maxTools = flag.Int("max-tools", 100, "Maximum number of tools to capture")
		useLLM   = flag.Bool("use-llm", false, "Enable LLM for tool name generation")
	)
	flag.Parse()

	if *target == "" {
		log.Fatal("Target server URL required. Usage: mcpify --target http://localhost:3000")
	}

	targetURL, err := url.Parse(*target)
	if err != nil {
		log.Fatalf("Invalid target URL: %v", err)
	}

	if err := checkTargetServer(*target); err != nil {
		log.Fatalf("Target server check failed: %v", err)
		log.Printf("Make sure your server is running at %s", *target)
	}

	llm := os.Getenv("LLM")
	llmEndpoint := os.Getenv("LLM_ENDPOINT")
	llmKey := os.Getenv("LLM_API_KEY")

	if *useLLM {
		if llm == "" {
			log.Fatal(`LLM model required when using LLM. Set the LLM environment variable: export LLM="your-llm-model"`)
		}

		if llmEndpoint == "" {
			log.Fatal(`LLM endpoint required when using LLM. Set the LLM_ENDPOINT environment variable: export LLM_ENDPOINT="https://your-llm-provider-endpoint"`)
		}

		if llmKey == "" {
			log.Fatal(`LLM API key required when using LLM. Set the LLM_API_KEY environment variable: export LLM_API_KEY="your-api-key-here"`)
		}

		log.Printf("Using LLM model: %s", llm)
		log.Printf("Using LLM endpoint: %s", llmEndpoint)
	}

	mcpServer := server.NewMCPServer("mcpify", "1.0.0", *maxTools)

	endpointCapture := capture.NewEndpointCapture(targetURL, mcpServer, *useLLM, llmKey, llmEndpoint, llm)

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
