# 迭代 5 · deps scan + 测试代码一等公民

日期：2026-10-11（goal mv3oop7j-0d8hel task-5，5/7）

## 交付物

| 交付 | 位置 | 说明 |
|---|---|---|
| deps scan 后端 | `internal/deps/scan.go` | `ScanRoot(root, cfgAllow) (*Draft, error)`；`Draft{Module, Imports, Covered}`；点目录过滤（`hiddenSkip`）；`deps.AnyCovers` / `deps.Uncovered`（边界语义与 checker 一致：exact / prefix+" /" / prefix+"."） |
| 九语言 import 抽取 | `internal/deps/scan_lang.go` | tree-sitter 查询按语言抽 import（Go import_spec、Java scoped_identifier、TS/JS import/export/require、Py dotted_name/aliased/import_from、C/C++ preproc_include、C# qualified_name、Rust use_declaration→crate 归约） |
| 标准库过滤 | `internal/rules/imports.go` `IsStdlib(lang, path)` | C/C++ 引号 include=仓库内部（不草拟）、尖括号=系统头集合（第三方 `<openssl/ssl.h>` 仍草拟）；其余语言去引号后走各语言 stdlib 判定 |
| CLI | `cmd/metricgate/deps_cmd.go` | `metricgate deps scan [-format text\|json] [-allow a,b]`；Go 报完整 module 路径，其余语言取首段 |
| 测试基线（配置） | `internal/config/config.go` | `LanguageConfig.TestMetrics`（yaml `test_metrics`）+ `TestMetricThresholds` / `TestMetricOff`（按级别覆盖，`off` 逐级生效） |
| 测试基线（引擎） | `internal/engine/engine.go` | `EffectiveTestThresholds`（每级 test 覆盖否则回落常规）；`metricVerdictAt` 抽取；`metricFindings` 对 `sources.IsTestPath(stats.File)` 的文件使用测试基线 |
| 共享测试路径判定 | `internal/sources/sources.go` `IsTestPath` | `_test.go`/`Test.java`/`_test.py`/`test_.py`/`_test.ts(x\|js)`/`.test.*`/`_test.rs` + `test`/`tests`/`__tests__` 段；`internal/gatecheck` 改为委托它，两处判定永不分歧 |

## 单测

- `internal/deps/scan_test.go`：多语言草拟断言（ghost/mystery-pkg/numpy/unknown_py.dep/openssl 草拟；local/stdio.h/fmt/known-pkg/os/node:fs 不草拟；`.data/` 排除）、policy allow 计覆盖、点目录跳过、相对路径与本 module 内部导入不草拟；`internal/sources/sources_test.go` TestIsTestPath。
- `internal/engine/test_baseline_test.go`：测试基线放宽后测试文件静默而产品代码仍告警；`off` 在测试中杀死该 metric；无 `test_metrics` 段时回落常规阈值（无静默行为变化）。

## 狗粮记录（gate 每次都抓住了新代码）

1. `scanQuery` cyclomatic 17 > 15 → 拆出 `collectHits(query, root, src, pathIdx)`；复检 PASSED。
2. policy.yaml 启用 `test_metrics.fan-out { warn: 15, error: off }` 后，`check cmd/metricgate-mcp/calllog_test.go` 仍报 threshold 10——根因是 **YAML 缩进**：`test_metrics:` 写在了 `metrics:` 的子级里，被解析成名为 `test_metrics` 的伪 metric 项；Python yaml 往返验证结构后修正缩进，`fan-out` 在 `cmd/metricgate-mcp/calllog_test.go:43/:176`（值 12）静默，产品代码保持 warn 10。
3. 基线生效后把账本里两条 calllog_test.go 让步（:43/:176）用 `accept remove` 删除，`check . -diff -base HEAD` 仍 PASSED（剩 1 条 main.go:35 fan-out 让步）——**策略级基线取代逐行让步**，可审计面更小。

## 调试教训（tree-sitter 绑定）

- `sitter.NewQuery` 可能**同时返回非 nil error 和可用 query**（err 打印 `<nil>`、解引用 QueryError 会 panic）——必须以 `query == nil` 判无效，不能以 err 判。
- parser 必须比 query 活得久：`p.Close()` 先于查询会让 Matches 静默返回空。

## 五性核对

| 性 | 结论 |
|---|---|
| 可介入 | ✅ deps scan 在推广时把"配依赖白名单"从手工抄写变成一条命令；测试基线让测试文件获得策略表达的一等阈值 |
| 可审计 | ✅ 草拟项都带 file:line 证据；基线是 policy 声明而非散落的逐行让步；accept remove 显式记录撤销 |
| 可度量 | ✅ scan 报告 distinct/uncovered 计数；本仓 13 imports / 0 uncovered（与 go.mod 30 项一致） |
| 可回溯 | ✅ 引入/覆盖判定走与 checker 相同的 `Matches` 边界语义，草拟结论可复算 |
| 可观察 | ✅ `deps scan -format json` 供 CI/agent 消费；未覆盖项直接是 dependencies.allow 草案 |

## 遗留

- deps scan 目前一次成型（无 `--fix` 自动写入 policy.yaml）；草拟项仍需人工审阅后落盘。
- Rust crate 归约只处理首段（`a::b` → `a`），dev-dependencies 未与主依赖区分。
- 测试基线目前只覆盖 metric 阈值；规则级（如 todo-comment）测试豁免仍走各自规则开关。
