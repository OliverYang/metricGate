# Cockpit 单文件形态技术方案（复刻 SonarQube 交互的落地方式）

**状态**：P0/P1 主要部分已落地（1896 行，闸门 0 error）。父文档：[`cockpit-sonarqube-parity.md`](cockpit-sonarqube-parity.md)（概念映射、逐页方案、P0/P1/P2 分批）。本文回答一件事：**在 `web/app.js` 单文件里，怎么把 SonarQube 那套交互装进去而不腐烂。**

决策记录：D4 定为 **C′ = 单文件 + 内建结构**（原 C 的升级）。不是"继续现状"，也不是引构建链。

## 0. 单文件换来什么、代价是什么

| | 说明 |
|---|---|
| **换来** | 零构建（无 Node/npm 进发布链）、单二进制、`curl` 下来的二进制自带 UI、无版本漂移。（embed 清单见 §15.3 第 3 条：从 `web/*` 改成了显式四条 `//go:embed`，形态不变，发布物里不再有 dev harness。） |
| **代价 1** | 无模块边界 → 依赖纪律只能靠**约定 + 闸门规则**，不是靠 bundler |
| **代价 2** | `file-length` 必然超阈值（见 §1，必须显式记账） |
| **代价 3** | 无 JSX / 无 VDOM → 渲染靠字符串模板，焦点/滚动/性能要自己处理（§4） |
| **代价 4** | 3000+ 行单文件的可读性必须被机器守护，否则第三次迭代就没人敢改（§2、§8） |

实测确认：**单文件不是 embed 逼出来的**。`//go:embed web/*` 递归包含子目录（`web/sub/deep/c.js` 可嵌入；`.` / `_` 前缀文件被排除）。所以这个形态是产品主张（零构建/零网络），要按主张的代价诚实记账。

## 1. 先解决闸门冲突（否则第一天就自相矛盾）

现状：`metricgate check internal/cockpit/web/app.js` → `file-length is 684 (threshold warning 400)` + 8 条 complexity/fan-out warning，0 error。撞 error 线（800）只是再加一页的量。

三条纪律，一条都不能省：

1. **在 `.metricgate.yaml` 加 `javascript` 段并写明理由**，不靠 `exclude` 绕过（`exclude` 是按目录名匹配，会把所有叫 `web` 的目录一起放过，dogfood 就断了）：

```yaml
  javascript:
    metrics:
      # cockpit 前端刻意保持单文件形态（零构建 + 零网络 + 单二进制的代价，
      # 见 docs/design/cockpit-single-file-tech.md）。长度阈值在此放宽，
      # 但用 §7 的结构性 DSL 规则补回可读性约束——放宽的是长度，不是纪律。
      file-length:    { warn: 2000, error: 4000 }
      function-length: { warn: 60, error: 100 }   # 单文件里函数必须更小
      params-count:    { warn: 4, error: 6 }      # 组件函数参数收紧
```

2. **长度预算写进文档并设上限**：4000 行是终止线。到线就必须砍页面（先砍 P2-2 可编辑仪表盘），不是先提阈值。
3. **承认 `-diff` 抓不到**：`filter.go:24` 只在整文件为新时保留 file-length，所以这条只在**定时全量扫描**里出现。方案里不假装 pre-commit 会拦住它。

## 2. 文件内部结构：固定 8 段 + TOC 自证

单文件的组织单位不是目录，是**段落 + 依赖方向**。顺序即分层，只允许向下依赖（由 §8 的 Go 测试断言顺序，不可交叉引用向上）：

```text
//! S1 · TOKENS      常量、severity/评级映射、MSG 文案表（无行为）
//! S2 · UTILS       escape / html 模板 / 格式化 / 纯函数      不碰 DOM
//! S3 · STORE       state + selectors + subscribe（唯一写入口 setState）
//! S4 · API         fetch + 并发去重 + 缓存 + 错误归一化
//! S5 · ROUTER      hash 路由 ⇄ state 序列化
//! S6 · COMPONENTS  data → HTML string，纯函数，一屏一个
//! S7 · VIEWS       每页一个对象 {id,title,render,bind,keymap}
//! S8 · BOOT        事件委托、键盘、dialog、mount            ← 唯一有副作用的段
```

`//#region` 不用（非标准）；用 `//! S<n> · NAME` 行首标记，Go 测试可直接解析。文件头部放一份 TOC 表（每段一行说明），**TOC 与实际标记不一致 = 测试失败**，这是防"文档腐烂"的机器手段。

关键约束（§7 有对应闸门规则）：

- S2/S6 必须是**纯函数**：入参 data、出参 string。这是能在 Node 里单测的前提（§8.2）。
- S8 之外**禁止 `addEventListener`**。21 处散监听是当前形态最大的腐烂源；单文件扩容后必须收敛成一张动作表（§3）。
- 段内按字母/功能排序不做强制，但函数**必须**带一行 `// 说明` 或前缀 banner，否则 §7 的注释规则会拦。

