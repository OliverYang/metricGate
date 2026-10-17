# 迭代 25 · store 保留策略（D4：2.4 GB 无上限的历史库）

日期：2026-10-16

编号说明：本文占用 `iter-25`，因为调研文档 §7 排期表里的 `iter-21` / `iter-22` / `iter-23` 三个序号已被**仍未开工**的工作占着。本文对应的不是一行计划，而是 **`iter-19（短）` 那行里的剩余项 D4**（表里与 S1 的 pre-commit 改根、`toolVersion` ldflags 注入并列在同一格）。
见 [产品演进方向调研 §7](../research/product-evolution-directions-2026-10.md)。

上游：`docs/research/product-evolution-directions-2026-10.md` §4 D4 与 §7「iter-19（短）」剩余行
前置：无（研究文档 §4 D4 明确「与 S1 无依赖关系」，可并行）

## §0 一句话目标

**给 `.metricgate/history.db` 一个上限，并让超限时的删除是可见、可预测、不伤棘轮的。**

现在它没有上限。**但要先纠正一个流传的说法**：研究文档 §4 D4 的第一轮实测说
「`check .` 从 1.36–1.88 s 退化到 78.03 s」，并在 §4 D4 正文里加了修订（`docs/research/product-evolution-directions-2026-10.md:125-127`）：

> **本节原先写的"先修 D4 再修 S1，否则 S1 的收益测不出来"已被第三轮实测反证**：store 只影响**绝对基线**（D4 仍要做），不是 S1 的前置条件，两件事可并行。

**本轮自己在同一台机器、同一份 2.3 GB 库上复测**（`./metricgate check .`，已构建二进制，非 `go run`）：

| 轮次 | real |
|---|---|
| 1 | 2.30 s |
| 2 | 1.53 s |
| 3 | 1.64 s |

⇒ **78 s 的退化在扇出修完后已经消失**（研究文档 :209 记录第四轮 scoped 口径下 `docs +1.65 s / cmd +2.05 s / internal +1.95 s`）。

**所以 D4 的真实理由是磁盘与可维护性，不是「它正在拖慢 check」**——把 2.4 GB 的、无上限的、74.5% 由 metric_snapshots 构成的单文件库留在仓库里，
会让备份、CI 缓存、fresh clone 验证逐轮变贵。本文按这个口径写，不重复已被反证的因果。

## §1 实测现状（2026-10-16，本仓）

| 指标 | 值 | 命令 |
|---|---|---|
| 库文件大小 | **2.39 GB** | `ls -lh .metricgate/history.db` |
| `page_count × page_size` | 611,721 × 4096 = 2389.5 MB | `pragma_page_count(), pragma_page_size()` |
| `freelist_count` | **0** | `pragma_freelist_count()` |
| runs | 1,376（`archived=0` **全部**） | `SELECT archived, COUNT(*) FROM runs GROUP BY archived` |
| findings | 1,189,973 | `COUNT(*) FROM findings` |
| metric_snapshots | **20,688,120** | `COUNT(*) FROM metric_snapshots` |
| runs 时间跨度 | 2026-09-24T03:47:36Z → 2026-10-16T13:41:35Z（22 天） | `MIN/MAX(started_at)` |
| 各表占用 | `metric_snapshots` **1781 MB**、`findings` 331 MB、`idx_snapshots_run` 262 MB、`idx_findings_run` 14 MB | `dbstat` |

**两个关键事实，它们决定修法：**

1. **`freelist_count = 0`** ⇒ 没有空闲页，`VACUUM` 省不出空间。空间全被活数据占着，
   必须真的 **DELETE 旧 run**。研究文档里「`VACUUM` 或归档导出」的并列写法要收窄：
   VACUUM 只在删除之后作为可选项（且 2.4GB 库的 VACUUM 会临时占用等量磁盘）。
2. **`metric_snapshots` 是 74.5% 的体积**（1781 / 2389 MB），不是 findings。
   ⇒ 保留策略的效果主要由 run 数决定，`archived` 标记不回收任何空间（实测 archived 全为 0）。

