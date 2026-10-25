# 🎯 MothX Use Cases & Real-World Examples

> From daily development to enterprise deployment, MothX meets all your needs.

---

## 💻 Daily Development

### Scenario 1: Quick Code Generation

```bash
# Generate a Go HTTP server
mothx -P "Write a Go HTTP server with RESTful API and user authentication"

# Generate a React component
mothx -P "Create a React search component with debounce and loading state"

# Generate a Python scraper
mothx -P "Write a Python scraper to fetch Hacker News headlines"
```

### Scenario 2: Code Understanding

```bash
# Explain code
mothx -P "Explain what main.go does"

# Explain regex
mothx -P "What does this regex mean? ^(?:https?:\/\/)?(?:www\.)?([^\/]+)"

# Analyze architecture
mothx -P "Analyze this project's architecture and draw component relationships"
```

### Scenario 3: Code Refactoring

```bash
# Refactor to generics
mothx -P "Refactor this function to use generics"

# Optimize performance
mothx -P "Optimize this code for performance, reduce memory allocations"

# Split modules
mothx -P "Split this class into smaller modules, following single responsibility principle"
```

### Scenario 4: Test Generation

```bash
# Unit tests
mothx -P "Write unit tests for UserService, covering all edge cases"

# Integration tests
mothx -P "Generate integration test cases for API endpoints"

# End-to-end tests
mothx -P "Write an end-to-end test simulating user login flow"
```

### Scenario 5: Documentation

```bash
# Function comments
mothx -P "Generate JSDoc comments for this function"

# README
mothx -P "Write a README.md for this project with installation and usage instructions"

# API documentation
mothx -P "Generate API documentation with request/response examples"
```

---

## 🔍 Code Review

### Scenario 1: PR Review

```bash
# Review PR
mothx --mode plan "Review this PR, find potential issues and suggestions"

# Security review
mothx --mode plan "Review this code for security vulnerabilities"

# Performance review
mothx --mode plan "Review this code for performance bottlenecks"
```

### Scenario 2: Code Quality

```bash
# Code standards
mothx --mode plan "Check if this code follows Go coding standards"

# Error handling
mothx --mode plan "Check if this code has proper error handling"

# Concurrency safety
mothx --mode plan "Check if this code is concurrency-safe"
```

---

## 🚀 CI/CD Integration

### Scenario 1: Auto-generate Documentation

```bash
# Generate changelog
mothx -p "Generate changelog from git log, grouped by version" > CHANGELOG.md

# Generate API docs
mothx -p "Generate API documentation from code comments" > docs/api.md

# Generate migration guide
mothx -p "Generate migration guide from v1 to v2" > docs/migration.md
```

### Scenario 2: Code Analysis

```bash
# Static analysis
mothx -p "Analyze this code for potential issues" > analysis.txt

# Security scan
mothx -p "Scan this code for security vulnerabilities" > security.txt

# Performance analysis
mothx -p "Analyze this code for performance bottlenecks" > performance.txt
```

### Scenario 3: Automated Testing

```bash
# Generate test cases
mothx -p "Generate test cases for this function" > tests/function_test.go

# Generate test data
mothx -p "Generate test data with edge cases" > testdata.json

# Generate test report
mothx -p "Generate test report from test results" > report.md
```

---

## 🌐 API Server

### Scenario 1: Team Sharing

```bash
# Start serve
mothx serve

# Config file ~/.mothx/serve.json
{
  "port": 8080,
  "auth": {
    "token": "your-secret-token"
  },
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### Scenario 2: API Integration

```python
import requests

# Call MothX API
response = requests.post(
    "http://localhost:8080/v1/chat/completions",
    headers={"Authorization": "Bearer your-secret-token"},
    json={
        "model": "deepseek-v4-flash",
        "messages": [{"role": "user", "content": "Hello, MothX!"}]
    }
)

