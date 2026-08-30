# gflow — Product Requirements Document (PRD)

**Status:** Draft V1  
**Project Type:** Open-source embeddable workflow automation engine  
**Primary Backend:** Go  
**Frontend:** SvelteKit SPA + Svelte Flow  
**Default Database:** SQLite  
**Optional Database:** PostgreSQL  
**Distribution Goal:** Single self-contained binary with embedded SPA  
**License Goal:** Apache-2.0  
**Primary Use Case:** Internal tools, AI agents, API workflows, SaaS-embedded workflow builder, white-label automation platform

---

# 1. Executive Summary

`gflow` is an open-source, embeddable workflow automation engine written primarily in Go.

The project is not intended to be a full n8n clone. Its core goal is to provide a lightweight, API-first, white-label workflow engine that can be embedded into any SaaS product through an iframe or SDK while still supporting interoperability with a practical subset of the n8n workflow JSON format.

The initial focus is:

- REST API workflows
- Webhooks
- AI agents
- Basic LLM chains
- AI tool calling
- Basic conversation memory
- PostgreSQL, MySQL, and SQLite workflow nodes
- Branching and data transformation
- Go Code node with isolated execution
- Workflow CRUD through API
- Execution history
- Embeddable workflow canvas
- White-label configuration
- n8n JSON import/export compatibility for supported nodes

The workflow engine itself is implemented by gflow.

The AI agent execution loop is delegated to an external battle-tested agent framework through an internal gflow adapter. The preferred initial framework is **Microsoft Agent Framework for Go**, while keeping gflow's workflow JSON and node contracts independent from it.

---

# 2. Product Vision

The product should allow a SaaS such as MitraChat or another application to create and manage workflows through REST APIs and open the workflow editor inside the host application.

Example:

```text
MitraChat
   |
   | POST /api/v1/workflows
   v
gflow
   |
   | workflow_id
   v
MitraChat UI
   |
   | iframe
   v
gflow /embed/:workflow_id
```

The end-user should not need to know that gflow is a separate product.

The host SaaS may brand the editor as its own internal workflow builder.

---

# 3. Product Principles

## 3.1 API First

Every workflow operation available through the UI must also be possible through the API.

The canvas is a client of the API, not the owner of workflow state.

## 3.2 Embeddable by Default

The editor must support:

- standalone mode
- embedded iframe mode
- JavaScript SDK wrapper later
- host-to-iframe communication with `postMessage`

## 3.3 Go-First Runtime

Core execution must remain in Go.

Native nodes should be implemented in Go whenever practical.

## 3.4 Single-Binary Distribution

The production build should contain:

- Go API
- workflow engine
- scheduler
- webhook server
- embedded SvelteKit SPA
- internal SQLite support
- AI adapter
- WASM runtime

A default deployment should be possible with:

```bash
./gflow
```

## 3.5 External AI Framework Behind an Adapter

gflow should not rebuild agent orchestration unless necessary.

The AI framework must never become the public workflow contract.

```text
gflow Agent Node
      |
      v
gflow AgentRuntime interface
      |
      v
Microsoft Agent Framework adapter
      |
      v
Provider
```

## 3.6 Interoperability Without Runtime Dependency on n8n

gflow may import/export supported n8n workflow JSON structures.

The core project must not depend on:

- n8n-core
- n8n-workflow
- n8n-editor-ui
- n8n-nodes-base

The workflow engine and node runtime must remain independent implementations.

---

# 4. Goals

## 4.1 V1 Goals

V1 must support:

- workflow CRUD
- workflow versioning
- workflow execution
- execution inspection
- workflow activation/deactivation
- webhook trigger
- manual trigger
- HTTP Request node
- Set/Edit Fields node
- IF node
- Merge node
- Respond to Webhook node
- PostgreSQL node
- MySQL node
- SQLite node
- AI Chat Model node
- AI Agent node
- Basic Memory node
- AI Tool connections
- Go Code node
- credentials
- basic expressions
- scheduler foundation
- Svelte Flow canvas
- embedded iframe editor
- white-label config
- SQLite default storage
- PostgreSQL internal storage option
- Svelte SPA embedded into Go binary
- n8n JSON import/export for supported nodes

## 4.2 Non-Goals for V1

V1 does not need to provide:

- complete n8n compatibility
- arbitrary n8n npm community nodes
- full LangChain compatibility
- full JavaScript Code Node
- distributed workflow execution
- Kubernetes-native worker scaling
- full OAuth provider catalog
- binary file processing pipeline
- vector-memory system
- advanced RAG
- multi-agent canvas
- marketplace
- enterprise RBAC
- GitOps workflow sync
- human approval orchestration beyond basic extension points

---

# 5. High-Level Architecture