对比研究文档 :85 记录的历史值（1018 runs / 8.7M snapshots / 0.79 GB → 2.19 GB → 2.3 GB），
本轮的 1376 runs / 20.7M snapshots / 2.39 GB 说明**增长是线性的、没有拐点**：
每 run 约 15k snapshots、约 1.7 MB。保留策略的收益可以直接按这个比例外推。

## §2 现状的写入与读取路径（E2 定位）

**写入**：`internal/store/store.go:377 saveRun` 每个 run 一个事务，插 `runs` + `saveRunChildren`
（findings / metric_snapshots）。**没有任何删除路径**——`grep -rn 'VACUUM\|pruneRuns\|Retention'
internal/ cmd/` 为空。

**schema**（`internal/store/store.go:26-86`，注释写明「additive-only by design」）：

```sql
CREATE TABLE IF NOT EXISTS findings (run_id INTEGER NOT NULL REFERENCES runs(id), ...);
CREATE TABLE IF NOT EXISTS metric_snapshots (run_id INTEGER NOT NULL REFERENCES runs(id), ...);
CREATE INDEX IF NOT EXISTS idx_findings_run ON findings(run_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_run ON metric_snapshots(run_id);
```

**注意：没有 `PRAGMA foreign_keys = ON`**（`grep -n 'foreign_keys' internal/store/store.go` 无命中）
⇒ `REFERENCES` 只是文档，**删 `runs` 行不会级联删子表**。删除必须显式三条语句，否则会留下
孤儿 findings/snapshots 让库更大——这是本轮最容易写错的地方。

**读取方**（删除会影响谁，必须逐个确认口径）：

| 读取点 | 定位 | 窗口 |
|---|---|---|
| `MetricTrend(limit)` | `internal/store/metritrend.go:50` | 有 limit（cockpit 用 20） |
| `LatestRunsIn(w)` | `internal/store/read.go`；调用点 `internal/cockpit/gate_api.go:143/:412`、`internal/traceview/traceview.go:106` | 有 window |
| `issues_query.issueCandidates` | `internal/store/issues_query.go:273` | LIMIT 下推（iter-05 已修） |
| `RuleFrequencyScoped` | `internal/store/read.go:194` | `run_id IN (inner … LIMIT ?)` |
| `MetricTrendIn(w)` | `internal/store/metritrend.go:56` | 有 window |
| `RunByID` / `ReadSourceWindow` | `internal/store/read.go:406`、`internal/cockpit/source_api.go` | 单 run |

⇒ **所有读取路径都已经是窗口化的**，没有「读全库」的调用点。这使保留策略是安全的：
删掉最老的 run 只影响「历史趋势能回溯多久」，不影响任何当前判定。

## §3 契约：三条保留规则

### 3.1 配置形状（`.metricgate.yaml` 的 `store:` 段）

现状（`.metricgate.yaml:239-241`）：

```yaml
store:
  enabled: true
  path: ".metricgate/history.db"
```

新增（`internal/config/config.go:337 StoreConfig`）：

```yaml
store:
  enabled: true
  path: ".metricgate/history.db"
  retain:
    runs: 500          # 0 = unlimited（默认 0，向后兼容）
    days: 90           # 0 = unlimited
  prune_on_write: true # 默认 false：显式开启才在写库后清理
```

**默认必须是不清理**（`runs: 0` / `days: 0` / `prune_on_write: false`），否则升级即删数据。

### 3.2 删除的判定

保留集 = 满足**任一**条件的 run：

- 按 `runs: N` 保留最新 N 条（`ORDER BY started_at DESC, id DESC LIMIT N`）；
- 按 `days: D` 保留 `started_at >= now - D`；
- **`archived = 1` 的永不自动删除**（归档是人的显式决定，自动删它会违背归档的语义）；
- **`project_root` 各保留最新 1 条**——否则删完某个长期没跑的项目，其 `ProjectSummary.runTotal`
  从 1 变 0，项目卡片会从「仅运行」态掉到「空」态（迭代 3 的 D3-1 三态会被扰动）。
  这条是**本轮特有的约束**，来自迭代 3 的产品契约，不能省。