## 3. 事件委托：把 21 处监听换成 1 处 + 一张表

现状每个控件各自 `addEventListener`，加页面时接线量线性增长。单文件里换成**根节点委托 + 动作注册表**，接线点恒定为 1：

```js
//! S8 · BOOT
const ACTIONS = {
  'issue.select':   (el) => store.set({ openIssue: el.dataset.key }),
  'issue.status':   (el) => api.patchIssue(el.dataset.key, el.value),
  'facet.toggle':   (el) => store.toggleList('facets', el.dataset.facet, el.dataset.value),
  'sort.set':       (el) => store.set({ sort: el.value }),
  'bulk.apply':     ()   => bulkApply(),
  'nav.view':       (el) => router.go(el.dataset.href),
};

document.addEventListener('click', (e) => {
  const el = e.target.closest('[data-action]');
  if (!el) return;
  const fn = ACTIONS[el.dataset.action];
  if (!fn) return fail(`未注册的动作: ${el.dataset.action}`);
  fn(el, e);
});
// change/input 同理各一条；键盘一条（§5）
```

组件端只写 `data-action="issue.status" data-key="…"`，不持有闭包。**动作表就是单文件的"模块边界清单"**——加功能先看这张表能加几行，一眼看出膨胀。

## 4. 渲染：字符串模板 + 轻量 keyed diff + 焦点保全

SonarQube 的 Issues 页有 3 个动作是"整块 innerHTML 重写"必坏的：**滚动位置、输入焦点、行内下拉**。单文件不引 VDOM，但要补这三件事，约 120 行：

### 4.1 `html` 标签模板（把 escape 从"自觉"变成"默认"）

现状 20 处 `innerHTML` 各自手写 `escapeHTML(...)`，漏一处就是 bug（已顺带发现一个同类：`style.css:13` 用了 `var(--border)`，而 `:root` 从未定义它——让步账本卡片边框实际失效）。改为：

```js
//! S2 · UTILS
const raw = (s) => ({ __raw: s });                       // 已转义的片段
const frag = (v) => v == null || v === false ? ''
  : Array.isArray(v) ? raw(v.map(frag).join(''))
  : typeof v === 'object' && v.__raw !== undefined ? String(v.__raw)
  : escapeHTML(String(v));
const html = (strings, ...values) =>
  raw(strings.reduce((acc, s, i) => acc + s + (i < values.length ? frag(values[i]) : ''), ''));
```

插值默认转义，拼接用 `${html`…`}` 自动识别。迁移可以逐页做，新旧并存（`innerHTML` 右侧统一取 `. __raw`）。不选 `htm` + Preact：那要在单文件里塞进第三方 blob，而上面这 10 行就能拿到同样的安全性。

### 4.2 keyed list：只重写变化的行

Issue 列表可能上千条，整表重建会掉滚动条和 hover。做一个够用的 `renderList`：

```js
//! S2 · UTILS
// 按 data-key 复用 DOM 节点；只增删/替换差异行。不排序 DOM，顺序由 items 决定。
function renderList(container, items, keyFn, itemHTML) { /* ~55 行 */ }
```

规则：容器带 `data-key`，key 用 finding 指纹（与后端 `issue_states.fingerprint` 同源，父文档 §4.1），**这样 UI 复用键 == 状态追踪键 == 跨 run 指纹**，一物三用得对齐。

### 4.3 焦点/滚动保全

`renderList` 外层包一个 `preserve(container, fn)`：记录 `document.activeElement.id` + `container.scrollTop`，重渲染后还原。facet 面板里的搜索框必须靠这个活着。

### 4.4 列表虚拟窗口

超过 ~200 行不做全量 DOM：用 `start/size` 分页窗口 + "加载更多"，或固定行高 + 首尾哨兵。**不做**通用 virtualizer（单文件里维护成本高，后端分页已经把量压下来）。

## 5. 状态与 URL：把筛选条件放进 location，不进闭包

SonarQube 交互的灵魂是"筛选可分享、可后退、浏览器记住排序"。单文件里必须有显式的 `state ⇄ query`：

```js
//! S5 · ROUTER
const ROUTE = (s) => `#/p/${s.projectId}/${s.view}` +
  qs({ severity: s.facets.severity, status: s.facets.status, rule: s.facets.rule,
      sort: s.sort, period: s.period, q: s.q, page: s.page });