```text
                         +--------------------------+
                         |      Host SaaS App       |
                         | MitraChat / CRM / etc.   |
                         +------------+-------------+
                                      |
                             REST API | iframe
                                      v
+-------------------------------------------------------------+
|                         gflow                               |
|                                                             |
|  +-------------------- Go API ----------------------------+  |
|  | Workflow CRUD       Credential Manager                |  |
|  | Execution API       Embed Sessions                    |  |
|  | Webhook API         Scheduler                         |  |
|  +---------------------------+----------------------------+  |
|                              |                               |
|  +---------------------------v----------------------------+  |
|  |                  Workflow Engine                      |  |
|  |                                                       |  |
|  | Graph execution      Branching        Retry           |  |
|  | Expressions          Context          Node I/O        |  |
|  +-----------+--------------------------+----------------+  |
|              |                          |                   |
|              v                          v                   |
|       Native Go Nodes             AI Adapter                |
|       HTTP / SQL / IF                 |                     |
|       Set / Webhook                   v                     |
|       Code / Merge         Microsoft Agent Framework        |
|                                  Provider / Tools            |
|                                                             |
|  +-------------------------------------------------------+  |
|  | Persistence                                           |  |
|  | GORM -> SQLite default / PostgreSQL optional          |  |
|  +-------------------------------------------------------+  |
|                                                             |
|  +-------------------------------------------------------+  |
|  | SvelteKit SPA                                         |  |
|  | Svelte Flow Canvas                                    |  |
|  | Embedded into Go binary via go:embed                  |  |
|  +-------------------------------------------------------+  |
+-------------------------------------------------------------+
```

---

# 6. Repository Structure

Recommended monorepo:

```text
gflow/
|
|-- cmd/
|   `-- gflow/
|       `-- main.go
|
|-- internal/
|   |-- api/
|   |   |-- handlers/
|   |   |-- middleware/
|   |   `-- routes.go
|   |
|   |-- auth/
|   |-- config/
|   |-- credentials/
|   |-- database/
|   |-- embed/
|   |-- engine/
|   |-- execution/
|   |-- expression/
|   |-- node/
|   |-- repository/
|   |-- scheduler/
|   |-- webhook/
|   |-- workflow/
|   |
|   |-- ai/
|   |   |-- runtime.go
|   |   |-- types.go
|   |   |-- memory.go
|   |   `-- maf/
|   |       `-- runtime.go
|   |
|   |-- runcode/
|   |   |-- compiler/
|   |   |-- runtime/
|   |   `-- wasm/
|   |
|   `-- web/
|       `-- embed.go
|
|-- nodes/
|   |-- core/
|   |   |-- manual/
|   |   |-- set/
|   |   |-- ifnode/
|   |   |-- merge/
|   |   `-- code/
|   |
|   |-- http/
|   |-- webhook/
|   |
|   |-- database/
|   |   |-- postgres/
|   |   |-- mysql/
|   |   `-- sqlite/
|   |
|   `-- ai/
|       |-- chatmodel/
|       |-- agent/
|       |-- memory/
|       `-- tool/
|
|-- pkg/
|   `-- sdk/
|
|-- schemas/
|   |-- workflow.schema.json
|   `-- node.schema.json
|
|-- web/
|   |-- src/
|   |-- static/
|   |-- package.json
|   |-- vite.config.ts
|   |-- svelte.config.js
|   `-- tsconfig.json
|
|-- migrations/
|
|-- scripts/
|
|-- .air.toml
|-- devbox.json
|-- devbox.lock
|-- Makefile
|-- go.mod
|-- go.sum
|-- Dockerfile
|-- docker-compose.yml
|-- LICENSE
`-- README.md
```

---

# 7. Development Environment

gflow should use **Devbox** for reproducible local development.

The developer should not need to manually install:

- Go
- Node.js
- pnpm
- Make
- SQLite
- Air

Recommended development commands:

```bash
devbox shell
make dev
```

---

# 8. Devbox Configuration

Example `devbox.json`:

```json
{
  "$schema": "https://raw.githubusercontent.com/jetify-com/devbox/0.16.0/.schema/devbox.schema.json",
  "packages": [
    "go@latest",
    "nodejs@22",
    "pnpm@latest",
    "gnumake@latest",
    "sqlite@latest",
    "git@latest"
  ],
  "shell": {
    "init_hook": [
      "export CGO_ENABLED=0",
      "export GFLOW_ENV=development"
    ],
    "scripts": {
      "install": [
        "go mod download",
        "cd web && pnpm install"
      ],
      "dev": [
        "make dev"
      ],
      "test": [
        "make test"
      ]
    }
  }
}
```

Air can be installed as a Go tool instead of a Nix package:

```bash
go install github.com/air-verse/air@latest
```

Recommended bootstrap target in the Makefile should ensure Air is installed.

---

# 9. Makefile

Recommended initial `Makefile`:

```makefile
APP_NAME := gflow
GO_CMD := go
WEB_DIR := web
DIST_DIR := internal/web/dist

.PHONY: help
help:
	@echo "gflow development commands"
	@echo ""
	@echo "make setup       Install project dependencies"
	@echo "make dev         Run backend and frontend development servers"
	@echo "make dev-api     Run Go backend with Air"
	@echo "make dev-web     Run SvelteKit development server"
	@echo "make build       Build production binary"
	@echo "make build-web   Build SvelteKit SPA"
	@echo "make test        Run Go tests"
	@echo "make lint        Run linters"
	@echo "make clean       Remove build artifacts"

.PHONY: setup
setup:
	$(GO_CMD) mod download
	$(GO_CMD) install github.com/air-verse/air@latest
	cd $(WEB_DIR) && pnpm install

.PHONY: dev
dev:
	@trap 'kill 0' INT TERM EXIT; \
	$(MAKE) dev-api & \
	$(MAKE) dev-web & \
	wait

.PHONY: dev-api
dev-api:
	air

.PHONY: dev-web
dev-web:
	cd $(WEB_DIR) && pnpm dev

.PHONY: build-web
build-web:
	cd $(WEB_DIR) && pnpm build
	rm -rf $(DIST_DIR)
	mkdir -p $(DIST_DIR)
	cp -R $(WEB_DIR)/build/. $(DIST_DIR)/

.PHONY: build
build: build-web
	CGO_ENABLED=0 $(GO_CMD) build \
		-trimpath \
		-ldflags="-s -w" \
		-o bin/$(APP_NAME) \
		./cmd/gflow

.PHONY: test
test:
	$(GO_CMD) test ./...

.PHONY: lint
lint:
	$(GO_CMD) vet ./...
	cd $(WEB_DIR) && pnpm check

.PHONY: clean
clean:
	rm -rf bin
	rm -rf $(WEB_DIR)/build
	rm -rf $(DIST_DIR)
	rm -rf .tmp
```