删除顺序（一个事务）：`DELETE FROM findings WHERE run_id IN (...)` →
`DELETE FROM metric_snapshots WHERE run_id IN (...)` → `DELETE FROM runs WHERE id IN (...)`。
**不能只删 runs。**

### 3.3 可见性（不可沉默）

- CLI：`metricgate store prune [-dry-run]` 打印「将删除 N 条 run（M 条 findings / K 条 snapshots），
  释放约 X MB」；`-dry-run` 是默认建议用法。
- 写库路径：`prune_on_write: true` 时，`saveRun` 成功后执行，**但只打印一行**（不阻断 check）。
- cockpit：`/api/dashboard` 增加 `storeRetention` 字段（`{runs, kept, prunedLastWrite}`），
  在概览页显式展示「保留最近 N 次运行」——用户必须能看出历史被裁剪过。

## §4 逐任务改法

| ID | 改动 | 落点 | 角色 |
|---|---|---|---|
| T22.1 | `StoreConfig.Retain` + 默认值 + 校验（负值拒绝） | `internal/config/config.go:337` | 后端 0.25d |
| T22.2 | `(*Store).Prune(w RetainPolicy) (PruneReport, error)`：四条规则 + 显式三表删除 + 单事务 | 新文件 `internal/store/retain.go` | 后端 0.5d |
| T22.3 | `metricgate store prune` 子命令 + `-dry-run` | 新文件 `cmd/metricgate/store_cmd.go` + `cmd/metricgate/main.go:41 commands` map | 后端 0.25d |
| T22.4 | `prune_on_write` 接到 `saveRun` 之后（默认关） | `internal/store/store.go:377` | 后端 0.25d |
| T22.5 | `/api/dashboard` 暴露 `storeRetention` + 概览页展示 | `internal/cockpit/dashboard.go` + `internal/cockpit/web/app.js` | 后端 0.25d + 前端 0.25d |

**合计 1.75 人日。**

## §5 本轮明确不做

- **`VACUUM`**：实测 `freelist_count = 0`，删之前它省不出空间；删之后可作独立后续
  （2.4GB 库 VACUUM 需等量临时磁盘 + 长事务，不该塞进 check 路径）。
- **归档导出**（把老 run 导出成可重新导入的格式）：研究文档提到的选项，但它是一个新的
  数据格式 + 迁移契约，与本轮「加上限」不是一件事。若需要，另开一轮。
- **自动按项目分库**：会破坏 `/api/dashboard` 的跨项目聚合（迭代 1/3 的 scope 契约）。
- **`explain .` 的性能**（研究文档实测它稳定在 1.59s，不是退化源）。

## §6 风险

| # | 风险 | 缓解 |
|---|---|---|
| R1 | **孤儿行**：漏删 findings/snapshots 会让库更大 | §3.2 的三表删除写进单测：prune 后 `SELECT COUNT(*) FROM findings WHERE run_id NOT IN (SELECT id FROM runs)` 必须为 0 |
| R2 | 默认开启清理导致升级即删数据 | 配置默认 `0 / 0 / false`；`prune_on_write` 显式 opt-in；`-dry-run` 是文档里的首选用法 |
| R3 | 删掉某项目唯一 run，项目卡片从「仅运行」掉到「空」 | §3.2 第四条：每 `project_root` 保底 1 条；加断言 |
| R4 | 与迭代 5/6 的 issues 窗口交互（`archived` 语义） | `archived = 1` 永不自动删；`scope=unassigned` 的 run 也纳入保底（按 `project_root IS NULL` 视为一个桶） |
| R5 | 2.4GB 库上 DELETE 的长事务锁库，与并发 cockpit 读冲突 | 分批删除（每批 50 run）+ 每批提交；实测单批耗时并记进 §8 |
| R6 | 并发会话正在写库 | 本轮全程用 `-dry-run` 验证；真删前先备份 `.metricgate/history.db`（2.4GB，先确认磁盘余量） |

## §7 验收判据

- **D22-1**：`metricgate store prune -dry-run` 在 2.39GB / 1376 runs 的真实库上给出确定数字，
  且 `-dry-run` **不修改库**（前后 `page_count` 与 `COUNT(*) FROM runs` 相同）。
