# 迭代 16 · 配置发现链与 Cockpit 策略读写（把「用哪个配置」变成可见可改的一件事）

日期：2026-10-13（延续 2026-10-12 的用户需求）

## 为什么做这个

用户原话：

> 扫描的配置文件应该用默认的兜底，然后它应该在项目下面去查一个默认的配置，最好是一个点开头的隐藏文件。像 eslint 那种逐渐退化的配置文件检查。这个配置文件应该在 UI 界面里面是可查、可解析、可修改的。

在那之前 MetricGate 的配置面有三个已经付费却被丢掉的成本：

| 已付的成本 | 被丢掉的部分 |
| --- | --- |
| 每个动词都有 `-config` 旗标（21 处） | 默认值全是 `"policy.yaml"`，相对**进程 cwd** 而非 `-root` 参数；`metricgate check /other/repo` 会拿当前目录的策略去判别的仓库 |
| `internal/config` 已实现 extends 继承、显式键溯源（`ExplicitKeys`/`ValueSource`） | 没有任何界面展示「这个阈值从哪来」 |
| Cockpit 已经是一个完整的本地审计界面 | 只有 `/api/dashboard` 与 `/api/runs/archive`，策略文件是唯一需要手改的输入 |

配置是所有其他动词的公共输入，它不可见意味着每次阈值争议都要靠猜。本次把三件事一次做完：**就近发现**（eslint 式退化）、**可查询可解析**（发现链 + 分节 + 生效键与出处）、**可修改**（同源校验 + 原子写）。

## 交付物

| 交付 | 位置 | 说明 |
| --- | --- | --- |
| 发现链 | `internal/configpath/configpath.go` | `.metricgate.yaml` → `.metricgate/config.yaml` → `policy.yaml`，逐级上溯命中即停，停在 Git 根 |
| 发现链测试 | `internal/configpath/configpath_test.go` | 10 个用例：优先隐藏文件 / 回退旧名 / 上溯 / 就近优先 / 显式优先 / 无效路径 / 文件作根 / 停在 Git 根 / 报告尝试过谁 |
| CLI 接线 | `cmd/metricgate/main.go` `loadPolicyFor(path, root)` / `policyFor` | 21 处 `-config` 默认值 `"policy.yaml"` → `""`；17 个动词改走发现链 |
| 静默加载 | `loadPolicyQuietFor` | `accept add` 判定严重级用：策略解析失败必须报错，不能静默变成默认值 |
| MCP 接线 | `cmd/metricgate-mcp/main.go` | 启动时的 `loadPolicy` 同样走发现链 |
| 策略读 API | `internal/cockpit/policy_api.go` | GET：路径/名称/根/停点/候选链/尝试记录 + 原文 + 分节行号 + extends + 生效键与出处 |
| 策略写 API | `internal/cockpit/policy_write.go` | PUT：同源校验 → 体积上限 → 未知字段拒绝 → 空内容拒绝 → 只写发现链上的文件 → yaml 校验 → 临时文件 + rename 原子替换 |
| 读写测试 | `internal/cockpit/policy_api_test.go` | 12 个用例（见下） |
| Cockpit 配置视图 | `internal/cockpit/web/index.html` + `app.js` + `style.css` | 「扫描配置」导航项、发现链可视化、分节跳转、YAML 编辑器、生效键表 |
| 运行视图措辞 | `index.html` `runsScopeNote` + `app.js` | 修掉「历史运行尚未记录项目身份」造成的「一条都没有」误读 |

## 关键设计决策（含理由）

**1. 三个候选名，就近停止，不自动合并祖先。** 用户明确选的策略。`extends` 仍然是显式的组合手段——自动合并会让「这个阈值到底谁说了算」变成一场考古，而退化链只回答「用哪个文件」这一个问题。

**2. 上溯必须停在 Git 工作树根。** 否则 `~/work/` 或 `/tmp` 下一个没人认领的 `policy.yaml` 会成为全局默认配置，等于把个人习惯静默推到所有仓库上。非 Git 目录则只搜起始目录（`stopDir` 退化为 `start`）。

