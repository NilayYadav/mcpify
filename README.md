# mcpify

Automatically generate MCP (Model Context Protocol) servers by observing HTTP API traffic.

## What it does

mcpify watches HTTP requests to your server and automatically creates MCP tools for each discovered endpoint. AI assistants can then call these tools to interact with your APIs.

## Installation

```bash
brew tap nilayyadav/mcpify
brew install mcpify
```

## Quick Start

1. Start your API server (e.g., on localhost:3000)
2. Run mcpify to observe traffic:

```bash
sudo mcpify --target http://localhost:3000
```

3. Make API calls to your server (using your app, curl, Postman, etc.)
4. Each unique endpoint becomes available as an MCP tool at `http://localhost:8081/mcp`

## Configuration

### Environment Variables

```bash
export LLM="openai/gpt-oss-120b:together"
export LLM_ENDPOINT="https://router.huggingface.co/v1"
export LLM_API_KEY="HF_TOKEN"
```

### Command Line Options

```bash
sudo -E mcpify --target http://localhost:3000 \
       --max-tools 100 \
       --use-llm true \
       --verbose
```

| Flag | Description | Default |
|------|-------------|---------|
| `--target` | Target server URL to observe (required) | - |
| `--mcp-port` | MCP server port | `8081` |
| `--max-tools` | Maximum number of tools to capture | `100` |
| `--use-llm` | Enable LLM for tool name generation | `false` |
| `--verbose` | Enable verbose logging | `false` |

## Requirements

- macOS or Linux
- Root/sudo privileges (for packet capture)
- Target server running on HTTP (not HTTPS)

## MCP Integration

Connect AI assistants to `http://localhost:8081/mcp` to access auto-generated tools.

## License

MIT