- **D22-2**：执行 prune（`retain.runs: 200`）后：`COUNT(*) FROM runs` = 200 + 归档数 + 各项目保底数；
  **孤儿行 = 0**；库文件显著缩小（记实测值）。
- **D22-3**：prune 后 `check .` 与 `/api/issues` 的读数**不变**（当前判定不受历史裁剪影响）。
- **D22-4**：prune 后 `/api/dashboard` 的项目卡片三态（有调用/仅运行/空）与 prune 前**逐项一致**
  （R3 的直接验证）。
- **D22-5**：默认配置下（不写 `retain`）行为与今天**逐字节相同**——写库不触发任何删除。
- **N-1**：`go build ./...` + `go test ./...` 全绿；`bash scripts/cockpit-observe.sh fast` 全绿。
- **N-2**：`check_path internal/ -diff` 0 error。

## §8 复现与实测命令

```bash
# 体积与构成（本轮所有数字的来源）
ls -lh .metricgate/history.db
sqlite3 .metricgate/history.db "SELECT name, SUM(pgsize)/1024/1024 AS mb FROM dbstat GROUP BY name ORDER BY mb DESC LIMIT 12;"
sqlite3 .metricgate/history.db "SELECT page_count, freelist_count FROM pragma_page_count(), pragma_freelist_count();"
sqlite3 .metricgate/history.db "SELECT COUNT(*) FROM runs; SELECT COUNT(*) FROM findings; SELECT COUNT(*) FROM metric_snapshots;"
sqlite3 .metricgate/history.db "SELECT archived, COUNT(*) FROM runs GROUP BY archived;"

# 删除前的余量检查（2.4GB 备份）
df -h /Users/mac/Downloads

# 干跑
go run ./cmd/metricgate store prune -dry-run

# 孤儿行检查（R1 的判据）
sqlite3 .metricgate/history.db "SELECT COUNT(*) FROM findings WHERE run_id NOT IN (SELECT id FROM runs);"
sqlite3 .metricgate/history.db "SELECT COUNT(*) FROM metric_snapshots WHERE run_id NOT IN (SELECT id FROM runs);"
```

## §9 收口记录

本轮五个任务全部落地（配置形状、`Prune`、`store prune` 子命令、`prune_on_write`、cockpit 可见性），
§7 的判据逐条实测过。下面每条都带**命令、时刻（本地时区）、以及数字取自哪棵树**——
真库正在被同机其他会话读写，不带这三样的数字下一轮就没法复证。

### D22-1 干跑给确定数字，且不改库

真库 `.metricgate/history.db`（当时 1430 runs）。

```
$ go build -o /tmp/mg-iter25 ./cmd/metricgate
$ /tmp/mg-iter25 store prune -dry-run                      # 00:02:57，默认策略：无界
retention: unlimited (no bound set): nothing is ever removed
store: 1418 runs, 1193370 findings, 21198152 metric snapshots, 2.7 GiB
would remove 0 runs (0 findings / 0 snapshots), about 0 B
nothing to retire: all 1418 runs are kept by policy …

$ /tmp/mg-iter25 store prune -dry-run -runs 200            # 00:03:22
retention: keep newest 200 runs [-runs/-days]
would remove 1224 runs (1133376 findings / 17970354 snapshots), about 2.3 GiB
kept: 206 runs
dry run: database unchanged — page_count 710727 -> 710727, runs 1430 -> 1430
```

00:04:08 用只读连接在命令行复核：`page_count=710727|runs=1430|findings=1193594|snaps=21291820`
——与 dry-run 自己报的 before 逐字相同，所以「dry-run 不写」不是靠它自报，是外部量过的。
206 = 最新 200 条 + 6 个落在窗口之外的 `project_root` 桶各保底 1 条（真库实测 8 个 DISTINCT 桶，
其中 2 个与 200 窗口重叠）。

无界分支连写事务都不开（`Prune` 在 `Unlimited()` 时直接回 `Kept`），所以「默认配置不删」不是
靠判断兜住的，是那条路径上没有 `DELETE`。