**3. 写只能写发现链上的文件。** UI 想另存新文件必须先去创建它。理由：一个命令会读的文件集合，和一个界面会写的文件集合，必须是同一个集合；否则「界面显示 A、命令读 B」这种分裂会出现，而且没有任何报错。

**4. 写路径用「同源 + 原子替换」而不是任何形式的合并。** `os.CreateTemp` 落在目标同目录（保证 rename 不跨文件系统）→ `Chmod 0644` → `Close` → 旧内容存 `<target>.bak`(0600) → `os.Rename` → `defer os.Remove` 清残留。保存是给人看的 diff，不是工具替人做的合并。

**5. GET 必须带原文。** 解析再序列化会丢注释，而注释是策略文件里唯一能解释「为什么是这个阈值」的地方。分节行号、生效键、extends 都是**附加**信息，都不替代原文。

**6. 生效键必须带出处。** 这是 `internal/config` 已有的 `ExplicitKeys`/`ValueSource` 能力，之前从未在任何界面暴露过。没有出处的阈值无法讨论，也无法在踩坑时判断该改哪里。

**7. 解析失败进 `view.Error`，不假装空配置。** 一个语法坏掉的策略显示成「什么都没配」和「这个能力不存在」是同一类谎言。

## 实测证据

**A. 发现链退化顺序**（`/tmp/cfgdemo`，git 仓 + `svc/a.go` 7 复杂度）：

| 场景 | 结果 |
| --- | --- |
| 无任何配置文件 | `note: no policy file found; using built-in defaults`，0 errors |
| 写 `.metricgate.yaml`（cyclomatic warn3/err5） | 1 error 命中 |
| 再加 `policy.yaml`（warn1/err2） | 仍 1 error（error 5）——隐藏文件赢 |
| `-config policy.yaml` 显式指定 | error 2 |
| root=子目录 `svc`（内含 warn1/err100） | 0 errors |
| root=仓库根 | error 5——就近优先 |
| 从子目录 cwd 起 CLI / MCP | 均正确拿到仓库根策略 |

**B. Cockpit 读写闭环**（`/tmp/cfgui`，真启服务 + curl）：

```
GET  /api/policy  → found:true name:.metricgate.yaml
                    candidates:[.metricgate.yaml .metricgate/config.yaml policy.yaml]
                    tried:[…/.metricgate.yaml, …/.metricgate/config.yaml]
                    sections:[{version,1},{gate,2},{languages,4}]
                    keys:9 条，全部 source:"this policy"
PUT  → 200 {"bytes":115,"saved":"…/.metricgate.yaml","status":"ok"}
     落盘内容与提交一致；同目录生成 .metricgate.yaml.bak
紧跟其后的 check  → 阈值已变（warn1/err9 生效）
```

仓库自身（`/Users/mac/Downloads/metricGate`）实测：`found:true name:policy.yaml path:…/policy.yaml keys:115 sections:[version:3, gate:5, exclude:8, languages:10, architecture:114, store:183]`，即发现链在 96+ 文件未提交的真实工作树上稳定工作。

**C. 拦截的保存请求**（12 测试全部覆盖）：不可解析文档（400，文件未被改动）、路径不在发现链上（409，不创建文件）、根本没有发现文件（409，不创建文件）、空内容（400）、跨源请求（403）、DELETE（405 + Allow 头）、无临时文件残留。

## 单测

| 测试 | 断言 |
| --- | --- |
| `TestDiscover*`（10 个，`internal/configpath`） | 退化顺序、就近优先、显式优先、文件作根、停在 Git 根、报告尝试记录 |
| `TestHandlerServesTheDiscoveredPolicy` | found/name/content 原文回传/candidates |
| `TestPolicyViewReportsSectionsWithLines` | `languages` 分节存在且行号 ≥1 |
| `TestPolicyViewCarriesProvenanceForEveryEffectiveKey` | 每个键都有非空 source |
| `TestPolicyViewWithoutAnyFileIsHonest` | found=false、path=""、Tried 非空 |
| `TestPolicySaveReplacesTheDiscoveredFile` | 200、落盘、GET 反映保存 |
| `TestPolicySaveRefusesAnUnparseableDocument` | 400 且文件字节不变 |
| `TestPolicySaveRefusesANewPath` | 409 且不创建文件 |
| `TestPolicySaveWithoutADiscoveredFileIsARefusal` | 409 且不创建文件 |
| `TestPolicySaveRejectsGETAndViceVersa` | 405 + `Allow: GET, PUT` |
| `TestPolicySaveIsRejectedCrossOrigin` | 403 |
| `TestPolicySaveLeavesNoTemporaryFileBehind` | 无 `.tmp` 残留 |