function parseHash(h) { /* → partial state */ }
window.addEventListener('hashchange', () => store.set(parseHash(location.hash), { fromRoute: true }));
```

- 只有**可分享状态**进 URL（视图、facet、排序、时间窗、页码、搜索词）。
- **UI 本地状态**（展开的 facet、hover、对话框开合）留在 store，不进 URL。
- "排序选择器记住上次"这类偏好走 `localStorage`，键名集中列在 S1，别散落。
- 时间窗（对应 New Code）用 git ref / 天数下拉，直接映射到已有的 `store.Window` 能力。

store 本体是单文件的"唯一真相"，约 60 行：

```js
//! S3 · STORE
let state = { /* 初始 */ }; const subs = new Set();
const store = {
  get: () => state,
  set(patch, opts) { state = { ...state, ...patch }; location.hash = ROUTE(state); subs.forEach(f => f(state, opts)); },
  toggleList(key, v) { /* facet 多值 */ },
  on: (f) => { subs.add(f); return () => subs.delete(f); },
};
```

## 6. 数据层：从"一次全量 `/api/dashboard`"到按视图拉取

现状一个 600KB 级 JSON 打天下，`app.js` 客户端全表过滤。复刻分诊交互必须换（父文档 §4.2 的端点），单文件侧配一个 ~120 行的 api：

- `api.get(path, params)`：URL 为缓存键；同键**并发去重**（复用 in-flight promise）；`AbortController` 取消过期请求（facet 连点时必需）；失败归一成 `{error, status}` 供 banner 显示。
- 保留 `/api/dashboard` 只做**落地页概览**，明细一律走 `/api/issues`，避免两套聚合路径（父文档 §8 风险项）。
- 写操作（状态流转、Bulk）**复用 Go 侧同一套校验口径**：`sameOrigin` + 严格 Content-Type + `MaxBytesReader` + `DisallowUnknownFields`，照 `decodeArchiveRequest`（`internal/cockpit/dashboard.go:338`）。前端只发最小载荷，不做"前端算完再存"。
- 轮询：只在可见视图轮询，`document.hidden` 时停（一行 `visibilitychange`）。

## 7. 用自家闸门守自家单文件（把纪律变成 finding）

`javascript` 已在 parser 支持列表（`parser.go:138`），tree-sitter JS 可用，**fan-out / cyclomatic / function-length 对 app.js 已经在报**（实测 9 条 warning）。所以单文件的结构约束能用 `custom_rules` DSL 落地，不需要新工具：

```yaml
  javascript:
    custom_rules:
      - id: web-no-scatter-listener
        files: ["internal/cockpit/web/app.js"]
        node: expression_statement
        match_regex: '\baddEventListener\s*\('
        exclude_regex: '^(window|document|root|container)\.addEventListener|^\s*//|visibilitychange|hashchange|keydown|input|change|click'
        severity: error
        reason: "单文件内事件接线必须收敛到 S8 的 ACTIONS 表；只有根节点与生命周期监听允许直接 addEventListener"
      - id: web-section-banner-required
        files: ["internal/cockpit/web/app.js"]
        node: function_declaration
        match_regex: '^function\s+[a-z]'
        exclude_regex: '^/\*|//!'   # 前一行有 banner/注释的放过
        severity: warning
        reason: "每个渲染函数前要有注释或段落标记，否则 3000 行单文件不可导航"
