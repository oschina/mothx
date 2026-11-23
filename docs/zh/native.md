# MothX Native 桌面客户端

MothX Native 是 MothX 的**主力桌面客户端**。它是用
[MyGo](https://github.com/egoist/mygo) 的 `ui` 包编写的纯 Go 应用：所有控件都由
应用自绘——没有 HTML、没有 JavaScript、没有 webview、没有 Electron。与已弃用的
Desktop 客户端一样，它是**纯 ACP（Agent Client Protocol）客户端**：通过 stdio
启动 `mothx acp` 子进程，使用 ACP v1（newline-delimited JSON-RPC 2.0）与增量
`mothx.dev` 扩展通信。会话、运行、设置、供应商、技能、知识库与全部管理面都由
Runtime 持有；Native 只做投影。

> **状态：** Native 取代 Electron [Desktop 客户端](desktop.md)，后者已弃用。
> 版本 tag 的发布页自动发布 Native 桌面产物；迁移窗口内 Electron 构建仅保留
> 手动兼容构建。

## 安装

从发布页下载对应平台产物（`mothx-native-<版本>-<平台>-<架构>`）：

- **Linux amd64：** 裸二进制、`.deb`、`.tar.gz` 或 `PKGBUILD`。
- **macOS universal：** `.dmg`。
- **Windows amd64：** 便携 `.exe` 或 `-setup.exe` 安装包。

Native 需要一个 `mothx` 运行时二进制。解析顺序：启动参数 `--mothx-bin`、
设置 → 运行时中持久化的选择、`MOTHX_BIN`、`PATH`、仓库 `../bin/mothx`
开发回退。持久化选择只是客户端外壳的启动状态，绝不写入
`settings.json`/`serve.json`。所选二进制启动或初始化失败时，Native 会自动
回退到之前的选择。

也可以从源码构建：

```sh
make native-build        # native/build/ 下的 stripped 二进制
make native-dist         # 当前平台打包
make native-dist-linux   # linux/amd64（deb、tar.gz、PKGBUILD）
make native-dist-darwin  # darwin/universal
make native-dist-windows # windows/amd64
```

## 功能一览

- **任务：** 项目/最近/未归类侧栏与会话菜单、cursor 分页、置顶、项目分组、
  重命名、Fork、删除、改工作目录；带搜索与 scope 筛选的分页历史视图。
- **聊天：** 流式 markdown、折叠时仍带 live 预览的思考、含输入/diff/输出的
  工具卡、计划卡与计划进度侧栏（含 Runtime 提供的计划标题/备注）、带截止
  时间的内联审批/提问卡（多 property elicitation form 按 schema 每 property
  渲染一个字段，按 property 名 keyed content 应答）、内联子代理生命周期卡与
  头部运行中/总数计数、结构化错误卡、内联图片、制品预览/保存、完整转录复制
  与导出。
- **输入区：** 模式、供应商/模型/思考级别选择（带能力徽标）、主角团绑定
  （替换非空绑定会 fork）、沙箱/浏览器/Web 搜索能力、附件（工作区内文件走
  link，工作区外文件与剪贴板图片内嵌）、斜杠指令、带最近目录历史的工作区菜单。
- **页面：** 技能（指令卡 + SkillHub 市场）、自动化（cron）、资料库（知识库
  状态），均带页面级管理对话框。
- **设置：** 与 Desktop 完全相同的管理面——应用默认、供应商、MCP、技能、
  SkillHub、主角团、ACP 工作器、环境变量、消息通道 + 投递、记忆、定时任务、
  serve、知识库、统计——以及本地外观（主题、语言、首页预设/Logo/背景，背景
  可选全应用或仅首页作用域）、运行时（连接、doctor、日志、二进制选择）、关于。
- **协作：** 隔离 git worktree、Router 工作器选择与只读 worker 证据、只读
  子代理名册/转录。
- **外壳：** 深/浅/跟随系统主题、中英文界面、Toast、按会话缓存后台运行、
  后台审批待处理徽标。

与 Desktop 一致：新进程的首个草稿获得私有 `~/MothX/mothx_projects_*`
quick-start 目录；显式选择过的目录只作为选择器历史被记住。**新任务**草稿不
继承目录：创建任务时外壳会询问一次，发送时若仍未设置会再问一次（取消则保留
草稿）。在目录存在之前，供应商/模型菜单按 Desktop 的做法直接从供应商目录
投影（`draft-config-options` 需要 cwd）。两者都属于
`native-preferences.json` 中的展示状态（主题、语言、首页外观、运行时二进制、
目录历史）——绝不是会话或配置权威。

## 从 Desktop 迁移

没有任何权威数据需要搬移：会话、转录、供应商、MCP 服务器、技能、主角团、
知识库、计划任务与通道配置都在共享 Runtime 中，在 Native 里与 Desktop
（以及 TUI/WebUI）完全一致地出现。留在原地的只有 Electron 本地展示状态：

| Desktop（Electron） | Native |
|---|---|
| `desktop-store.json` 主题/语言 | 设置 → 外观（重新选择一次） |
| 首页 Logo/背景、透明度/模糊/fit/position | 设置 → 外观，另新增全应用/仅首页作用域 |
| 内置/自定义运行时二进制 | 设置 → 运行时二进制选择（失败自动回滚） |
| 上次/最近工作区选择历史 | Native 偏好自动记住 |
| DevTools / 本地 CDP 调试 | 设置 → 运行时诊断与 ACP stderr 日志 |

能力门控面板的行为不变：面板缺失说明运行时没有声明对应能力键——应更新
运行时二进制，而不是外壳。

## 故障排查

- **Linux：** Native 自绘窗口，但仍需要显示服务器（X11/Wayland）。无显示器
  主机请使用 TUI 或 `mothx serve` + Web UI。
- **运行时无法启动：** 在设置 → 运行时查看解析到的二进制与来源；自定义
  二进制切换失败会自动回滚。
- **审批/提问一直不结束：** Runtime 决策超时
  （`MOTHX_ACP_PERMISSION_TIMEOUT` / `MOTHX_ACP_QUESTION_TIMEOUT`）不变；
  无法恢复的 pending decision 由 Runtime 终止化。

## 开发

```sh
make native-dev    # 从源码运行
make native-test   # 转录 reducer + 无头 UI 测试
make native-vet    # go vet + MyGo 生命周期/goroutine 检查
```

无头 tester 离线渲染全部页面；英文扫描对任何未翻译中文 UI 直接失败，像素
探针固定背景图层顺序。
## 代码结构

`native/main.go` 只是入口。实现位于私有 `internal/` 包：`internal/acp`
（stdio JSON-RPC 传输与 wire payload）、`internal/i18n`、`internal/icons`、
`internal/kit`（共享配色/控件），以及 `internal/feature/*` 下按 Runtime 管理
业务拆分的包（供应商、技能、SkillHub、MCP、知识库、主角团、定时任务、通道、
环境变量、记忆、统计、Serve、ACP 工作器）。`internal/desktop` 是组合外壳，
负责应用生命周期、会话/运行状态、转录/输入区、任务树、历史、worktree、协作
与页面布局。业务包不得反向 import 外壳；外壳实现各业务包声明的窄 `Host`
接口。