VACUUM 之后（库 341 MB / 207 runs）在 01:29:27 又跑了一次同样的判据，因为「裁剪过一次之后
再干跑」才是下一轮操作者真正会遇到的状态：`page_count` 83231 → 83231、`runs` 207 → 207，
外部独立连接同时读到 83231 / 207（两次 `PRAGMA page_count` 之间涨的 2 页是并发写者，
不是 dry-run）。干跑数了将被删的三张表：`would remove 1 run (108 findings / 23628 snapshots)`。

### D22-2 真删（`retain.runs: 200`）

真删前先备份：

```
$ df -h /System/Volumes/Data        # 前 160Gi used / 34Gi avail 83% → 后 163Gi / 31Gi 85%
$ /usr/bin/sqlite3 .metricgate/history.db ".backup /tmp/history.pre-prune-iter25.db"
                                     # 00:20:14 → 00:20:27，13.82s
                                     # 产物 2926354432 B，PRAGMA integrity_check = ok，backup_runs=1438
$ /tmp/mg-iter25c store prune -config /tmp/iter25-runs200.yaml
                                     # 00:29:12 → 00:33:24，real 251.83s
removed 1236 runs (1135167 findings / 18161017 snapshots), about 2.3 GiB
kept: 206 runs
file: 2.7 GiB -> 2.7 GiB; 2.4 GiB is now free pages. VACUUM would return it (not run automatically).
```

00:33:49 之后逐项：**runs 206 / findings 58629 / metric_snapshots 3365010**；
两个孤儿查询（`findings`、`metric_snapshots` 的 `run_id NOT IN (SELECT id FROM runs)`）都是 **0**；
`archived` 仍为 0（本来就没有归档行）；`COUNT(DISTINCT COALESCE(NULLIF(project_root,''),'(unassigned)'))` = 8，
每个桶都还剩至少一条，**R3 没有掉态**。

**「库文件显著缩小」这条判据按原口径没满足，且不是实现的问题**：删完后 `page_count` 仍是 716076、
`file_size=2933047296` 逐字节不变，`freelist_count` 涨到 **629567**（≈2.58 GiB 空闲页）。
DELETE 只是把页放回 freelist，不缩短文件——§5 当年写「实测 freelist_count=0，删之前省不出空间」
在**删之前**是对的，本轮实测把它精确化为：**删之后 VACUUM 才有效**。

于是把 VACUUM 作为独立人工步骤量了一次（不进 `check`，也不进 `Prune`）：

```
真库  00:45:26 → 00:45:33  real 6.91s
      page_count 716076 → 83229   freelist 629563 → 0
      bytes 2933047296 → 340905984   （回收 88.4%）
      数据不变：207 runs / 58629 findings / 3365177 snapshots，孤儿 0/0，integrity ok
静态副本 00:44:0x 同量：real 6.26s，bytes 2926354432 → 341118976
```

所以 D22-2 的「显著缩小」= **2.93 GB → 341 MB（88.4%）**，但**这一步本轮没有自动化**：
`store prune` 的文案只告诉你有多少页空闲、VACUUM 不会自动跑。理由写在 `Vacuum()` 的注释里——
它需要等量的空闲磁盘和全程写锁，塞进任何自动出口都可能把一次正常 `check` 变成磁盘事故。

### D22-3 裁剪不影响当前判定

活库上直接 A/B 会被并发写者污染（00:28:36 前 / 00:34:53 后 `check .` 的 findings 从 657 变 659，
差集全落在 `internal/cockpit/web/app.js` 的行号漂移和并发新增的 `web_contract_test.go` 上，
mtime 分别是 00:36:35 与 00:38:10，都晚于前一次读数）。所以判据改成**静态副本 A/B**——
`cp -c` 两份 APFS clone，同一二进制、同一棵树，唯一变量是 prune：

```
副本 prune：00:38:29 → 00:41:04  removed 1232 runs (1134830 findings / 18089720 snapshots)，kept 206
/api/issues：两份 22942 B，/usr/bin/cmp 通过（逐字节相同）
```