---

# 10. Air Hot Reload

Recommended `.air.toml`:

```toml
root = "."
tmp_dir = ".tmp"

[build]
cmd = "go build -o ./.tmp/gflow ./cmd/gflow"
bin = "./.tmp/gflow"
include_ext = ["go", "json", "yaml", "yml"]
exclude_dir = [
  ".git",
  ".tmp",
  "web",
  "node_modules"
]
delay = 300
stop_on_error = true
send_interrupt = true
kill_delay = 500

[log]
time = true

[color]
main = "magenta"
watcher = "cyan"
build = "yellow"
runner = "green"
```

During development:

```text
Air
 |
 v
Go API :8080

SvelteKit
 |
 v
Vite :5173
```

The SvelteKit frontend proxies backend traffic to Go.

---

# 11. Development Networking

## 11.1 Required Behavior

Production uses one host:

```text
https://flow.example.com/
https://flow.example.com/api/v1/*
https://flow.example.com/webhook/*
https://flow.example.com/embed/*
```

Development should behave as closely as possible to production.

The browser should only communicate with the SvelteKit dev server during frontend development:

```text
Browser
   |
   v
http://localhost:5173
   |
   +--> /api/* --------+
   |                   |
   +--> /webhook/*     |
                       v
                 Go :8080
```

This means the frontend source can always use relative URLs:

```ts
fetch('/api/v1/workflows')
```

Do not hardcode:

```ts
fetch('http://localhost:8080/api/v1/workflows')
```

---

# 12. SvelteKit / Vite Dev Proxy

Recommended `web/vite.config.ts`:

```ts
import { defineConfig } from 'vite';
import { sveltekit } from '@sveltejs/kit/vite';

const backend = process.env.GFLOW_BACKEND_URL ?? 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [sveltekit()],

  server: {
    port: 5173,

    proxy: {
      '/api': {
        target: backend,
        changeOrigin: true
      },

      '/webhook': {
        target: backend,
        changeOrigin: true
      },

      '/embed-session': {
        target: backend,
        changeOrigin: true
      }
    }
  }
});
```

This ensures the frontend API binding matches the production single-host model.

---

# 13. SvelteKit SPA Configuration

The editor should be a full SPA.

Recommended `web/src/routes/+layout.ts`:

```ts
export const ssr = false;
export const prerender = false;
```

Recommended `web/svelte.config.js`:

```js
import adapter from '@sveltejs/adapter-static';

const config = {
  kit: {
    adapter: adapter({
      fallback: 'index.html'
    })
  }
};

export default config;
```

The SPA build output is embedded into the Go binary.

---

# 14. Go Embedded Frontend

Production flow:

```text
pnpm build
   |
   v
web/build
   |
   v
internal/web/dist
   |
   v
go:embed
   |
   v
gflow binary
```

Example:

```go
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/*
var assets embed.FS

func StaticFS() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}

func Handler() http.Handler {
	sub, err := StaticFS()
	if err != nil {
		panic(err)
	}

	return http.FileServer(http.FS(sub))
}
```

The production router must support SPA fallback.

Pseudo behavior:

```text
/api/*       -> Go API
/webhook/*   -> Go workflow webhook
/health      -> Go health check
/*           -> embedded static assets
unknown SPA route -> index.html
```

---

# 15. Backend HTTP Server

Recommended default:

```text
GFLOW_HTTP_HOST=0.0.0.0
GFLOW_HTTP_PORT=8080
```

Example config:

```yaml
server:
  host: 0.0.0.0
  port: 8080
```

The Go server is the only server used in production.

---

# 16. Persistence

## 16.1 ORM

Use GORM for gflow internal persistence.

```text
gflow Repository
       |
       v
      GORM
     /    \
SQLite   PostgreSQL
```

## 16.2 Default

SQLite:

```text
./data/gflow.db
```

Recommended SQLite defaults:

```text
WAL mode
foreign_keys = ON
busy_timeout
```

## 16.3 PostgreSQL

Example:

```yaml
database:
  driver: postgres
  dsn: postgres://gflow:password@postgres:5432/gflow
```

## 16.4 Config

Example:

```yaml
database:
  driver: sqlite
  dsn: ./data/gflow.db
```

Environment override:

```text
GFLOW_DATABASE_DRIVER=postgres
GFLOW_DATABASE_DSN=postgres://...
```

---

# 17. Repository Layer

The workflow engine should not directly depend on GORM.

Example:

