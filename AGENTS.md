# metricGate 项目约定

Edit 工具的 ops 语法和失败处理规则在全局 `~/.pi/agent/AGENTS.md` 里，每次会话都会加载，本文件不重复。

对话使用中文

## 改动后

Go 改动完成后跑 `go build ./...`，再对改到的包跑 `go test ./internal/<包>/...`，两件事都通过才算完成。

## 闸门循环（metricGate MCP，接入在 .pi/mcp.json）

写代码时走三触点，不要绕过：

1. **生成前**：先调 `get_constraints`（拿本仓约束包）或 `find_symbols`（查可复用符号），不要凭空猜仓库规矩。
2. **每次产出后**：对刚写/改的每段代码调 `check_code`（虚拟文件名按扩展名选语言），把 findings 修完再继续，不要攒到最后。
3. **收尾**：`check_path`（必要时 `-diff` 口径）确认整树状态；pre-commit hook 会自动再跑一次 `check -diff`，error 级 finding 会直接拦住提交。

调用记录写入 `.metricgate/mcp-calls.jsonl`（默认开启），是 claims #15 的采集数据，不要删除或绕行。