`check .` 侧：files 355 / errors 105 / ignored 1 / verdict.passed=false 前后相同；同一棵树上
连跑两次（00:47:36）输出逐字节相同（`cmp` 通过），所以变化不是抖动，是那两个被并发改写的文件。
结论：**当前判定不受历史裁剪影响**，且 `/api/issues` 的窗口是「最近 60 次运行」，保留 206 条时
根本碰不到——这一点静态副本比活库证明得更干净。

### D22-4 项目卡片三态逐项一致

静态副本 A/B 的 `/api/dashboard`：7 张卡的三态（有调用 / 仅运行 / 空）**逐项一致**，
`issueOpen` 全部不变；只有 `runTotal`/`listed` 随窗口缩小（metricGate 817→190、mghead 2→1、
pydemo 2→1、vrepo 6→1），这正是「历史被裁了」应该看到的形状。

活库上 metricGate 的 `issueOpen` 从 19 变 0，一开始看像 R3 兑现了。静态副本两份都是 0，
说明这个 19 来自 60-run 窗口被并发新写入的 run 挤出，与 prune 无关——**如果没做静态副本，
这里就会把并发噪声记成 prune 的副作用**。

### D22-5 默认配置逐字节不变

真库 `.metricgate.yaml:239-241` 只有 `store: enabled / path`，**没有 retain / prune_on_write**。
`check internal/baseline -config .metricgate.yaml` → `run #1443 stored`，库里 runs 206→207，
没有任何删除发生（00:4x）。配置层另有 6 个测试钉住零值 = 无界（`internal/config/retain_test.go`），
写路径层有 `TestPruneOnWriteOffKeepsEveryRun` / `TestPruneOnWriteUnlimitedPolicyIsSilent`
钉住「没开就不跑，开了但没设界就闭嘴」。

### prune_on_write 端到端（真二进制、真 saveRun 路径）

```
$ /tmp/mg-iter25d check . -config /tmp/iter25-pow2.yaml     # 00:47:11 → 00:47:18
store prune after write: retired 181 runs, kept 26
run #1439 stored in /tmp/pow2.db
gate: PASSED (fail_on=error)
之后 runs=26 findings=37898 snaps=677430，孤儿 0/0
```

打印只有那一行，且排在 verdict 之前——`pruneAfterWrite` 失败时也只打一行
`store prune after write failed: …` 后返回，**不 `fail()`**：判定已经算完、run 已经提交，
库不肯缩小不能把一次 PASS 变成 error。

### cockpit 可见性（§3.3 的第三条）

`/api/dashboard` 实测（APFS 副本 `retain.runs: 20`，服务进程 cwd 有对应 `.metricgate.yaml`）：

```
剪之前 01:23:5x  {"runs":20,"days":0,"kept":26,"total":207,"prunedLastWrite":"","prunedRuns":0}
store prune     01:23:55 → 01:24:02  removed 181 runs (20791 findings / 2672987 snapshots)
剪之后 01:24:0x  {"runs":20,"days":0,"kept":26,"total":26,
                 "prunedLastWrite":"2026-10-16T17:24:01Z","prunedRuns":181}
```

概览页渲染（把**服务端真实载荷**喂给真实 `app.js`，不是 fixture）：

```
NOTE: 历史保留：保留最近 20 次运行（当前 26 次运行，上限内保留 26 次）。上次清理 2026-10-17 01:24:01，删除 181 条运行
IN_PAGE: true     HAS_ID: true     （id="storeRetention"）
```

`prunedLastWrite` 从**库里**读（`store_meta` 表），不是进程内变量：执行剪枝的通常是一个已经退出的
`check` 进程，cockpit 若在内存里记这个数字就永远是 0。记录与 DELETE 同事务写，所以回滚不会留下
「剪过」的假声明；`report.Runs == 0` 的早退分支**不写记录**（否则一次 no-op 会抹掉唯一的历史证据）。

请求路径只跑便宜的查询：保留集计数在 1438-run 的备份库上实测 **0.031s**（走 `idx_runs_started`），
而 `Stats()` 那种数 `findings`/`metric_snapshots` 的三表 COUNT 是 **7.111s**（21M 行全表扫）——
后者绝不进 `/api/dashboard`，所以 `kept/total` 是数 run，不是数被删的行。