```go
type WorkflowRepository interface {
	Create(ctx context.Context, workflow *Workflow) error
	Get(ctx context.Context, id string) (*Workflow, error)
	List(ctx context.Context, query WorkflowQuery) ([]Workflow, error)
	Update(ctx context.Context, workflow *Workflow) error
	Delete(ctx context.Context, id string) error
}
```

Implementation:

```text
GORMWorkflowRepository
```

This allows persistence implementation changes later without coupling the workflow engine.

---

# 18. Core Database Tables

Initial internal entities:

```text
workflows
workflow_versions

executions
execution_node_runs

credentials

webhooks
schedules

ai_sessions
ai_messages

embed_sessions

code_artifacts
```

Suggested workflow model:

```go
type Workflow struct {
	ID         string
	Name       string
	Active     bool
	Definition []byte
	Version    int

	CreatedAt time.Time
	UpdatedAt time.Time
}
```

Workflow JSON should remain the primary persisted workflow definition.

---

# 19. Workflow JSON

gflow owns its canonical workflow format.

Example:

```json
{
  "id": "wf_123",
  "name": "Customer Support Agent",
  "version": 1,
  "nodes": [
    {
      "id": "node_1",
      "name": "Webhook",
      "type": "gflow.webhook",
      "typeVersion": 1,
      "position": [120, 200],
      "parameters": {}
    }
  ],
  "connections": {},
  "settings": {}
}
```

The format should intentionally resemble common workflow JSON structures so that n8n adapters are easier to implement.

---

# 20. Internal Workflow IR

Do not execute imported n8n JSON directly.

Pipeline:

```text
n8n JSON
    |
    v
n8n Import Adapter
    |
    v
gflow Workflow JSON
    |
    v
Compiler / Validator
    |
    v
Runtime IR
    |
    v
Execution Engine
```

Suggested runtime types:

```go
type Workflow struct {
	ID       string
	Name     string
	Nodes    []Node
	Edges    []Edge
	Settings WorkflowSettings
}

type Node struct {
	ID          string
	Name        string
	Type        string
	Version     int
	Parameters  map[string]any
	Credentials map[string]string
	Position    Position
}

type Edge struct {
	From      string
	FromPort  string
	FromIndex int

	To      string
	ToPort  string
	ToIndex int
}
```

---

# 21. Connection Types

Initial connection types:

```go
type ConnectionType string

const (
	ConnectionMain          ConnectionType = "main"
	ConnectionLanguageModel ConnectionType = "ai_languageModel"
	ConnectionMemory        ConnectionType = "ai_memory"
	ConnectionTool          ConnectionType = "ai_tool"
)
```

This allows AI graphs such as:

```text
                   Chat Model
                       |
                       v
Input -----------> AI Agent
                    ^   ^
                    |   |
                Memory  Tool
```

---

# 22. Workflow Data Model

Node execution data should use item semantics.

```go
type Item struct {
	JSON   map[string]any
	Binary map[string]BinaryData
}
```

Node execution should allow multiple outputs:

```go
type NodeOutput [][]Item
```

Example IF node:

```text
output[0] = true items
output[1] = false items
```

This makes n8n-style import compatibility easier.

---

# 23. Node Registry

Nodes must be registered through metadata.

Example:

```go
type NodeDefinition struct {
	Type        string
	DisplayName string
	Version     int
	Category    string

	Inputs  []PortDefinition
	Outputs []PortDefinition

	Properties []PropertyDefinition

	Executor NodeExecutor
}
```

The frontend should fetch node definitions from:

```http
GET /api/v1/node-types
```

The Svelte UI should render property forms dynamically from node metadata.

Avoid hardcoding node-specific forms wherever possible.

---

# 24. Native V1 Nodes

## Trigger

- Manual Trigger
- Webhook Trigger
- Schedule Trigger

## Core

- HTTP Request
- Set / Edit Fields
- IF
- Merge
- Respond to Webhook
- Go Code

## Database

- PostgreSQL
- MySQL
- SQLite

## AI

- OpenAI-Compatible Chat Model
- AI Agent
- Basic Memory
- Tool Adapter

---

# 25. AI Architecture

gflow should use an agent framework for agent orchestration.

Initial preferred runtime:

```text
Microsoft Agent Framework for Go
```

However, the dependency must be isolated.

Public gflow code should depend only on:

```go
type AgentRuntime interface {
	Run(
		ctx context.Context,
		request AgentRequest,
	) (<-chan AgentEvent, error)
}
```

Example request:

```go
type AgentRequest struct {
	Model    ModelConfig
	Messages []Message
	Tools    []Tool
	Memory   Memory

	SystemPrompt string
	MaxSteps     int
}
```

Implementation:

```text
internal/ai/maf/runtime.go
```

The workflow engine must never directly import Microsoft Agent Framework packages.

---

# 26. AI Scope Boundary

gflow owns:

- graph execution
- node wiring
- tool node mapping
- workflow persistence
- memory persistence
- credentials
- expressions
- session key resolution
- node execution history

AI framework owns:

- model invocation
- tool-calling loop
- provider message translation
- agent iteration
- streaming
- tool call sequencing
- agent middleware
- framework-level tracing

The AI framework must not become the gflow workflow engine.

---

# 27. AI Chat Model V1

Initial model node:

```text
OpenAI Compatible Chat Model
```

Configuration:

```text
Base URL
API Key
Model
Temperature
Max Tokens
Timeout
```

This can initially support providers exposing OpenAI-compatible APIs.

Provider-native nodes can be added later.