```

（这两条的具体正则要在实现时对 tree-sitter 的 node 类型实测校准，DSL 现有形态是"读整个声明"，`exclude_regex` 语义要写测试验证——按 AGENTS.md 三触点走，别先假定生效。）

再加三条内置阈值的收紧（§1）：`function-length` warn 60、`params-count` warn 4。含义很直白：**长度换结构，单文件的补偿性纪律必须比多文件更严，否则就是纯退化。**

## 8. 怎么测（无浏览器、无构建链）

分三层，成本从低到高：

1. **Go 静态断言（扩展现有测试，最便宜，收益最大）**
   `internal/cockpit/cockpit_test.go:198-213` 已经有"取 `/web/app.js` 断言含 `fetch(`"的先例。扩成：
   - `//! S<n>` 八段齐全、顺序正确、TOC 与标记一致；
   - `ACTIONS` 表的 key 与模板里出现的 `data-action="…"` 集合**双向一致**（这条能抓住整个单文件最容易烂的一类错：接了个不存在的动作）；
   - 每个 `data-view` / `id` 在 `index.html` 里存在（防 JS 摸空）；
   - `style.css` 里用到的每个 `var(--x)` 都在 `:root` 有定义（直接抓死 §4.1 说的 `--border` 这类错）；
   - 只允许一个 `addEventListener('click'`。
2. **纯函数单测（dev-only，不进发布链）**
   S2/S6 是纯函数，只要 S8 的 bootstrap 带守卫：

```js
const isBrowser = typeof window !== 'undefined' && document.getElementById('app-shell');
if (isBrowser) boot();
export { html, renderIssueCard, parseHash, ROUTE };   // Node 侧可 import
```

   于是 `node --test internal/cockpit/web/app.test.mjs` 能跑模板转义、`parseHash ⇄ ROUTE` 往返、评级计算。**注意**：`<script type="module">` + `export` 不影响 embed，也不引入构建步骤；Node 只是本地开发可选件，发布物仍是单二进制。这条要在文档里写死，免得被误读成"开始依赖 Node 构建"。
3. **服务端契约测试**
   `httptest` 打 `/api/issues` 的 facet 计数联动、状态 PATCH 的校验拒绝路径（沿用 `archiveAPI` 的测试写法）。视图正确性主要靠 1+2 兜，不追求浏览器端 E2E。

## 9. 页面装配（单文件里的 VIEWS 表）

```js
//! S7 · VIEWS
const VIEWS = {
  projects:  { title: '项目总览', render: vProjects,  keymap: KEYS_LIST },
  summary:   { title: '概览',     render: vSummary },          // Gate 横幅 + New/Overall + measure 卡
  issues:    { title: '问题',     render: vIssues,   keymap: KEYS_ISSUES },   // P0
  rules:     { title: '规则',     render: vRules },
  gate:      { title: '质量门禁', render: vGate },
  activity:  { title: '活动',     render: vActivity },
  code:      { title: '源码视图', render: vCode },             // 需 /api/source，父文档 §4.4 边界
};
```

> 落地差异（见 §14/§15）：`summary` 与 `overview` 已合并成一个 `overview`（两个概览页意味着同一个 URL 有两种解释、侧栏与 tab 栏各指其一）；`code` 不是独立视图，代码窗口在 issue 详情的「位置」tab 里 —— SonarQube 自己也是这么放的，独立一页只会让人以为可以浏览仓库，而 §4.4 的边界恰恰禁止这种联想。

键盘映射同样是一张表，且必须有**输入态抑制**（否则 SonarQube 自己也踩过的坑：在 facet 搜索框里打 `f` 触发状态流转）：

```js
const KEYS_ISSUES = { ArrowUp:'move-1', ArrowDown:'move+1', ArrowRight:'open',
  f:'cycle-status', a:'assign', m:'assign-me', i:'severity', c:'comment', t:'tags' };
// keydown 处理器首行：if (e.target.matches('input,textarea,select')) return;
```

## 10. 实施顺序（与父文档 P0/P1/P2 对齐）

| 步 | 内容 | 完成判据 |
|---|---|---|
| S-1 | §1 的 `.metricgate.yaml` `javascript` 段 + §7 两条 DSL 规则 + 其单测 | `check internal/cockpit/web` 无 file-length error；DSL 规则能被 `rules explain` 解释出来 |
| S-2 | §2 段落重排 + `html` 模板 + `ACTIONS` 委托改造（现有 683 行先结构化，不加功能） | 页面行为逐条不变；`go test ./internal/cockpit/` 覆盖 TOC/动作表/id 一致 |
| S-3 | §5 store + router + §6 api（in-flight 去重 + AbortController） | `parseHash ⇄ ROUTE` 往返单测通过；后退/分享链接可用 |
| S-4 | `issues` 视图（父文档 P0-1）：facet 栏 + keyed 列表 + 状态下拉 | 上千行不整表重绘；滚动/焦点不丢 |
| S-5 | `summary` 视图（P0-2）+ 评级 | Gate 横幅与 §4.3 评级公式一致 |
| S-6 | P1 各视图（详情四 tab、activity、rules/gate） | 每加一页只动 VIEWS + ACTIONS + 段落，不新增接线方式 |
| — | 每步收尾：`check_path` / `check . -diff`；`go build ./...` + `go test ./internal/cockpit/...` | AGENTS.md 约定 |

## 11. 明确不做（单文件形态下必砍）

- 通用虚拟列表、通用表单引擎、组件生命周期框架——都是"在单文件里重写半个框架"。
- CSS 框架化：只把 `style.css` 的 174 处硬编码色收进 `:root` token（父文档 D6 保留我们自己的深色板），不做 Tailwind 式原子类。
- 可编辑仪表盘（P2-2）：4000 行上限下第一个被牺牲的对象。
- 任何形式的打包/压缩：产物不可读会破坏 dogfood，体积 36 KB → 约 150 KB 在 loopback 场景无所谓。
- 前端埋点/远端错误上报：与零网络主张冲突。

## 12. 风险

- **动作表膨胀**：`ACTIONS` 会先变成"什么都能干"的杂物抽屉。约束：每个动作只允许 `store.set` 或 `api.*` 或 `router.go` 三类副作用之一，超出即拆视图（可用 DSL 规则粗查，但主要靠 review）。
- **段落顺序纪律靠测试，不靠自觉**：所以 §8.1 的 TOC/顺序断言是 S-2 的交付物，不是可选项。
- **`html` 模板迁移期新旧并存**：混用容易在旧代码里二次转义（双重 escape 会显示 `&amp;`）。约束：以视图为单位整块迁移，不在同一模板里混两种写法。
- **`file-length` error 阈值放到 4000 是本仓最大的一次阈值放宽**，必须是显式决策 + 有理由 + 有补偿规则，并在 `docs/claims.md` 的叙事里如实写（不能对外宣称"所有语言统一阈值"）。
- **Node 单测被误当构建依赖**：§8.2 的措辞要在 PR 描述与 README 里重复一次；发布链里不出现 npm。
- 3000 行后 git 冲突集中（单文件 = 同文件并发改）。缓解：按视图分支、`VIEWS`/`ACTIONS` 表项**追加到末尾**，减少同区冲突。

## 13. 待确认

1. §1 的阈值放宽（`file-length` warn 2000 / error 4000）与两条 DSL 补偿规则，你是否接受——这是单文件形态唯一需要动 `.metricgate.yaml` 语义的地方。
2. §8.2 是否引入 `<script type="module">` + `export`（dev-only Node 单测能力，不进发布链）。不接受的话，测试只剩 §8.1 静态断言一层，模板/路由逻辑的回归覆盖会明显变弱。
3. 起始顺序是否按 S-1 → S-2（先把现有 683 行结构化，**不加任何新功能**）——我倾向先做这两步，因为 S-3 以后的所有代码都要落在 S-2 的骨架上。


## 14. 实施结果（截至本次落地）

### 14.1 与方案的偏差（都是实测修正方案，不是走捷径）

| 方案里写的 | 实际落地 | 原因 |
|---|---|---|
| §7 两条 `custom_rules` DSL 规则补偿结构纪律 | **删掉了**，理由写进 `.metricgate.yaml` 的 `javascript` 段注释 | DSL 无位置概念：`web-no-scatter-listener` 对 S8 自己的三个 `addEventListener` 报 error，`web-no-import-outside-tokens` 对 S1 唯一的合法 import 报 error。两条都是"写来允许的那个点反而被抓"，只能删掉；对应契约在 Go 测试里用行号实现，能真正分清位置 |
| §4.2 `renderList` keyed diff | **未做** | Issues 列表服务端已分页（每页 50），且 `render` 已保住焦点与滚动。等列表本地超过一页再引入 |
| §5 把 `sort/severity/status/page` 写进 hash | **做了**，见表 `ROUTE_KEYS` | 从"每字段一个 if"改成一张表后，`encodeRoute`/`decodeRoute` 的 cyclomatic 从 18/16 降到 3。这是闸门逼出来的，也是最该这么改的一次 |
| §8.2 Node 单测 | **做了**：`render_all.mjs` + `TestRenderAllViews`，并补了 `smoke.mjs` + `TestCockpitRendersThePayloadsTheServerActuallySends` | 前者 9 视图 × 3 scope × 2 history mode + 4 个 issue tab + 路由往返；后者用真实 handler 的载荷驱动真实 fetch 层。fixture 与 app.js 由同一只手写，它只能证明自洽，证明不了与 Go 一致 |
| §8.3 "视图正确性主要靠 1+2 兜，不追求浏览器端 E2E" | **该判断是错的，已推翻（见 §15）** | 1+2 两层全绿的期间，前端在浏览器里一次都没有启动过（见 §15）。fixture 驱动的渲染测不出启动失败、测不出取数层与载荷的命名分歧、也测不出 CSS 类有没有人定义 |

### 14.2 落地清单

- **契约单一来源**：`internal/webspec`（Go 侧 `Build()` → `web/spec.json`，前端 `import spec`）+ 漂移守卫测试。前端不再硬编码任何枚举。
- **数据层**：`internal/store/issues.go`（指纹、状态机、原子写）、`issues_query.go`（分面/过滤/排序/分页）、`findings` 表加 `snippet_hash`/`fingerprint` 两列 + `issue_states` 表 + 两条迁移。
- **API**：`/api/issues`、`/api/issues/state`、`/api/gate`、`/api/measures`、`/api/source`、`/api/spec`（原有 3 个端点之外），全部沿用 loopback 守卫；写端点 sameOrigin + 严格 Content-Type + `MaxBytesReader` + `DisallowUnknownFields`。
- **前端**（8 段、2093 行）：`projects / overview / issues / gate / rules / activity / calls / runs / policy` 九个视图（`summary` 与 `overview` 合并），28 个动作、4 个 issue tab、详情 dialog、焦点与滚动保全、`writeInto` 唯一 DOM 写入点、`list()` 防 `[object Object]`。
- **测试**：`web_contract_test.go`（段序、`data-action` 与 `ACTIONS` 双向一致、监听集中化、单 DOM 写入、CSS 变量已定义、`list()` 未退化成 `.join("")`）+ `render_all.mjs` 全视图渲染 + Go 侧 API 契约测试。

### 14.3 预算记账

- 现状 2093 行 / error 线 4000。**函数级阈值比 Go/Java 更严**（`function-length` warn 40 error 60，`params-count` warn 4 error 6），这就是"长度换结构"的那一半交易。
- 到 4000 的第一动作是砍页面（候选：`dashboard` 式可编辑仪表盘），不是提阈值。

## 15. 第二轮实测：界面从未在浏览器里跑起来过

第一轮结束后补做的一轮排查，结论不体面但必须记下来：**§8 那三层测试当时全绿，而 cockpit 在浏览器里一次都没有成功启动过**。下面每一条都是真缺陷，不是风格问题，且每一条都在 §8 的覆盖范围之外。

| # | 缺陷 | 为什么测试没抓到 |
|---|---|---|
| 1 | `boot()` 守卫读 `getElementById("app-shell")`，而 `index.html` 只有 `class="app-shell"` —— 守卫恒假，整个前端不启动，页面是静态外壳 | 没有任何测试执行过 `boot()`；渲染测试直接调 `VIEWS[x].render(state)`，绕过了 S8 |
| 2 | `join()` 返回普通字符串，于是 `list()` 的产物在插值时被**二次转义** —— 页面显示源码文本而不是元素 | `render_all` 断言的是字符串内容，转义后的字符串依然"正确" |
| 3 | `fetchIssues` 写入 `issues.rows`，模板读 `s.issues.issues` —— Issues 页必然抛 `undefined` | fixture 的键是 `issues`，与模板一致；取数层没被任何测试调用 |
| 4 | 路由**从未把状态写回 URL**（`encodeRoute` 只在测试里被调用）—— 筛选不可分享、刷新丢状态、前进后退无效 | 路由测试只做 `decode → encode → decode` 往返，没测"生产代码是否调用 encode" |
| 5 | 只有 `click` 委托：`<select>` 在 click 时仍持有**旧值**，排序/流转/阶段/结论/trace/度量 6 个控件全部作用于错误的值；搜索框 click 时还没输入 | 委托测试数 `addEventListener` 的个数，不检查事件类型与控件是否匹配 |
| 6 | `issue.status` 读 `el.dataset.status`，而那是 `<select>`（值在 `el.value`）—— 每次流转都发出空结论被服务端拒绝 | 处理器从未被真实调用过 |
| 7 | `state.error` 被写入 18 处，但 `#errorBanner` 无一处引用 —— 取数失败、保存被拒、流转失败全部静默 | 只有 `web_embed_test` 检查 `index.html` 里存在这个 id，没检查它被写过 |
| 8 | `state.loading` 写入但从不读取；`refreshBtn` 的 `:disabled` 样式写了却无人置位 —— 读大库时界面看起来像卡死 | 同上 |
| 9 | `readPref` 从未被调用：归档模式与度量选择每次刷新都回默认值，而代码注释写着"跨刷新保留" | 注释描述意图，测试验证渲染，两者都不执行 localStorage |
| 10 | `WORKSPACE_VIEWS` 手写且滞后（含已合并掉的 `overview`、缺 `summary`），停在概览页时侧栏**没有任何高亮** | 手写副本的固有失效；`paintNav` 未被执行 |
| 11 | `scope=""`（未归属记录）被路由器当空值丢弃 —— 这个工作区可进入但不可分享/刷新/后退 | 路由往返测试的样本里没有 `scope=` 空值这一条 |
| 12 | `q` 参数同时被 issue 搜索与调用搜索使用，互相覆盖 | 同上 |
| 13 | `metricStep` 比较 `errThreshold`（不存在）而非 `errorThreshold` —— **error 带收紧完全不可见**，warn 带正常显示 | 渲染测试没有断言表格行的 class 差异 |
| 14 | `VIEWS.overview` 与 `VIEWS.summary` 是两个概览页，`workspaceNav` 的"概览"指向其中一个、侧栏"项目概览"指向另一个 | 视图表只被遍历渲染，没被检查语义重复 |
| 15 | `webspec.Views()` 只声明 6 个视图，前端有 10 个 —— **契约单一来源自己就是漂移** | spec 测试只比对 `Build()` 与 `spec.json` 的字节，比对不了前端 |
| 16 | `sev-error/warning/info` 三档严重度徽章**在 CSS 里完全没有定义**，看起来一模一样 | 契约测试检查 CSS 变量，不检查类名 |
| 17 | 空态行写成 `'<tr>…</tr>'` 普通字符串（5 处）—— 修 #2 后立即变成可见源码 | 空态分支在 fixture 下永远不进 |
| 18 | 策略编辑器的未保存改动只存在 DOM 里：任何一次重绘都用服务端副本重建 textarea，**操作员以为已生效的阈值被静默丢弃** | 没有测试执行"编辑 → 重绘"这一步；`policy.edit` 的处理器本身是空函数 |

### 15.1 修法与新的第四层测试

- 1/7/8/10：`index.html` 补 `id="app-shell"`；`paintNav` 负责写 `#errorBanner`、`updatedAt` 的读取态、`refreshBtn.disabled`；侧栏高亮从 DOM 按钮反推，不再手写名单。
- 2/3/17：`join()` 返回安全片段；`fetchIssues` 与载荷同名 `issues`；裸 HTML 字符串一律换成 `html` 片段。
- 4/5/11/12：`set()` 统一写 hash（导航用 `location.hash` 产生历史条目，筛选细化用 `replaceState`），并按控件类型分发 `click/change/input`；调用筛选改用 `cq`、`callStatus` 命名空间。
- 6/9/13：处理器改读 `el.value`；`boot()` 先读偏好并恢复偏好；`errorThreshold` 拼写修正。
- 18：未保存草稿进 `state.policyDraft`，textarea 与"有未保存的改动"提示都读它；新增 `record()`——S3 唯一的**不通知**写入，因为一次 `set()` 会在击键中间重建舞台，进而打断输入法合成（中文注释打到一半即丢失）。豁免只有一处、只写文本，`innerHTML` 仍然只有一个出口。
- 14/15：`overview`/`summary` 合并为一个项目概览（对齐 SonarQube 的 project Overview 单页语义），`Views()` 补全为 9 个并加双向守卫 `TestViewTableMatchesSpec`。

第四层是 `internal/cockpit/web/smoke.mjs`，三条设计决定使它和前三层不同：

1. **入口是真的**。`--probe` 让**前端自己报告**它会发哪些请求，Go 侧用真实 handler 应答，再把应答交回真实的 fetch 层与 `render()`（不是视图函数）渲染。DOM 只是薄 stub：`boot()`、`set()`、委托派发、`#errorBanner` 写入都是生产代码。
2. **端点集合是不动点**。加载期的请求可以先猜中，但一次委托点击（改排序、搜索框输入）会让前端提出**新的** `/api/issues?…` 变体。Go 侧因此反复收割 `REQUEST` 行、补答未捕获的请求、再跑一轮（上限 4 轮），而不是把端点清单写死在测试里。
3. **载荷按前端请求顺序解析**。同一个 endpoint 在交互后会有多个变体，而 Go map 序列化成 JSON 是按键排序的——早期版本因此拿 `q=loop` 那一页去比对 `sort=severity` 那一屏，报出一个并不存在的行数分歧。

断言面：9 视图 × 3 scope 全矩阵（拾取器 / 单项目 / 未归属记录）、载荷行数 == 页面行数、面包屑在每个 scoped 视图存在、facet chip 同时带 kind 与 value、作用域趋势图在有 series 时必须有柱、错误必须可见并可清除、加载态必须反映到 `refreshBtn.disabled` 与状态标签、每个视图侧栏恰好一项高亮、`issue.select` 经真实 `/api/source` 出代码行。

`policy` 的草稿断言是"编辑 → 重绘 → 文本仍在"这一条真实链路，不是字符串检查。交互层还直接派发事件：`dispatch("click"/"change"/"input", …)` 走 `boot()` 注册的同一张委托表，因此"控件有 `data-action` 但没有对应事件类型"这一类缺陷第一次可测——这是第 5、6 条能被抓到的唯一原因。

**变异验证**（每条新断言都以"它确实会失败"为准，不是写完就信）：

| 变异 | 报出的失败 |
|---|---|
| 删掉 `banner.textContent` 写入 | `error: a recorded failure never reached #errorBanner` |
| `updated.textContent` 不再读 `loading` | `loading: the status label reads "更新于 …"` |
| 去掉 `writeRoute()` 调用 | `delegation: the hash is "" after navigating…` + 后退落错视图 |
| `SELECT` 回到只认 click | `delegation: a click on a select changed the sort from the value being left` |
| `issues` 写回 `rows` | issues 视图抛错、行数为 0 |
| 去掉面包屑 | `issues (project)/(unassigned): no breadcrumb…` |
| 去掉作用域趋势图 | `activity (project): 1 trend points, no column rendered` |
| 掏空 facet 的 `data-value` | 四组 chip 全部报"无值" |

### 15.2 这一轮该记住的

单文件形态把构建步骤换成了"自己写契约测试"，但 §8 那三层的共同前提是**测试代码自己充当浏览器**——一旦它绕过 `boot()`、绕过 `render()`、绕过 fetch，它就只是在验证自己写的那份 fixture。真正的分界线不是"有没有浏览器"，而是**被测的是生产入口还是生产入口的副本**。`smoke.mjs` 的 DOM stub 很薄，但它跑的是 `boot → fetch → render → 写 DOM` 这条真链路，上表里凡是只有"真的启动、真的取数、真的绘制"才能暴露的条目（1、2、3、5、6、7、8、9、10），在这一层第一次跑起来时全部当场失败；其余条目由 §15.1 的双向守卫与 CSS 类名核对补上。

预算：`internal/cockpit/web/app.js` 2093 行 / warn 2000 已越线（warning，非 error，error 线 4000）。第二轮回加了启动修复、路由、草稿与趋势图后从 2055 涨到 2093；下一动作按 §14.3 的约定是砍页面而不是提阈值；候选见 §11。

### 15.3 收尾：全量闸门才暴露的两件事

1. **`-diff` 口径抓不到文件级 metric。** `check . -diff` 一路绿灯，`check .` 才报出 `internal/cockpit/issues_api.go` 1043 行越 error 线（800）。按 §14.3 的交易——"长度换结构"——**拆文件而不是提阈值**：沿文件里本来就写好的三节边界分成 `issues_api.go`(332) / `gate_api.go`(465) / `source_api.go`(263)，各节的设计注释跟着自己的代码走。这也说明 §7 那条纪律的正确口径是**收尾跑全量**，`-diff` 只够当过程中的快速反馈。
2. **两条 error 是 HEAD 里既有的**：`cmdExplainPreview`（18，本轮未触及该文件）与 `digest.go` 的 `banSection`（16）。把本轮的改动与仓库既有欠账分开记账，不顺手改无关代码，也不对外声称全树零 error。
3. **发布物里混进了 dev harness。** `//go:embed web/*` 把 `render_all.mjs`、`smoke.mjs`、`fixtures.json`（合计约 38 KB）一起打进二进制的嵌入文件系统：浏览器永远不会请求它们，但 `curl` 下来的人在 `strings` 里会看见 `.mjs`，"零 Node 依赖"这句话就有了杂质。改成 `dashboard.go` 里四条显式 `//go:embed`（`index.html` / `app.js` / `style.css` / `spec.json`）。显式清单的风险是漏一条 → 浏览器 404 而**每一层 Go 测试照样绿**（正是 §15 那一类"测试自己充当浏览器"的失败），所以配套加 `TestEmbeddedAssetsCoverTheRequestGraph`：从 `index.html` 的 `href/src="/web/…"` 与 `app.js` 的 `from "./…"` 反查真实请求图，逐项要求已嵌入，反向要求嵌入文件必须有人引用（除 `index.html` 自身），并显式点名三个 harness 文件不得在包内。变异验证：删掉 `//go:embed web/spec.json` → `web/spec.json is requested by the UI but not embedded`；改回 `web/*` → 三条 harness 同时报"不该嵌入/无人引用"。二进制从 34775250 降到 34742226 字节。

4. **"我重启了，界面还是旧的"——两个各自独立的原因。** 用户实测反馈，值得单独记账，因为它暴露的是流程而不是代码：
   - **主因：跑的是旧二进制。** `go build ./...` 带多个包时**丢弃输出**，仓库根的 `metricgate` / `metricgate-mcp` 只有显式 `-o` 才会被重写；`PATH` 上的 `/Users/mac/go/bin/metricgate` 则是更早 `go install` 留下的（mtime 前一天），而 `lsof` 显示 cockpit 进程的 `txt` 正指向它。判断方法：`rg -a --fixed-strings 'policyDraft' <binary>`，旧二进制为 0。根因是 `toolVersion` 是常量，`metricgate version` 对任何本地构建都打印同一个 `0.1.0`，"我跑的是不是刚编的"从外部不可回答。修法：`cmd/metricgate/main.go` 加 `buildStamp()`（读 `debug.ReadBuildInfo()` 的 `vcs.revision` / `vcs.modified`，截 12 位、脏树加 `+dirty`），`version` 与 cockpit 启动行都带上；纯函数 `formatBuildStamp` 单独可测。
   - **次因：静态资源没有缓存校验。** `http.FileServer` 对嵌入文件不发 `Last-Modified`/`ETag`（embed 的 `FileInfo` 无 modtime），浏览器可以启发式复用旧副本——即使二进制换了，页面也可能继续用缓存里的 `app.js`。这个坑会在用户下次重编后立刻咬人，所以 `webMount` 的静态路由套 `noStore()` 发 `Cache-Control: no-store`（API 路由本来就有）。两条都加了会失败的断言：`TestEmbeddedUIServesEveryAssetAndEndpoint` 现在要求四个资产都带 `no-store`（变异：去掉 `noStore` → 四条 `cache-control = ""` 全部报错），`TestFormatBuildStamp` 覆盖截断/脏树/无 stamp（变异：去掉 `+dirty` 后缀 → 对应子用例失败）。

第四层测试还纠正了本文档一处判断错误：`§8.3` 写的"视图正确性主要靠静态断言 + 纯函数单测兜，不追求浏览器端 E2E"，实测是错的——见 §15 表格，那两层全绿的期间界面从未启动过。§8 已按四层重新理解：**能被生产入口执行的断言才算覆盖，测试自己充当浏览器的部分不算**。