### 收口观测（整批做完后跑一次，AGENTS.md 口径）

| 时刻 | 命令 | 结果 |
|---|---|---|
| 01:25:04 | `bash scripts/cockpit-observe.sh quick` | node --check ok；`render_all: 9 views x 3 scopes x 2 modes (+4 issue tabs) and 7 router round trips ok`；`ok metricgate/internal/cockpit 0.926s` |
| 01:36:44 | `go build ./...` | exit 0 |
| 01:36:48 | `go vet ./...`（整树） | **exit 0，输出 0 字节** |
| 01:36:59 → 01:37:05 | `go test ./internal/store/... ./internal/cockpit/... ./internal/config/... ./cmd/metricgate/... -count=1` | ok 1.324 / 5.545 / 1.419 / 5.248s，四个包全过 |
| 01:37:21 → 01:37:26 | `bash scripts/cockpit-observe.sh fast` | **`gate: PASSED (fail_on=error)`** 原文；`summary: 0 errors, 17 warnings, 0 notes, 445 metric snapshots`；`run #1468 stored` |

本轮 21 个改动/新增文件逐文件 `check_path`，**整口径与棘轮口径的 errors 都是 0**：
`meta.go 0/0`、`stats.go 0/0`、`store_cmd.go 0/0`、`store_render.go 0/0`、`store_cmd_test.go 0/0`、
`prune_on_write_test.go 0/0`、`retain_test.go 整 1 warn / 棘轮 0`、`dashboard.go 整 10 / 棘轮 3（hidden 7）`、
`app.js 整 16 / 棘轮 10（hidden 6）`、`config.go 整 5 / 棘轮 4`。
两个棘轮口径下仍报出的 warning 是我这两次改动引入的，已认：
`internal/cockpit/retention_api_test.go` 1 条（`seedRetentionRuns` 的 params/fan-out 类），
`internal/store/retain.go` 1 条既存、`retain_test.go` 1 条既存。

整树 `check_path(".")` 在 01:38:34：**files 358 / errors 105 / warnings 562 / findings 667，verdict FAIL**。
105 条 error 的归属用目录聚合，一条都不在跟踪源码里：

```
.trellis/  80 条      .pi/  25 条
ERRORS_IN_MY_TOUCHED_FILES = 0
NON_DOT_DIR_ERRORS = (空)
```

这两个目录都是工具自带目录、且都被 ignore：`git check-ignore -v` 命中 `.gitignore:6: .pi/` 与
`/Users/mac/.gitignore:11: .trellis/`，`git ls-files .trellis .pi | wc -l` = **0**（一个都没跟踪）。
对照 iter-20 收口时的整树口径（9 errors / 426 warnings / 322 files），差额就是这 29 个
新出现的 `.py`/`.ts` 文件——它们不属于本轮，也不是本轮能修的（改 `.metricgate.yaml:21` 的 `exclude`
会把整个仓库的闸门口径改掉，那是另一个决定）。**遗留第 6 条记这件事**：本仓 `check .` 现在
不可能 PASS，而这一点没人写在闸门配置旁边，操作者会以为是自己的代码坏了。

### 顺带证伪的一件事：「store 膨胀污染 `check .` 的读数」

D4 立项的理由是调研里那句「第一轮 `check .` 1.36–1.88 s，第二轮同一命令 78.03 s ⇒ 退化发生在
规则执行 + 写库这一侧」（[调研 §D4](../research/product-evolution-directions-2026-10.md)）。
既然本轮手里正好同时拥有 2.93 GB（剪前备份，APFS 副本）与 341 MB（剪后真库）两份库，
就用同一二进制（`/tmp/mg-iter25d`）、同一棵树、交替跑，把顺序噪声抵掉：

```
BIG-2.93GB 2.04s      SMALL-341MB 1.60s
BIG-2.93GB 1.40s      SMALL-341MB 1.40s
BIG-2.93GB 1.62s      SMALL-341MB 1.50s
```