## 五性核对

| 维度 | 状态 | 证据 |
| --- | --- | --- |
| 可介入 | ✅ | PUT 落盘后紧跟的 `check` 立即按新阈值判定（`fail_on: warning` → 14 findings；改回 `error` → 恢复） |
| 可审计 | ✅ | 每个生效键带出处（this policy / builtin / preset:x / extends 路径）；`policyView.Tried` 记录为什么没找到；save 只在已发现文件上生效 |
| 可度量 | ✅ | 12 个 API 测试 + 10 个发现链测试；A 表 8 种退化场景逐一实测 |
| 可回溯 | ✅ | 写路径留 `.bak`（0600）；原文不回写，注释保留；每个决策在代码 doc comment 里留了理由 |
| 可观察 | ✅ | Cockpit「扫描配置」视图：候选链（◉生效/○未命中）、分节跳转、脏标记、保存结果反馈 |

## 阈值纪律

自检抓到 3 次，全部当场拆函数，未用 accept ledger 掩盖：

| 位置 | 抓到 | 处理 |
| --- | --- | --- |
| `policy_api.go` `servePolicyView` | fan-out 12 | 拆出 `policyViewOf` |
| `policy_write.go` `writePolicy` | fan-out 17 | 拆 `readPolicyBody`/`decodePolicyContent`/`resolvePolicyTarget`/`savePolicy` |
| `policy_write.go` `writePolicy` | fan-out 11（仍超） | 再拆 `savePolicy`，终值 0 |

MCP `check_code` 终态：`policy_api.go` / `policy_write.go` / `policy_api_test.go` 全部 PASS 0/0。

## 验证

- `gofmt -l internal/ cmd/` 空
- `go build ./...` OK
- `go test ./internal/cockpit/ ./internal/configpath/ ./internal/config/ ./cmd/... -count=1` 全 ok（cockpit 10.6s）
- `node --check < internal/cockpit/web/app.js` OK
- `git diff --check` 干净

## 遗留

1. **UI 只读路径尚未在真实浏览器里点过。** 本迭代用 HTTP 级验证了 GET/PUT 与随后的 `check`，JS 侧只做了语法校验与元素 id 对齐；交互（分节跳转、脏标记、保存后刷新）按代码走查确认，未做浏览器回归。
2. **`.bak` 只有一份，且每次保存覆盖。** ~~每次保存追加一个同内容备份，没有轮转或上限~~（2026-10-17 实测更正：`writePolicyAtomic` 写的是固定名 `target+".bak"`，`os.WriteFile` 是覆盖语义。连续保存 v1/v2/v3 后目录里只有 `.metricgate.yaml` + `.metricgate.yaml.bak`，bak 内容是 **v2**（上一版），不是 v1 的累积。）所以真实缺口不是「没有轮转上限」，而是**只能回退一步**：连续两次改坏配置，第一次的原文就找不回来了。当前判断不变——配置改动频率低、文件小；但要修的话修的是「留 N 份」而不是「清理累积」。见 [iter-29 评估](../evaluations/evaluation-iter29-remediation.md) §5。
3. **MCP 侧没有策略读写工具。** MCP 仍是只读的（符合「MCP 不写仓库」的既有约束），策略编辑走 Cockpit 或编辑器。
4. **发现链没有暴露成 CLI 查询动词。** 「现在用的是哪个配置」目前只能从 `check` 的 stderr note 或 Cockpit 看出来；`policyFor()` 已留好接口，是否要做成 `metricgate policy show` 待定。
5. **`-config` 显式路径仍不查存在性。** 这是刻意保留（显式指定一个不存在的文件应当报错而不是静默退化），但意味着 `-config typo.yaml` 的错误信息来自 `config.LoadFile`，而不是来自发现链的友好提示。
