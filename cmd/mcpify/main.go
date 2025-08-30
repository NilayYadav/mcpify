package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/NilayYadav/mcpify/internal/capture"
	"github.com/NilayYadav/mcpify/internal/server"
)

func main() {
	var (
		target   = flag.String("target", "", "Target server URL to observe (required)")
		mcpPort  = flag.String("mcp-port", "8081", "MCP server port")
		verbose  = flag.Bool("verbose", false, "Enable verbose logging")
		maxTools = flag.Int("max-tools", 100, "Maximum number of tools to capture")
	)
	flag.Parse()

	if *target == "" {
		log.Fatal("Target server URL required. Usage: mcpify --target http://localhost:3000")
	}

	targetURL, err := url.Parse(*target)
	if err != nil {
		log.Fatalf("Invalid target URL: %v", err)
	}

	mcpServer := server.NewMCPServer("mcpify", "1.0.0", *maxTools)

	endpointCapture := capture.NewEndpointCapture(targetURL, mcpServer)

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