两份的中位数差 0.12s，小于单份内部 0.6s 的极差。**当前代码与当前机器上，库大小对 `check .` 的
耗时没有可测影响**，78.03 s 那个读数无法复现。所以 D4 的成立理由要改口径：它是**磁盘占用缺陷**
（`.metricgate/` 2.3 GB→ 现 341 MB）和**无上限即无界增长**的运维缺陷，
不是「污染其他度量」的读数缺陷。这句话记在这里，是为了下一轮不要再拿 78 s 给保留策略排序。

原始读数是当时量到的，不撤销；本轮只能声明它现在量不出来。

### 本轮明确没做

| 项 | 状态 | 原因 |
|---|---|---|
| VACUUM 自动化 | 未做，只加了 `Store.Vacuum()` + 文案提示 | 需等量空闲磁盘 + 全程写锁；§5 的判断在「删之前」成立，本轮实测把「删之后有效」写在这里 |
| 归档导出 | 未做 | 是新数据格式 + 迁移契约，另开一轮 |
| 按项目分库 | 未做 | 会破坏 `/api/dashboard` 的跨项目聚合 |
| `explain .` 性能 | 未做 | 与保留无关 |
| 清理历史序列 | 未做 | `store_meta` 只有一个 `last_prune` 键，只答「上次剪了什么」，不保留历次 |

### 遗留（下一轮的输入）

1. **单事务真删在 21M 行库上是 251.83s**。用户裁决取了单事务（§6 R5 的分批方案否掉了），
   本轮没有断言这期间并发读写者的体验（`SQLITE_BUSY` 在本轮其他步骤出现过，需重试）。
   如果下一次真删要面对更大的库，需要的是一条可观测的锁等待指标，不是把事务拆小。
2. **`Retention` 的 days 界靠 `started_at` 文本比较**。成立的前提是 `saveRun` 写的是定宽 20 字符
   RFC3339 UTC（`store.go:414`）。改成别的格式会静默失效，值得在写入侧钉一条测试。
3. **从子目录跑 `store prune` 会静默降级到内置默认策略**。机制是
   `internal/configpath/configpath.go:98` 的 `if dir == stop { break }` 配合 `stopDir()`
   在非 git 目录返回自身——iter-20 §九 遗留第 7 条记过同一个坑。本轮只在概览上用
   `policyError` 让读策略失败可见，没有修发现口径（修它影响所有命令）。
4. **概览的保留说明是库级事实**：`ProjectSummary` 没有 `storeRetention`，项目 scope 下显示的仍是
   同一份策略与同一个库计数。前端在 `retentionNote` 上方按惯例写了 L8 注释；真要按项目显示
   需要后端加字段，本轮不造数据。
5. **`store prune` 的 `-dry-run` 在 2.9 GB 库上要 62.6s**，因为它数了 `findings` 与
   `metric_snapshots`。概览用的那条便宜查询只能给 run 数；若以后要让干跑变快，就得接受
   「干跑只报 run 数、不报将被删的行数」，而 §3.3 明确要求后者可见——这是一个口径决定，不是性能优化。
6. **整树 `check .` 现在必然 FAIL，而原因不在闸门配置旁边**。01:38:34 实测 files 358 /
   errors 105，105 条全在 `.trellis/`（80）与 `.pi/`（25）这两个**已被 ignore、且一个都没被 git
   跟踪**的工具目录里（`git ls-files .trellis .pi | wc -l` = 0；ignore 命中 `.gitignore:6: .pi/`
   与 `/Users/mac/.gitignore:11: .trellis/`）。跟踪源码里的 error 是 **0**。
   `.metricgate.yaml:21` 的 `exclude` 列了 `.git vendor node_modules testdata .data third_party shots-iter2`
   但没有 `.pi` / `.trellis`。把这两个加进去会改掉**整个仓库的闸门口径**（pre-commit 的 `-diff`
   与 `check_path` 都吃这一条），属于另一个决定，本轮只记录不动。
   记在这里的理由是：操作者看到 105 条 error 的第一反应是「我写的代码坏了」，而真实答案是
   「工具目录被当成源码扫了」。
