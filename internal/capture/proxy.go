package capture

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/pcap"
)

type ToolRegistrar interface {
	RegisterTool(name string, method, url string, headers map[string]string, body []byte, description string) error
}

type EndpointCapture struct {
	target        *url.URL
	toolRegistrar ToolRegistrar
	seenAPIs      map[string]*APICall
	mu            sync.RWMutex
}

type APICall struct {
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body,omitempty"`
	FirstSeen   time.Time         `json:"first_seen"`
	LastSeen    time.Time         `json:"last_seen"`
	CallCount   int               `json:"call_count"`
	StatusCodes []int             `json:"status_codes,omitempty"`
}

func NewEndpointCapture(target *url.URL, toolRegistrar ToolRegistrar) *EndpointCapture {
	return &EndpointCapture{
		target:        target,
		toolRegistrar: toolRegistrar,
		seenAPIs:      make(map[string]*APICall),
	}
}

func (ec *EndpointCapture) StartCapture(verbose bool) error {

	iface, err := getLoopbackInterface()
	if err != nil {
		return err
	}

	handle, err := pcap.OpenLive(iface, 65536, true, pcap.BlockForever)
	if err != nil {
		return fmt.Errorf("failed to open interface %s: %w", iface, err)
	}
	defer handle.Close()

	port, _ := strconv.Atoi(ec.target.Port())
	if port == 0 {
		log.Printf("Invalid or missing port in target URL")
	}

	filter := fmt.Sprintf("tcp port %d", port)
	if err := handle.SetBPFFilter(filter); err != nil {
		return fmt.Errorf("failed to set packet filter: %w", err)
	}

	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())

	for packet := range packetSource.Packets() {
		if verbose {
			fmt.Printf("Packet captured\n")
		}
		ec.processPacket(packet, verbose)
	}

	return nil
}

func getLoopbackInterface() (string, error) {
	switch runtime.GOOS {
	case "linux":
		return "lo", nil
	case "darwin", "freebsd", "openbsd":
		return "lo0", nil
	case "windows":
		return "", fmt.Errorf("Windows is not supported at the moment")
	default:
		return "lo0", nil
	}
}

func (ec *EndpointCapture) processPacket(packet gopacket.Packet, verbose bool) {
	if appLayer := packet.ApplicationLayer(); appLayer != nil {
		payload := appLayer.Payload()

		if ec.isHTTPRequest(payload) {
			if verbose {
				log.Printf("HTTP request detected")
			}
			ec.parseHTTPRequest(payload, verbose)
		}
	}
}

func (ec *EndpointCapture) isHTTPRequest(payload []byte) bool {
	payloadStr := string(payload)
	return strings.HasPrefix(payloadStr, "GET ") ||
		strings.HasPrefix(payloadStr, "POST ") ||
		strings.HasPrefix(payloadStr, "PUT ") ||
		strings.HasPrefix(payloadStr, "DELETE ") ||
		strings.HasPrefix(payloadStr, "PATCH ") ||
		strings.HasPrefix(payloadStr, "HEAD ") ||
		strings.HasPrefix(payloadStr, "OPTIONS ")
}

func (ec *EndpointCapture) parseHTTPRequest(payload []byte, verbose bool) {
	// Create a reader from the payload
	reader := bytes.NewReader(payload)
	bufReader := bufio.NewReader(reader)

	// parse http request
	req, err := http.ReadRequest(bufReader)
	if err != nil {
		if verbose {
			log.Printf("Failed to parse HTTP request: %v", err)
		}
		return
	}
	defer req.Body.Close()
	println(req.Host)
	println(req.Method)
	println(req.URL.Path)
	println(req.Proto)

	for key, values := range req.Header {
		println(key, values[0])
	}

	bodybt, _ := io.ReadAll(req.Body)

	println(string(bodybt))

	// Check if this request is for our target host
	if !ec.isTargetRequest(req) {
		if verbose {
			log.Printf("Skipping request for %s (not our target)", req.Host)
		}
		return
	}

	// Read the request body
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		bodyBytes = []byte{}
	}

	if verbose {
		log.Printf("Captured: %s %s", req.Method, req.URL.Path)
		if len(bodyBytes) > 0 {
			log.Printf("Body: %s", ec.truncateString(string(bodyBytes), 100))
		}
	}

	// Convert headers to simple map and filter sensitive ones
	headers := ec.extractHeaders(req.Header)

	ec.recordAPICall(req.Method, req.URL.Path, headers, string(bodyBytes))
}

