# 🚀 MothX 5 分钟快速上手

> 别读长文档了，直接上手！

## 第一步：安装（30 秒）

```bash
# 方式一：npm（推荐，自动下载对应平台二进制文件）
npm install -g mothx-installer

# 方式二：PyPI
pipx install mothx-installer

# 方式三：一键安装（Linux/macOS）
curl -fsSL https://mothx.net/install.sh | bash
```

```bat
:: 方式四：一键安装（Windows 命令提示符）
curl.exe -fsSL https://mothx.net/install.bat -o install.bat && install.bat
```

```bash
# 方式五：Go 安装
go install github.com/oschina/mothx/cmd/mothx@latest
```

**卸载:**

```bash
# npm
npm uninstall -g mothx-installer

# PyPI
pipx uninstall mothx-installer

# Linux/macOS（一键安装）
curl -fsSL https://mothx.net/install.sh | bash -s -- --uninstall
```

## 第二步：配置 API 密钥（30 秒）

```bash
# 设置 DeepSeek API 密钥（默认提供商）
export DEEPSEEK_API_KEY=sk-...

# 或者使用 OpenAI
export OPENAI_API_KEY=sk-...

# 或者使用 Anthropic
export ANTHROPIC_API_KEY=sk-ant-...
```

## 第三步：运行！（10 秒）

```bash
# 启动交互式会话
mothx

# 或者直接提问
mothx -P "Hello, MothX!"
```

就这样，你已经在用 AI 编程了！

---

## 🎮 三种模式，随心切换

| 模式 | 命令 | 用途 |
|------|------|------|
| 🗒️ **Plan** | `mothx --mode plan` | 只读分析，安全探索 |
| 🔧 **Agent** | `mothx --mode agent` | 标准开发 |
| 🚀 **YOLO** | `mothx --mode yolo` | 完全自由，没有限制（默认） |

在交互模式中，按 `Tab` 或输入 `/mode plan|agent|yolo` 切换。

---

## 💡 常用场景速查

### 📝 代码生成
```bash
mothx -P "写一个 Go HTTP 服务器，支持 RESTful API"
mothx -P "用 Python 写一个爬虫，抓取新闻标题"
mothx -P "生成一个 React 组件，包含搜索框和列表"
```

### 🔍 代码理解
```bash
mothx -P "解释 main.go 的作用"
mothx -P "这段正则表达式是什么意思？"
mothx -P "分析这个项目的架构"
```

### 🛠️ 代码重构
```bash
mothx -P "把这个函数重构成泛型版本"
mothx -P "优化这段代码的性能"
mothx -P "把这个类拆分成更小的模块"
```

### 🧪 测试生成
```bash
mothx -P "为 UserService 写单元测试"
mothx -P "生成集成测试用例"
mothx -P "写一个端到端测试"
```

### 📚 文档生成
```bash
mothx -P "为这个函数生成 JSDoc 注释"
mothx -P "写一个 README.md"
mothx -P "生成 API 文档"
```

---

## ⌨️ 快捷键速查

| 快捷键 | 功能 |
|--------|------|
| `Enter` | 提交 prompt |
| `Alt+Enter` | 换行 |
| `Tab` | 切换模式 |
| `Esc` | 中止当前操作 |
| `Ctrl+O` | 打开工具详情 |
| `Ctrl+G` | 切换简洁/完整事件显示（默认简洁） |
| `Up/Down` | 浏览历史 |
| `Ctrl+C` | 取消/清空输入 |

---

## 🔧 常用命令

```bash
/mode plan      # 切换到 Plan 模式
/mode agent     # 切换到 Agent 模式
/mode yolo      # 切换到 YOLO 模式
/model          # 查看当前模型
/think          # 切换思考级别
/clear          # 清空对话
/help           # 显示帮助
/quit           # 退出
```

---

## 🎯 实战示例

### 示例 1：创建一个新项目
```bash
mothx -P "创建一个 Express.js 项目，包含用户认证和数据库连接"
```

### 示例 2：调试代码
```bash
mothx -P "这段代码报错了：TypeError: Cannot read property 'map' of undefined，帮我修复"
```

### 示例 3：代码审查
```bash
mothx --mode plan "审查当前目录的代码，找出潜在问题"
```

### 示例 4：生成配置文件
```bash
mothx -P "生成一个 Docker Compose 文件，包含 Node.js、PostgreSQL 和 Redis"
```

### 示例 5：写正则表达式
```bash
mothx -P "写一个正则表达式，匹配中国手机号码"
```

---

## 🛡️ 安全提示

- **Plan 模式**：只读，不会修改文件，适合探索和分析
- **Agent 模式**：读写，但 bash 命令需要审批（可配置白名单）
- **YOLO 模式**：完全自由，谨慎使用！

默认情况下，危险命令（如 `rm -rf`、`sudo`）会被黑名单拦截。

---

## 📱 进阶用法

### IDE 集成
```bash
# VS Code：在 settings.json 中添加
{
  "acp.agents": {
    "mothx": {
      "command": "mothx",
      "args": ["acp", "--mode", "agent"]
    }
  }
}
```

### API 服务器
```bash
# 启动 OpenAI 兼容的 HTTP 服务器
mothx serve
```

### 聊天机器人
```bash
# 部署为微信/飞书机器人
mothx serve
```

---

## 🆘 遇到问题？

```bash
# 运行诊断命令
mothx doctor

# 查看帮助
mothx --help

# 启用调试模式
mothx --debug
```

---

## 📖 更多资源

- [完整文档](README.md) — 所有功能详解
- [配置指南](configuration.md) — 自定义设置
- [工具参考](tools.md) — 所有内置工具
- [场景演示](scenarios.md) — 更多实战示例
- [FAQ](faq.md) — 常见问题解答

---

<p align="center">
  <strong>🎉 恭喜！你已经掌握了 MothX 的基础用法。</strong><br>
  <strong>现在，开始你的 AI 编程之旅吧！</strong>
</p>