---

# 28. Basic Memory

Basic Memory should store conversation history.

Canvas:

```text
              Chat Model
                  |
                  v
Input --------> Agent
                  ^
                  |
             Basic Memory
```

Node configuration:

```text
Session Key
Context Window
```

Example:

```text
Session Key:
{{ $json.session_id }}

Context Window:
10
```

Storage belongs to gflow.

Suggested entities:

```go
type AISession struct {
	ID         string
	WorkflowID string
	TenantID   string
	SessionKey string

	CreatedAt time.Time
	UpdatedAt time.Time
}

type AIMessage struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	CreatedAt time.Time
}
```

Persistence:

```text
GORM
 |
 +-- SQLite
 `-- PostgreSQL
```

The AI runtime reads memory through an adapter.

---

# 29. Tool Architecture

A normal gflow executable should be reusable as an AI tool where practical.

Example:

```go
type Callable interface {
	Call(
		ctx context.Context,
		input map[string]any,
	) (any, error)
}
```

HTTP Request can therefore be used as:

```text
HTTP Request Node
```

or:

```text
AI HTTP Tool
```

without duplicating the actual HTTP execution implementation.

---

# 30. Go Code Node

V1 Code Node language:

```text
Go
```

User-facing code:

```go
func Run(ctx Context) (any, error) {
	input := ctx.Input()

	return map[string]any{
		"hello": input["name"],
	}, nil
}
```

Users should not need to declare:

```go
package main
```

gflow creates the wrapper.

---

# 31. Go Code Security

User code must not execute directly inside the main gflow process.

Preferred production execution path:

```text
Go Source
   |
   v
Wrapper Generator
   |
   v
GOOS=wasip1 GOARCH=wasm
   |
   v
WASM Artifact
   |
   v
wazero Runtime
```

Compilation must happen when code changes, not on every execution.

```text
source code
   |
   v
hash
   |
   +-- existing -> reuse artifact
   |
   `-- new -> compile -> cache
```

---

# 32. Go Code V1 Restrictions

Do not allow arbitrary dependencies in V1.

Provide a small SDK:

```text
gflow/input
gflow/output
gflow/http
gflow/json
gflow/log
```

Avoid exposing uncontrolled:

```text
os
os/exec
syscall
host filesystem
arbitrary host networking
```

A future compiler service may run separately from the main process.

---

# 33. Expressions

V1 should support a safe expression subset.

Examples:

```text
{{ $json.name }}

{{ $json.user.email }}

{{ $input.email }}

{{ $node["Get User"].json.id }}

{{ $env.API_URL }}

{{ $execution.id }}
```

Do not target arbitrary JavaScript expressions in V1.

Expression parsing should remain independent from n8n.

---

# 34. Credentials

Workflow JSON must never contain plaintext secrets.

Workflow:

```json
{
  "credentials": {
    "postgres": "cred_abc123"
  }
}
```

Credential storage:

```text
credentials
 |
 +-- id
 +-- tenant_id
 +-- type
 +-- encrypted_payload
 +-- created_at
 `-- updated_at
```

Recommended encryption:

```text
AES-256-GCM
```

Master key must come from environment or external secret manager.

Example:

```text
GFLOW_ENCRYPTION_KEY=...
```

---

# 35. Workflow Execution Model

Execution should be queue/graph based.

Example:

```text
A
|\
| \
B  C
 \ /
  D
```

Runtime behavior:

```text
execute A

queue B
queue C

B complete
C complete

D becomes ready

execute D
```

Execution record:

```go
type Execution struct {
	ID         string
	WorkflowID string
	Status     ExecutionStatus

	StartedAt  time.Time
	FinishedAt *time.Time
}
```

Node execution:

```go
type NodeRun struct {
	ID          string
	ExecutionID string
	NodeID      string

	Input  NodeInput
	Output NodeOutput

	Status ExecutionStatus

	StartedAt  time.Time
	FinishedAt *time.Time
	Error      *ExecutionError
}
```

---

# 36. API

Base:

```text
/api/v1
```

## Workflow

```http
POST   /api/v1/workflows
GET    /api/v1/workflows
GET    /api/v1/workflows/:id
PUT    /api/v1/workflows/:id
DELETE /api/v1/workflows/:id
```

## Lifecycle

```http
POST /api/v1/workflows/:id/activate
POST /api/v1/workflows/:id/deactivate
POST /api/v1/workflows/:id/run
```

## Execution

```http
GET /api/v1/executions
GET /api/v1/executions/:id
POST /api/v1/executions/:id/cancel
```

## Nodes

```http
GET /api/v1/node-types
```

## Credentials

```http
POST   /api/v1/credentials
GET    /api/v1/credentials
PUT    /api/v1/credentials/:id
DELETE /api/v1/credentials/:id
```

## Embed

```http
POST /api/v1/embed-sessions
```

---

# 37. Webhook Routing

Example:

```text
POST /webhook/:webhookID
```

The webhook maps to:

```text
workflow
trigger node
activation state
```

Example execution:

```text
HTTP request
   |
   v
Webhook Trigger
   |
   v
workflow engine
   |
   v
