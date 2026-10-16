# metricGate Cockpit UI 评审规则（可机器判定）

范围：`internal/cockpit/web/` 下的 `app.js`、`index.html`、`style.css`（`go:embed` 的恰好 4 个文件，
其余文件一律不允许放进该目录）。

来源：`docs/design/cockpit-sonarqube-parity.md`（视觉对齐 SonarQube / Echoes 的决策）与
`docs/iterations/cockpit-ui/` 五段迭代中逐条沉淀的教训。本文件**只写规则与判定口径**，
不负责门禁绑定（绑定见各段迭代的收口记录）。

纪律底色：SonarQube 前端 `sonarqube-webapp` 是 LGPL-3.0。我们只借鉴**结构与交互语义**，
从不搬运其代码与 l10n 文案；本工具的 UI 文案是中文，且不需要英文原文对照。

---

## 规则 1 · 单内页颜色类 ≤8

一个视图模板在其自身及其调用子树里引用的**颜色类名**（`error` / `warning` / `info` /
`success` / `grade-A..E` / `tone-*` 等语义色）不得超过 8 个。颜色是密度最高的视觉信号，
一次给出超过 8 种语义色，页面就从"扫一眼"退化成"逐个读"。

```bash
# 数某个 VIEWS 块（含其调用的行内类名）里出现的语义色类
a=$(grep -n '^VIEWS\.policy = {' internal/cockpit/web/app.js | cut -d: -f1)
n=$(awk -v s="$a" 'NR>s && /^VIEWS\.[a-z]* = \{/ {print NR; exit}' internal/cockpit/web/app.js)
awk -v s="$a" -v e="$n" 'NR>=s && NR<=e' internal/cockpit/web/app.js \
  | grep -oE '(error|warning|info|success|grade-[A-E]|tone-[a-z]+)' | sort -u | wc -l   # 期望 ≤8
```

实测（2026-10-16，`app.js` 2488 行）：projects 0 / issues 1 / calls 1 / runs 3 / overview 0 /
policy 1 / activity 3 / gate 1 / rules 3。全部远低于上限——规则 1 是防回归的闸门，不是待修项。

## 规则 2 · rail 是 9 个视图的唯一入口

`index.html` 的 rail 有且仅有 9 个 `data-view` 按钮，与 `app.js` 里 `VIEWS.<key> = {`
的定义一一对应。任何"页面内新建第二套视图切换"的做法都要先问是否该变成一个视图参数（tab / scope），
而不是第十个按钮——rail 是操作者的心智地图，第二张地图会让人先怀疑自己走错了地方。

```bash
grep -c 'data-action="nav.view"' internal/cockpit/web/index.html   # 期望 9
grep -c '^VIEWS\.[a-z]* = {' internal/cockpit/web/app.js          # 期望 9
```

## 规则 3 · facet 计数口径 = 全量，点击才收窄

筛选栏里的计数描述**未筛选的全集**，不能描述当前已筛选的行。否则点一个 chip 之后其它 chip 的
数字集体塌向选中值，操作者为了知道"还有什么可看"必须清空筛选——这与 facet 的存在理由相反。

```bash
# facet 计数必须来自 facetCounts/facetCache，不得直接读 s.issues.*
grep -n "s\.issues\.severities\|s\.issues\.statuses" internal/cockpit/web/app.js
# 上面只允许出现在 facetCounts 自身（缓存未命中时的降级）与注释里
```

## 规则 4 · `:root` 之外不出现颜色字面量

`style.css` 的令牌块 `:root { … }` 之后不允许再出现十六进制 / `rgb(` / `hsl(` 颜色。
所有颜色必须经 `--e-*` 或别名令牌引用，否则换主题时会出现"改了令牌但有几处不动"的静默失效
（CU-00 就踩过：`var(--border)` 未声明导致边框静默退回浏览器默认值）。

```bash
ROOT_END=$(grep -n '^}' internal/cockpit/web/style.css | head -1 | cut -d: -f1)
awk -v n="$ROOT_END" 'NR>n' internal/cockpit/web/style.css \
  | grep -nE '#[0-9a-fA-F]{3,8}|rgba?\(|hsla?\(' && echo 'FAIL: 令牌块外有颜色字面量'
```

## 规则 5 · 非数据驱动不得使用 inline `style="`

`style="` 只允许用于**数据驱动**的条宽 / 柱高（值来自 payload，无法写成静态类）。
间距、padding、border 这类结构性样式必须走类名——inline 结构性样式是令牌化的漏口，
抽主题时扫描不到它。

```bash
grep -n 'style="' internal/cockpit/web/app.js
# 每一行都必须满足：style 值里含 ${…} 插值（数据驱动）
grep -n 'style="' internal/cockpit/web/app.js | grep -v 'style="[^"]*\${' && echo 'FAIL: 存在装饰性 inline style'
```

## 规则 6 · 禁抄 SonarQube 代码与 l10n 文案

不得从 `sonarqube-webapp` / `@sonarsource/echoes-react` 复制源码、类名到实现的直译或文案；
参考图只用于比对结构与密度。缺后端字段的能力**不伪造占位**：要显示就显示可解释的代价档位并
标明推断（Effort / Assignee / Tags 即为此类，payload 无字段，故不渲染）。

```bash
# web/ 目录里唯一允许的 sonarsource 出现形式是注释里的出处引用
grep -rn 'sonarqube-webapp\|@sonarsource' internal/cockpit/web/ | grep -v '^[^:]*:[0-9]*: *\*' && echo 'FAIL: 出现非注释引用'
```

实测（2026-10-16）：仅 `style.css:27` 一处命中，且是注释里的出处引用（说明 `--e-*` 变量名与
`@sonarsource/echoes-react` 的 `dist/tokens/sonar/themed.json` 可逐一对照，便于审计），非代码搬运。

---

## 违规处置

1–2 为硬规则，违反即回改；3–5 由 `bash scripts/cockpit-observe.sh quick` 与收尾
`fast` + `check_path('internal/cockpit/web', diff_only:true)` 一并兜住；6 靠清单自证。
新增规则必须带**可执行判定命令**与一条已知违规的反例，否则不算规则。
