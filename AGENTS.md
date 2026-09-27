# metricGate 项目约定

Edit 工具的 ops 语法和失败处理规则在全局 `~/.pi/agent/AGENTS.md` 里，每次会话都会加载，本文件不重复。

## 包布局（别按上游 ast-metrics 猜）

本仓库没有 `internal/analyzer/` 包。实际布局：指标在 `internal/metrics/`（`classes.go`、`halstead.go`、`kindset.go`、`metrics.go`、`mi.go`），规则在 `internal/rules/`，解析在 `internal/parser/`，配置解释在 `internal/config/explain.go`，报告在 `internal/report/`，选择在 `internal/selection/`。

## 二进制与 flag

仓库根的 `./metricgate` 是已构建产物，子命令含 `check`、`bundle`。flag 未定义时它会直接报 `flag provided but not defined: -xxx`，所以不要凭习惯加 `--exclude` 一类参数；`bundle` 的 `--token-budget` 与 `--max-chars` 互斥，只能给一个。

## 第三方克隆只读

`/Users/mac/Downloads/ast-metrics-temp` 和 `~/.pi-desktop/scratch/<session>/ast-metrics` 是拉来对照的上游仓库，只读，不要编辑，也不要把它们的路径当作本仓库路径来 Read。

## 改动后

Go 改动完成后跑 `go build ./...`，再对改到的包跑 `go test ./internal/<包>/...`，两件事都通过才算完成。