func (ec *EndpointCapture) isTargetRequest(req *http.Request) bool {
	targetHost := ec.target.Host
	reqHost := req.Host

	if !strings.Contains(targetHost, ":") {
		log.Printf("Target host missing port")
	}

	// Check direct match or localhost variant
	return reqHost == targetHost ||
		reqHost == "localhost:"+ec.target.Port() ||
		reqHost == ec.target.Hostname()+":"+ec.target.Port()
}

func (ec *EndpointCapture) truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (ec *EndpointCapture) recordAPICall(method, path string, headers map[string]string, body string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	key := fmt.Sprintf("%s_%s", method, path)
	now := time.Now()

	if existing, exists := ec.seenAPIs[key]; exists {
		existing.LastSeen = now
		existing.CallCount++
	} else {
		apiCall := &APICall{
			Method:    method,
			Path:      path,
			Headers:   ec.filterSensitiveHeaders(headers),
			Body:      body,
			FirstSeen: now,
			LastSeen:  now,
			CallCount: 1,
		}

		ec.seenAPIs[key] = apiCall

		go ec.registerMCPTool(apiCall)

		log.Printf("New endpoint discovered: %s %s", method, path)
	}
}

func (ec *EndpointCapture) registerMCPTool(apiCall *APICall) {
	toolName := ec.generateToolName(apiCall.Method, apiCall.Path)
	url := ec.target.String() + apiCall.Path
	description := fmt.Sprintf("Auto-discovered: %s %s", apiCall.Method, apiCall.Path)

	err := ec.toolRegistrar.RegisterTool(
		toolName,
		apiCall.Method,
		url,
		apiCall.Headers,
		[]byte(apiCall.Body),
		description,
	)

	if err != nil {
		log.Printf("Failed to register tool %s: %v", toolName, err)
	} else {
		log.Printf("MCP tool registered: %s", toolName)
	}
}

func (ec *EndpointCapture) generateToolName(method, path string) string {
	safePath := strings.ReplaceAll(strings.Trim(path, "/"), "/", "_")
	if safePath == "" {
		safePath = "root"
	}

	if queryPos := strings.Index(safePath, "?"); queryPos > 0 {
		safePath = safePath[:queryPos]
	}

	return fmt.Sprintf("%s_%s", strings.ToLower(method), safePath)
}

func (ec *EndpointCapture) filterSensitiveHeaders(headers map[string]string) map[string]string {
	filtered := make(map[string]string)
	sensitive := []string{"authorization", "cookie", "x-api-key", "x-auth-token"}

	for k, v := range headers {
		isSensitive := false
		for _, s := range sensitive {
			if strings.EqualFold(k, s) {
				isSensitive = true
				break
			}
		}

		if !isSensitive {
			filtered[k] = v
		}
	}

	return filtered
}

func (ec *EndpointCapture) extractHeaders(httpHeaders http.Header) map[string]string {
	headers := make(map[string]string)
	sensitive := []string{"authorization", "cookie", "x-api-key", "x-auth-token"}

	for key, values := range httpHeaders {
		// Skip sensitive headers
		isSensitive := false
		for _, s := range sensitive {
			if strings.EqualFold(key, s) {
				isSensitive = true
				break
			}
		}

		if !isSensitive && len(values) > 0 {
			headers[key] = values[0] // Take first value
		}
	}

	return headers
}