Respond To Webhook
```

---

# 38. Embeddable Editor

Standalone editor:

```text
/app/workflows/:workflowID
```

Embedded editor:

```text
/embed/:workflowID
```

Embedded mode hides:

- global navigation
- instance admin
- workflow list
- unnecessary account settings

Embedded mode shows:

- canvas
- node picker
- node properties
- save
- execute
- execution inspector

---

# 39. Embed Authentication

Do not depend entirely on third-party cookies.

Recommended flow:

```text
Host backend
   |
   | POST /api/v1/embed-sessions
   v
gflow
   |
   | short-lived token
   v
Host frontend
   |
   v
iframe
```

Preferred token transport:

```text
iframe loads
    |
    v
FLOW_READY
    |
    v
parent postMessage()
    |
    v
FLOW_AUTH
```

The iframe must validate:

```text
event.origin
```

Embed session claims:

```json
{
  "workflow_id": "wf_123",
  "tenant_id": "tenant_456",
  "permissions": [
    "workflow:read",
    "workflow:update",
    "workflow:execute"
  ],
  "allowed_origin": "https://app.example.com",
  "exp": 1787980000
}
```

---

# 40. Host SDK

Future JavaScript SDK:

```ts
const gflow = new GflowClient({
  baseURL: 'https://flow.example.com',
  apiKey: process.env.GFLOW_API_KEY
});

const workflow = await gflow.workflows.create({
  name: 'Support Agent'
});
```

Editor wrapper:

```ts
const editor = new GflowEditor({
  container,
  workflowId,
  getEmbedToken
});
```

SDK should wrap:

- iframe creation
- embed authentication
- postMessage
- save events
- execution events
- dirty state
- close navigation

---

# 41. White Label

Branding must be configuration-driven.

Example:

```yaml
branding:
  name: My Workflow
  logo: https://example.com/logo.svg
  favicon: https://example.com/favicon.ico
  powered_by: false

theme:
  default: system
```

Embed sessions may override selected display options.

No core UI code should hardcode the project name where avoidable.

---

# 42. n8n Compatibility

Compatibility levels:

## Level 1 — Workflow Shape

Support:

- node ID
- node name
- node type
- node version
- node position
- parameters
- connections
- settings

## Level 2 — Supported Node Mapping

Examples:

```text
n8n-nodes-base.httpRequest
        |
        v
gflow.http
```

```text
n8n-nodes-base.postgres
        |
        v
gflow.postgres
```

## Level 3 — Execution Semantics

Support only semantics required by mapped nodes.

Do not promise arbitrary n8n workflow execution.

Unsupported imported nodes should remain visible and marked:

```text
Unsupported
```

rather than silently discarded.

---

# 43. Frontend Architecture

Recommended stack:

```text
SvelteKit
Svelte 5
Svelte Flow
TypeScript
Tailwind CSS
```

State groups:

```text
workflow state
canvas state
selection state
execution state
embed session state
node registry state
```

The frontend must never execute workflows itself.

---

# 44. Frontend Routes

```text
/
  landing or redirect

/app/workflows
/app/workflows/:id

/embed/:id

/settings
/settings/credentials

/executions
/executions/:id
```

For a dedicated white-label SaaS embed, `/embed/:id` is the primary integration route.

---

# 45. Configuration

Example `config.yaml`:

```yaml
server:
  host: 0.0.0.0
  port: 8080

database:
  driver: sqlite
  dsn: ./data/gflow.db

security:
  encryption_key_env: GFLOW_ENCRYPTION_KEY

branding:
  name: gflow
  powered_by: true

execution:
  max_concurrent: 10
  default_timeout: 60s

code:
  enabled: true
  compiler: local

ai:
  runtime: maf
```

Environment variables override file configuration.

---

# 46. Local Development Flow

First setup:

```bash
devbox shell
make setup
```

Development:

```bash
make dev
```

Starts:

```text
Go + Air
http://127.0.0.1:8080

