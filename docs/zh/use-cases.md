# 🎯 MothX 使用场景与实战示例

> 从日常开发到企业部署，MothX 满足你的所有需求。

---

## 💻 日常开发

### 场景 1：快速生成代码

```bash
# 生成一个 Go HTTP 服务器
mothx -P "写一个 Go HTTP 服务器，支持 RESTful API，包含用户认证"

# 生成一个 React 组件
mothx -P "创建一个 React 搜索组件，支持防抖和加载状态"

# 生成一个 Python 爬虫
mothx -P "用 Python 写一个爬虫，抓取 Hacker News 首页标题"
```

### 场景 2：代码理解

```bash
# 解释代码
mothx -P "解释 main.go 的作用"

# 解释正则表达式
mothx -P "这段正则表达式是什么意思？^(?:https?:\/\/)?(?:www\.)?([^\/]+)"

# 分析架构
mothx -P "分析这个项目的架构，画出组件关系图"
```

### 场景 3：代码重构

```bash
# 重构为泛型
mothx -P "把这个函数重构成泛型版本"

# 优化性能
mothx -P "优化这段代码的性能，减少内存分配"

# 拆分模块
mothx -P "把这个类拆分成更小的模块，遵循单一职责原则"
```

### 场景 4：测试生成

```bash
# 单元测试
mothx -P "为 UserService 写单元测试，覆盖所有边界情况"

# 集成测试
mothx -P "生成集成测试用例，测试 API 端点"

# 端到端测试
mothx -P "写一个端到端测试，模拟用户登录流程"
```

### 场景 5：文档生成

```bash
# 函数注释
mothx -P "为这个函数生成 JSDoc 注释"

# README
mothx -P "为这个项目写一个 README.md，包含安装和使用说明"

# API 文档
mothx -P "生成 API 文档，包含请求/响应示例"
```

---

## 🔍 代码审查

### 场景 1：PR 审查

```bash
# 审查 PR
mothx --mode plan "审查这个 PR，找出潜在问题和改进建议"

# 安全审查
mothx --mode plan "审查这段代码的安全性，找出潜在漏洞"

# 性能审查
mothx --mode plan "审查这段代码的性能，找出瓶颈"
```

### 场景 2：代码质量

```bash
# 代码规范
mothx --mode plan "检查这段代码是否符合 Go 编码规范"

# 错误处理
mothx --mode plan "检查这段代码的错误处理是否完善"

# 并发安全
mothx --mode plan "检查这段代码的并发安全性"
```

---

## 🚀 CI/CD 集成

### 场景 1：自动生成文档

```bash
# 生成更新日志
mothx -p "从 git log 生成更新日志，按版本分组" > CHANGELOG.md

# 生成 API 文档
mothx -p "从代码注释生成 API 文档" > docs/api.md

# 生成迁移指南
mothx -p "从 v1 到 v2 的迁移指南" > docs/migration.md
```

### 场景 2：代码检查

```bash
# 静态分析
mothx -p "分析这段代码的潜在问题" > analysis.txt

# 安全扫描
mothx -p "扫描这段代码的安全漏洞" > security.txt

# 性能分析
mothx -p "分析这段代码的性能瓶颈" > performance.txt
```

### 场景 3：自动化测试

```bash
# 生成测试用例
mothx -p "为这个函数生成测试用例" > tests/function_test.go

# 生成测试数据
mothx -p "生成测试数据，包含边界情况" > testdata.json

# 生成测试报告
mothx -p "从测试结果生成测试报告" > report.md
```

---

## 🌐 API 服务器

### 场景 1：团队共享

```bash
# 启动 serve
mothx serve

# 配置文件 ~/.mothx/serve.json
{
  "port": 8080,
  "auth": {
    "token": "your-secret-token"
  },
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### 场景 2：API 集成

```python
import requests

# 调用 MothX API
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

### 场景 3：负载均衡

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

## 📱 聊天机器人

### 场景 1：微信机器人

