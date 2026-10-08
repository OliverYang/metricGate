# metricGate 项目约定

Edit 工具的 ops 语法和失败处理规则在全局 `~/.pi/agent/AGENTS.md` 里，每次会话都会加载，本文件不重复。

对话使用中文

## 改动后

Go 改动完成后跑 `go build ./...`，再对改到的包跑 `go test ./internal/<包>/...`，两件事都通过才算完成。