SvelteKit + Vite
http://127.0.0.1:5173
```

Browser:

```text
http://localhost:5173
```

API calls from browser:

```text
/api/v1/*
```

Vite forwards those requests to:

```text
http://127.0.0.1:8080
```

Production uses the same relative URLs, but Go serves both API and SPA.

---

# 47. Production Build

Command:

```bash
make build
```

Pipeline:

```text
SvelteKit build
      |
      v
web/build
      |
      v
internal/web/dist
      |
      v
go:embed
      |
      v
Go build
      |
      v
bin/gflow
```

Deployment:

```bash
GFLOW_ENCRYPTION_KEY=... ./gflow
```

---

# 48. Docker

Recommended multi-stage build:

```dockerfile
FROM node:22-alpine AS web

WORKDIR /src/web

COPY web/package.json web/pnpm-lock.yaml ./

RUN corepack enable \
    && pnpm install --frozen-lockfile

COPY web/ ./

RUN pnpm build


FROM golang:1.25-alpine AS go

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

COPY --from=web /src/web/build ./internal/web/dist

RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/gflow \
    ./cmd/gflow


FROM gcr.io/distroless/static-debian12

WORKDIR /app

COPY --from=go /out/gflow /app/gflow

VOLUME ["/app/data"]

EXPOSE 8080

ENTRYPOINT ["/app/gflow"]
```

Actual Go/Node versions should be pinned before the first release rather than using floating versions.

---

# 49. Health Endpoints

```http
GET /health
GET /ready
```

`/health`:

```json
{
  "status": "ok"
}
```

`/ready` verifies:

- database reachable
- migrations complete
- node registry initialized

---

# 50. Observability

V1:

- structured logging
- request ID
- execution ID
- node run logs
- duration
- token usage where available

Recommended Go logging:

```text
log/slog
```

Future:

```text
OpenTelemetry
Prometheus
distributed tracing
```

---

# 51. Execution Events

Internally, standardize execution events.

Example:

```go
type ExecutionEvent interface {
	EventName() string
}
```

Events:

```text
execution.started
execution.completed
execution.failed

node.started
node.output
node.failed

agent.token
agent.tool.started
agent.tool.completed

workflow.saved
```

These events can later power:

- WebSocket
- SSE
- embed SDK
- observability
- audit logs

---

# 52. Realtime UI

Initial recommendation:

```text
SSE
```

Use cases:

- live execution log
- agent streaming
- node completion
- execution state

Endpoint:

```http
GET /api/v1/executions/:id/events
```

WebSocket can be introduced later if bidirectional realtime control is needed.

---

# 53. Scheduler

V1 scheduler requirements:

- cron schedule
- active/inactive
- workflow ID
- timezone
- last run
- next run

Scheduler must live in Go.

For SQLite single-instance deployment, one scheduler process is sufficient.

Distributed scheduling is a future capability.

---

# 54. Migration Strategy

Use versioned migrations.

Example:

```text
migrations/
  000001_initial.up.sql
  000001_initial.down.sql
```

Migrations should work on both:

```text
SQLite
PostgreSQL
```

Avoid database-specific schema features in V1 where practical.

---

# 55. ID Strategy

Use string IDs.

Recommended:

```text
UUIDv7
```

Examples:

```text
wf_019...
node_019...
exec_019...
cred_019...
session_019...
```

Prefixing IDs improves debugging while retaining globally unique values.

---

# 56. Multi-Tenancy

Even if V1 initially runs internally, models should be tenant-aware.

Recommended common column:

```text
tenant_id
```

Applies to:

- workflows
- credentials
- executions
- sessions
- messages
- embed sessions

Standalone installs can use:

```text
tenant_id = default
```

This prevents future painful migrations when embedding gflow into SaaS products.

---

# 57. Security Requirements

V1 security requirements:

- credential encryption
- tenant isolation
- workflow ownership validation
- embed origin validation
- short-lived embed sessions
- API rate limit extension point
- Code Node isolation
- SQL credential isolation
- no workflow access to internal gflow DB by default
- safe expression engine
- configurable webhook payload limit
- request timeout
- execution timeout
- HTTP node SSRF protection option

---

# 58. Internal DB vs SQLite Workflow Node

gflow internal SQLite:

```text
./data/gflow.db
```

must not automatically be exposed to workflows.

SQLite workflow nodes should connect to explicit user configured database files.

Example:

```text
Credential:
SQLite Database

Path:
/data/customer.db
```

Never default:

```text
/data/gflow.db
```

---

# 59. V1 Milestones

## Milestone 0 — Foundation

- Go project
- Devbox
- Makefile
- Air
- configuration
- GORM
- SQLite
- PostgreSQL adapter
- migrations
- SvelteKit SPA
- Svelte Flow
- Vite proxy
- Go embedded frontend

## Milestone 1 — Workflow Core

- workflow models
- node registry
- workflow CRUD
- execution model
- graph runner
- Manual Trigger
- Set
- IF
- Merge

## Milestone 2 — API Automation

- HTTP Request
- Webhook Trigger
- Respond to Webhook
- credentials
- expressions
- execution inspector

## Milestone 3 — Database Nodes

- PostgreSQL
- MySQL
- SQLite
- SQL credentials

## Milestone 4 — AI

- Microsoft Agent Framework adapter
- OpenAI-compatible Chat Model
- AI Agent
- Basic Memory
- Tool Adapter
- agent streaming

## Milestone 5 — Go Code

- Code Node editor
- source hashing
- WASM compiler
- wazero execution
- artifact cache

## Milestone 6 — Embed

- `/embed/:workflowID`
- embed sessions
- postMessage protocol
- white-label options
- host origin validation

## Milestone 7 — Interop

- n8n JSON importer
- n8n JSON exporter
- supported node mapping
- unsupported node handling

---

# 60. Definition of Done for V1

V1 is complete when an external SaaS can:

1. deploy one gflow instance
2. use SQLite without external infrastructure
3. optionally switch internal persistence to PostgreSQL
4. create a workflow through REST API
5. open the workflow in an iframe
6. visually edit it
7. save it
8. execute it
9. inspect node execution results
10. receive a webhook
11. call external REST APIs
12. query PostgreSQL/MySQL/SQLite
13. run an AI Agent
14. attach Basic Memory
15. expose a workflow node as an AI tool
16. execute isolated Go Code
17. import/export supported n8n workflow JSON
18. white-label the editor
19. ship the UI and backend as one production binary

---

# 61. Initial Technical Dependencies

The exact versions should be pinned in `go.mod`, `package.json`, and `devbox.lock`.

Suggested categories:

## Go

```text
GORM
GORM SQLite driver
GORM PostgreSQL driver

HTTP router
Microsoft Agent Framework Go
wazero

database/sql drivers:
- PostgreSQL
- MySQL
- SQLite where required

UUIDv7 implementation
migration library
```

Prefer standard library where practical:

```text
net/http
log/slog
context
embed
database/sql
crypto
```

## Frontend

```text
SvelteKit
Svelte 5
@xyflow/svelte
Tailwind CSS
TypeScript
adapter-static
```

## Development

```text
Devbox
Go
Node.js
pnpm
Make
Air
SQLite CLI
```

---

# 62. Suggested Go Package Boundaries

```text
cmd/gflow
    bootstrap only

internal/api
    HTTP transport

internal/engine
    workflow execution

internal/workflow
    workflow domain

internal/node
    node contract + registry

internal/execution
    execution domain

internal/repository
    persistence interfaces

internal/database
    GORM initialization

internal/ai
    gflow AI contracts

internal/ai/maf
    Microsoft Agent Framework adapter

internal/runcode
    Go Code compilation/runtime

internal/embed
    iframe session security

internal/web
    SPA embedding
```

Rule:

```text
engine should not import api
engine should not import GORM
engine should not import Svelte concerns
engine should not directly import Microsoft Agent Framework
```

---

# 63. Recommended Bootstrap Flow

```go
func main() {
	cfg := config.Load()

	db := database.Open(cfg.Database)

	repositories := repository.New(db)

	nodeRegistry := node.NewRegistry()

	nodes.RegisterAll(nodeRegistry)

	agentRuntime := maf.NewRuntime(...)

	workflowEngine := engine.New(
		repositories,
		nodeRegistry,
		agentRuntime,
	)

	server := api.NewServer(
		cfg,
		repositories,
		workflowEngine,
		nodeRegistry,
	)

	server.Run()
}
```

The real implementation should use dependency injection through constructors, not a global service locator.

---

# 64. Open Questions After V1

Items intentionally deferred:

- whether distributed workers use NATS, Redis Streams, or PostgreSQL
- whether PostgreSQL becomes recommended default for HA
- plugin SDK design
- arbitrary third-party Go nodes
- n8n npm compatibility runner
- vector store abstraction
- RAG nodes
- MCP Client node surfaced directly on canvas
- multi-agent node
- human approval node
- secrets manager integrations
- marketplace
- cloud-hosted gflow
- enterprise SSO
- RBAC
- Git-based workflow versioning

---

# 65. Recommended First Implementation Order

Do not start with AI.

Start in this order:

```text
1. repository + Devbox + Makefile + Air
2. Go HTTP server
3. SvelteKit SPA + proxy + embedded build
4. SQLite/GORM
5. workflow CRUD
6. node registry
7. graph execution
8. Set / IF / Manual Trigger
9. HTTP / Webhook
10. execution inspector
11. SQL nodes
12. AI adapter + Agent
13. Basic Memory
14. Tool integration
15. Go Code/WASM
16. embed session system
17. n8n importer/exporter
```

The first useful vertical slice should be:

```text
Manual Trigger
      |
      v
Set
      |
      v
HTTP Request
```

editable in Svelte Flow and executable by the Go runtime.

The second vertical slice should be:

```text
Webhook
   |
   v
HTTP
   |
   v
Respond To Webhook
```

The third vertical slice should be:

```text
Chat Input
    |
    v
AI Agent
  ^     ^
  |     |
Memory  HTTP Tool
```

---

# 66. Final V1 Architecture

```text
                       Host SaaS
                          |
                   REST / iframe
                          |
                          v
+----------------------------------------------------+
|                       gflow                        |
|                                                    |
|               Go HTTP Server                       |
|              /       |       \                     |
|             /        |        \                    |
|            v         v         v                   |
|       REST API    Webhooks    Embedded SPA         |
|            |         |            |                |
|            +---------+------------+                |
|                      |                             |
|                      v                             |
|              Workflow Engine                      |
|                      |                             |
|          +-----------+-----------+                 |
|          |                       |                 |
|          v                       v                 |
|     Native Go Nodes        AI Runtime Adapter      |
|                                  |                 |
|                                  v                 |
|                   Microsoft Agent Framework        |
|                                                    |
|          Go Code -> WASM -> wazero                 |
|                                                    |
|            Repository Interfaces                   |
|                      |                             |
|                     GORM                           |
|                  /        \                        |
|              SQLite      PostgreSQL                |
+----------------------------------------------------+
```

---

# 67. Project Identity

Working name:

```text
gflow
```

Recommended positioning:

> An embeddable open-source workflow engine for APIs, AI agents, and SaaS products.

Alternative short description:

> Go-first workflow automation engine with an embeddable Svelte canvas.

Core differentiation:

```text
Go-native
single binary
API-first
embeddable
white-label
AI-agent capable
n8n-interoperable
SQLite by default
```

---

# 68. Summary

The most important architectural decisions for gflow V1 are:

1. **Go owns the workflow engine.**
2. **SvelteKit is a full SPA bundled into the Go binary.**
3. **Frontend API calls always use relative URLs.**
4. **Vite proxies `/api` and related paths to the Go backend in development.**
5. **GORM handles internal persistence.**
6. **SQLite is the default; PostgreSQL is optional.**
7. **Microsoft Agent Framework is used behind a gflow AI adapter.**
8. **Basic Memory storage remains owned by gflow.**
9. **Go Code executes through WASM isolation rather than inside the main process.**
10. **The editor is iframe-first and white-label capable.**
11. **n8n support is interoperability, not a runtime dependency.**
12. **The V1 focus is the workflow engine, not rebuilding an entire AI framework.**

This architecture keeps gflow small enough to ship, while leaving a clean path toward more advanced workflow automation, AI agents, plugins, and distributed execution later.