```bash
# 启动消息通道
mothx serve

# 配置文件 ~/.mothx/serve.json
{
  "platform": "wechat",
  "appId": "your-app-id",
  "appSecret": "your-app-secret",
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### 场景 2：飞书机器人

```bash
# 配置文件 ~/.mothx/serve.json
{
  "platform": "feishu",
  "appId": "your-app-id",
  "appSecret": "your-app-secret",
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

### 场景 3：WebSocket

```bash
# 配置文件 ~/.mothx/serve.json
{
  "platform": "websocket",
  "port": 8080,
  "defaultProvider": "deepseek-openai",
  "defaultModel": "deepseek-v4-flash"
}
```

---

## 🤝 多 Agent 协作

### 场景 1：大型任务分解

```text
# 启用多 Agent 模式
mothx --multi-agent

# 主 Agent 分解任务
> "把这个大任务分解成 5 个子任务，每个子任务分配给一个子 Agent"

# Agent 会在内部为每个边界清晰的子任务调用 subagent_spawn。
```

### 场景 2：并行执行

Agent 使用的工具调用 payload 示例：

```json
{ "tool": "subagent_spawn", "arguments": { "task": "任务1: 处理数据集 A" } }
{ "tool": "subagent_spawn", "arguments": { "task": "任务2: 处理数据集 B" } }
{ "tool": "subagent_status", "arguments": { "handle": "subagent-job-1" } }
{ "tool": "subagent_send", "arguments": { "handle": "subagent-job-1", "message": "返回处理结果" } }
```

### 场景 3：A2A 远程协作

```bash
# 启用 A2A Master 模式
mothx --enable-a2a-master

# 配置远程 Agent
# a2a-list.json
[
  {
    "name": "data-agent",
    "url": "http://agent1.example.com:8080",
    "description": "数据处理 Agent"
  },
  {
    "name": "report-agent",
    "url": "http://agent2.example.com:8080",
    "description": "报告生成 Agent"
  }
]

# 主 Agent 自动分发任务
> "分析数据并生成报告"
```

### 场景 4：动态 Workflow 编排

使用 Workflow 模式进行多阶段、带验证的复杂任务编排：

```bash
# 启用 Workflow 模式
mothx --workflows

# 让 AI 运行代码审计 workflow
> "对 internal/serve/openaiapi 和 internal/serve/channels 做一次安全审计，先并行扫描再交叉验证"

# AI 会自动生成并执行类似这样的 JavaScript workflow:
# - phase 1: 并行扫描多个模块
# - phase 2: 交叉验证结果，剔除弱结论
# - phase 3: 生成最终审计报告
```

Workflow 模式适合代码审计、架构调研、多角色评审、生成-评审循环等需要结构化多智能体协作的场景。详见 [Workflow 模式](workflow.md) 文档。

---

## 🛠️ 系统管理

### 场景 1：服务器管理

```bash
# 检查服务器状态
mothx --mode yolo "检查服务器的 CPU、内存、磁盘使用情况"

# 清理日志
mothx --mode yolo "清理 /var/log 下超过 30 天的日志文件"

# 备份数据
mothx --mode yolo "备份数据库到 /backup 目录"
```

### 场景 2：Docker 管理

```bash
# 生成 Dockerfile
mothx -P "为这个 Node.js 项目生成 Dockerfile"

# 生成 docker-compose.yml
mothx -P "生成 docker-compose.yml，包含 Node.js、PostgreSQL、Redis"

# 优化镜像
mothx -P "优化这个 Dockerfile，减少镜像大小"
```

### 场景 3：Kubernetes 管理

```bash
# 生成 Kubernetes 配置
mothx -P "生成 Kubernetes Deployment 和 Service 配置"

# 生成 Helm Chart
mothx -P "为这个应用生成 Helm Chart"

# 故障排查
mothx --mode plan "分析这个 Kubernetes Pod 的日志，找出崩溃原因"
```

---

## 📊 数据分析

### 场景 1：数据处理

```bash
# 数据清洗
mothx -P "用 Python 清洗这个 CSV 文件，处理缺失值和异常值"

# 数据转换
mothx -P "把 JSON 数据转换为 CSV 格式"

# 数据聚合
mothx -P "按月份聚合销售数据，计算总销售额和平均值"
```

### 场景 2：数据可视化

```bash
# 生成图表
mothx -P "用 Matplotlib 生成销售趋势图"

# 生成仪表盘
mothx -P "用 Plotly 生成交互式仪表盘"

# 生成报告
mothx -P "从数据生成分析报告，包含图表和结论"
```

---

## 🎓 学习与教育

### 场景 1：代码学习

```bash
# 解释代码
mothx -P "解释这段代码的作用，逐行分析"

# 解释算法
mothx -P "解释快速排序算法的原理和实现"

# 解释设计模式
mothx -P "解释单例模式的使用场景和实现"
```

### 场景 2：编程练习

```bash
# 生成练习题
mothx -P "生成 10 道 Python 编程练习题，难度递增"

# 检查答案
mothx -P "检查这道题的答案是否正确"

# 生成解析
mothx -P "为这道题生成详细解析"
```

### 场景 3：项目指导

```bash
# 项目规划
mothx -P "帮我规划一个博客系统的架构"

# 技术选型
mothx -P "推荐适合这个项目的技术栈"

# 代码审查
mothx -P "审查我的代码，给出改进建议"
```

---

## 🏢 企业应用

### 场景 1：代码规范

```bash
# 生成规范文档
mothx -P "生成团队代码规范文档"

# 检查规范
mothx --mode plan "检查这段代码是否符合团队规范"

# 自动修复
mothx -P "自动修复这段代码的规范问题"
```

### 场景 2：知识库

```bash
# 生成知识库
mothx -P "从代码注释生成知识库文档"

# 搜索知识库
mothx -P "在知识库中搜索关于用户认证的文档"

# 更新知识库
mothx -P "更新知识库，添加新的 API 文档"
```

### 场景 3：自动化流程

```bash
# 生成工作流
mothx -P "生成 GitHub Actions 工作流，自动测试和部署"

# 生成脚本
mothx -P "生成自动化脚本，每天备份数据库"

# 生成监控
mothx -P "生成监控脚本，检测服务器异常"
```

---

## 🎯 最佳实践

### 1. 选择合适的模式

- **Plan 模式**：用于分析、规划、代码审查
- **Agent 模式**：用于日常开发、代码生成、测试
- **YOLO 模式**：用于系统管理、自动化脚本

### 2. 使用技能系统

```bash
# 创建项目技能
.skills/conventions/SKILL.md

# 技能内容示例
# 项目编码规范

## 命名规范
- 变量名：camelCase
- 函数名：camelCase
- 类名：PascalCase
- 常量：UPPER_SNAKE_CASE

## 代码风格
- 使用 4 空格缩进
- 每行不超过 120 字符
- 使用单引号字符串

## 注释规范
- 函数必须有 JSDoc 注释
- 复杂逻辑必须有行内注释
- TODO 必须包含负责人
```

### 3. 配置审批白名单

```json
{
  "approval": {
    "bashWhitelist": ["go ", "make ", "git ", "npm "],
    "bashBlacklist": ["rm -rf", "sudo"],
    "confirmBeforeWrite": true
  }
}
```

### 4. 使用会话管理

```bash
# 继续最近的会话
mothx --continue

# 恢复特定会话
mothx --resume <session-id>

# 使用特定会话句柄文件
mothx --session <session-file.db>
```

### 5. 监控缓存命中率

- 在 TUI 底部查看缓存命中率
- 优化 prompt 以提高缓存命中率
- 监控 token 使用情况，控制成本

---

## 📖 更多资源

- [5 分钟快速上手](quick-start-tutorial.md) — 别读长文档，直接上手！
- [核心特性详解](features-overview.md) — 了解所有功能
- [配置详解](configuration.md) — 自定义设置
- [工具参考](tools.md) — 所有内置工具
- [FAQ](faq.md) — 常见问题解答

---

<p align="center">
  <strong>🎉 现在，你已经掌握了 MothX 的所有使用场景。</strong><br>
  <strong>开始你的 AI 编程之旅吧！</strong>
</p>