print(response.json())
```

### Scenario 3: Load Balancing

```yaml
# docker-compose.yml
version: '3'
services:
  mothx-1:
    image: mothx
    command: serve
    ports:
      - "8081:8080"
  
  mothx-2:
    image: mothx
    command: serve
    ports:
      - "8082:8080"
  
  nginx:
    image: nginx
    ports:
      - "80:80"
    volumes:
      - ./nginx.conf:/etc/nginx/nginx.conf
```

---

## 📱 Chatbot

### Scenario 1: WeChat Bot

```bash
# Start messaging channels
mothx serve

# Config file ~/.mothx/serve.json
{
  "platform": "wechat",
  "appId": "your-app-id",
  "appSecret": "your-app-secret",
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### Scenario 2: Feishu Bot

```bash
# Config file ~/.mothx/serve.json
{
  "platform": "feishu",
  "appId": "your-app-id",
  "appSecret": "your-app-secret",
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### Scenario 3: WebSocket

```bash
# Config file ~/.mothx/serve.json
{
  "platform": "websocket",
  "port": 8080,
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

---

## 🤝 Multi-Agent Collaboration

### Scenario 1: Large Task Decomposition

```text
# Enable multi-agent mode
mothx --multi-agent

# Main agent decomposes task
> "Decompose this large task into 5 subtasks, assign each to a sub-agent"

# The agent can call subagent_spawn internally for each focused subtask.
```

### Scenario 2: Parallel Execution

Example tool-call payloads used by the agent:

```json
{ "tool": "subagent_spawn", "arguments": { "task": "Task 1: Process dataset A" } }
{ "tool": "subagent_spawn", "arguments": { "task": "Task 2: Process dataset B" } }
{ "tool": "subagent_status", "arguments": { "handle": "subagent-job-1" } }
{ "tool": "subagent_send", "arguments": { "handle": "subagent-job-1", "message": "Return results" } }
```

### Scenario 3: A2A Remote Collaboration

```bash
# Enable A2A Master mode
mothx --enable-a2a-master

# Configure remote agents
# a2a-list.json
[
  {
    "name": "data-agent",
    "url": "http://agent1.example.com:8080",
    "description": "Data processing agent"
  },
  {
    "name": "report-agent",
    "url": "http://agent2.example.com:8080",
    "description": "Report generation agent"
  }
]

# Main agent automatically dispatches tasks
> "Analyze data and generate report"
```

### Scenario 4: Dynamic Workflow Orchestration

Use Workflow mode for structured multi-phase, verified complex task orchestration:

```bash
# Enable Workflow mode
mothx --workflows

# Ask AI to run a security audit workflow
> "Run a security audit on internal/serve/openaiapi and internal/serve/channels, parallel scan then cross-verify"

# AI automatically generates and executes an JavaScript workflow like:
# - phase 1: parallel scan of multiple modules
# - phase 2: cross-verify results, drop weak findings
# - phase 3: generate final audit report
```

Workflow mode is ideal for code audits, architecture research, multi-role reviews, generate-critic loops, and other scenarios requiring structured multi-agent collaboration. See the [Workflow Mode](workflow.md) documentation for details.

---

## 🛠️ System Administration

### Scenario 1: Server Management

```bash
# Check server status
mothx --mode yolo "Check server CPU, memory, and disk usage"

# Clean logs
mothx --mode yolo "Clean log files older than 30 days in /var/log"

# Backup data
mothx --mode yolo "Backup database to /backup directory"
```

### Scenario 2: Docker Management

```bash
# Generate Dockerfile
mothx -P "Generate a Dockerfile for this Node.js project"

# Generate docker-compose.yml
mothx -P "Generate docker-compose.yml with Node.js, PostgreSQL, and Redis"

# Optimize image
mothx -P "Optimize this Dockerfile to reduce image size"
```

### Scenario 3: Kubernetes Management

```bash
# Generate Kubernetes config
mothx -P "Generate Kubernetes Deployment and Service configuration"

# Generate Helm Chart
mothx -P "Generate a Helm Chart for this application"

# Troubleshoot
mothx --mode plan "Analyze this Kubernetes Pod logs, find crash reason"
```

---

## 📊 Data Analysis

### Scenario 1: Data Processing

```bash
# Data cleaning
mothx -P "Clean this CSV file with Python, handle missing values and outliers"

# Data transformation
mothx -P "Convert JSON data to CSV format"

# Data aggregation
mothx -P "Aggregate sales data by month, calculate total and average"
```

### Scenario 2: Data Visualization

```bash
# Generate charts
mothx -P "Generate a sales trend chart with Matplotlib"

# Generate dashboard
mothx -P "Generate an interactive dashboard with Plotly"

# Generate report
mothx -P "Generate an analysis report from data with charts and conclusions"
```

---

## 🎓 Learning & Education

### Scenario 1: Code Learning

```bash
# Explain code
mothx -P "Explain this code line by line"

# Explain algorithms
mothx -P "Explain the quicksort algorithm principle and implementation"

# Explain design patterns
mothx -P "Explain the singleton pattern use cases and implementation"
```

### Scenario 2: Programming Practice

```bash
# Generate exercises
mothx -P "Generate 10 Python programming exercises with increasing difficulty"

# Check answers
mothx -P "Check if this answer is correct"

# Generate explanations
mothx -P "Generate detailed explanations for this problem"
```

### Scenario 3: Project Guidance

```bash
# Project planning
mothx -P "Help me plan the architecture of a blog system"

# Tech stack selection
mothx -P "Recommend a tech stack suitable for this project"

# Code review
mothx -P "Review my code and give improvement suggestions"
```

---

## 🏢 Enterprise Applications

### Scenario 1: Code Standards

```bash
# Generate standards document
mothx -P "Generate team coding standards document"

# Check standards
mothx --mode plan "Check if this code follows team standards"

# Auto-fix
mothx -P "Auto-fix code standards issues in this code"
```

### Scenario 2: Knowledge Base

```bash
# Generate knowledge base
mothx -P "Generate knowledge base documentation from code comments"

# Search knowledge base
mothx -P "Search knowledge base for user authentication documentation"

# Update knowledge base
mothx -P "Update knowledge base with new API documentation"
```

### Scenario 3: Automation Workflows

```bash
# Generate workflow
mothx -P "Generate GitHub Actions workflow for automated testing and deployment"

# Generate scripts
mothx -P "Generate automation script for daily database backup"

# Generate monitoring
mothx -P "Generate monitoring script to detect server anomalies"
```

---

## 🎯 Best Practices

### 1. Choose the Right Mode

- **Plan mode**: For analysis, planning, code review
- **Agent mode**: For daily development, code generation, testing
- **YOLO mode**: For system administration, automation scripts

### 2. Use Skills System

```bash
# Create project skill
.skills/conventions/SKILL.md

# Skill content example
# Project Coding Standards

## Naming Conventions
- Variables: camelCase
- Functions: camelCase
- Classes: PascalCase
- Constants: UPPER_SNAKE_CASE

## Code Style
- Use 4-space indentation
- Max 120 characters per line
- Use single quotes for strings

## Comment Standards
- Functions must have JSDoc comments
- Complex logic must have inline comments
- TODOs must include owner
```

### 3. Configure Approval Whitelist

```json
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git ", "npm "],
    "bashBlacklist": ["rm -rf", "sudo"],
    "confirmBeforeWrite": true
  }
}
```

### 4. Use Session Management

```bash
# Continue most recent session
mothx --continue

# Resume specific session
mothx --resume <session-id>

# Use specific session handle file
mothx --session <session-file.db>
```

### 5. Monitor Cache Hit Rate

- Check cache hit rate in TUI footer
- Optimize prompts to improve cache hit rate
- Monitor token usage to control costs

---

## 📖 More Resources

- [5-Minute Quick Start](quick-start-tutorial.md) — Skip the long docs, just start coding!
- [Features Overview](features-overview.md) — Learn about all features
- [Configuration Guide](configuration.md) — Customize settings
- [Tool Reference](tools.md) — All built-in tools
- [FAQ](faq.md) — Common questions answered

---

<p align="center">
  <strong>🎉 Now you've mastered all MothX use cases.</strong><br>
  <strong>Start your AI coding journey!</strong>
</p>
