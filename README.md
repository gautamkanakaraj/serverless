# ⚡ Mini AWS Lambda (Multi-Tenant Serverless Platform)

A lightweight, multi-tenant serverless function platform designed to eliminate traditional cloud infrastructure complexity by providing a frictionless, sub-millisecond **"Code-to-URL"** deployment and execution workflow.

---

## 🏗️ Architecture Diagram

```mermaid
graph TD
    %% Define Nodes
    Client([Client Browser])
    Proxy[HTTP Ingress / Router]
    ControlPlane[Go Control Plane]
    DB[(Neon Postgres DB)]
    
    subgraph Sandbox ["Execution Sandbox"]
        JS[Goja JS VM]
        Wasm[Wasmtime Python VM]
    end

    WS[WebSocket Broadcaster]
    Logs[Terminal Output]

    %% Define Connections
    Client -->|1. Deploy Code| ControlPlane
    ControlPlane -->|2. Save Snippet| DB
    ControlPlane -->|3. Return Public URL| Client
    
    Client -->|4. HTTP Request| Proxy
    Proxy -->|5. Lookup & Fetch Code| DB
    Proxy -->|6. Run Code| JS
    Proxy -->|6. Run Code| Wasm
    
    JS -->|7. Console Stream| WS
    Wasm -->|7. Console Stream| WS
    WS -->|8. Live Logs| Logs
    Logs -->|9. Render| Client
```

### Request Flow Overview

1. **Deploy**: Developer writes code in the frontend editor and clicks **Deploy**. The Go Control Plane saves the code snippet to Neon Postgres, generates a UUID, and returns a public invocation URL (`/user/code/{uuid}`).
2. **Trigger**: When an HTTP request hits the public URL, the router extracts the UUID and fetches the corresponding code snippet from Postgres.
3. **Execute**: The control plane initializes a lightweight, memory-isolated runtime context (`goja` for JS, `wasmtime` for Python) and executes the code.
4. **Stream**: The sandbox intercepts console output streams (stdout/stderr) and broadcasts them in real time back to the frontend terminal via WebSockets.

---

## 🚧 Work in Progress (Roadmap)

The following core components are currently in progress or planned for development:

* **Database & Schema**:
  * [ ] Populate `db/schema.sql` with table definitions (`functions`, `execution_logs`).
  * [ ] Set up persistent database migration scripts.
* **Execution Sandbox**:
  * [ ] Integrate the WebAssembly/Python execution flow into the main Control Plane router (currently JS-only).
  * [ ] Implement sandbox resource constraints (CPU/Memory limits) and API guardrails to block host-system access.
* **Frontend Dashboard**:
  * [ ] Refactor the current single-page vanilla HTML/CSS/JS frontend into a robust React application (Vite-scaffolded).
  * [ ] Integrate `@monaco-editor/react` to replace the standard HTML textarea.
* **Infrastructure**:
  * [ ] Write `Dockerfile` and configure Render deploy pipeline for zero-downtime scaling.

