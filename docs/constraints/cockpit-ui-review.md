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
n=$(awk -v s="$a" 'NR>s && (/^VIEWS\.[a-z]* = \{/ || /^\/\/! S8/) {print NR; exit}' internal/cockpit/web/app.js)
awk -v s="$a" -v e="$n" 'NR>=s && NR<=e' internal/cockpit/web/app.js \
  | grep -oE '(error|warning|info|success|grade-[A-E]|tone-[a-z]+)' | sort -u | wc -l   # 期望 ≤8
```

实测（2026-10-17，`app.js` 3456 行）：projects 0 / issues 1 / calls 1 / runs 3 / overview 0 /
policy 1 / activity 3 / gate 2 / rules 1。全部远低于上限——规则 1 是防回归的闸门，不是待修项。
（2026-10-16 的旧值是 gate 1 / rules 3。两个数都动了：gate 页因横幅与状态列多出一档；
rules 页的旧值 3 是量测范围的偏差 —— 它是最后一个 `VIEWS`，上面的 `awk` 找不到下一个
`VIEWS.X = {` 就把范围拉到文件末尾，扫进了 S8 的 `s.error` 分支。按上表命令量测时把终止条件
加上 `|| /^\/\/! S8/`，rules 就是真实的 1。）

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

## 规则 7 · 详情面板不占列宽

表格页的详情（问题详情、后续任何同类面板）**不得作为 grid 的第三列**预留宽度。
密集表格的每一列都需要最小可用宽度，而一个常驻的第三列会在窗口不够宽时先把「问题」这类
自由文本列压成省略号——恰恰是操作者正在读那一行的时候，行的标题消失。

详情要么浮在列表之上（`position: fixed`，只遮住右边缘，一键可关），要么在窄屏下沉为列表下方的块。

```bash
# 详情列不得出现在 grid 模板里。排除单列（那是窄屏下沉，不是第三列）。
grep -nE '\.(issues|runs)-layout\.has-detail *\{[^}]*grid-template-columns' internal/cockpit/web/style.css \
  | grep -v 'grid-template-columns: minmax(0, 1fr); }' \
  && echo 'FAIL: 详情面板被排成了 grid 列'
```

反例（本仓真实踩过，2026-10-17）：`.issues-layout.has-detail { grid-template-columns: 13rem minmax(0, 1fr) 24rem }`
在 1600px 窗口下把表格压到约 660px，7 列的「问题」列渲染成 `fan-out of ...`；
补 `.issues-table { min-width: 46rem }` 只是把裁剪推给横向滚动，`引入`/`状态` 两列彻底看不见。
最终改为抽屉浮层后两列同时可用。

## 规则 8 · 坐标轴标签不得重复无信息

趋势类图表的横轴标签，当全序列落在同一天时**必须换成时刻**，且点数多于空间时必须稀疏绘制。
二十根柱全标 `10-17` 等于没有坐标轴，而唯一能说的信息（这一天里的什么时候）被省掉了。

```bash
# 稀疏绘制：标签默认隐藏，每 4 根 + 最后一根可见
grep -n 'trend-column:nth-child(4n + 1) small' internal/cockpit/web/style.css \
  || echo 'FAIL: 趋势轴标签未稀疏绘制'
```

反例：2026-10-17 截图 `p4-overview2.png` 的轴读作 `10-170-170-17`（二十个 `10-17` 互相重叠）。
修法：`sameDaySeries(points)` 判定后把标签切成 `HH:MM`，并把 caption 里的「横轴是运行日期」
同步换成「运行时刻」——只改图不改文案会留下一个说谎的说明。

---

1–2 为硬规则，违反即回改；3–5 与 7–8 由 `bash scripts/cockpit-observe.sh quick` 与收尾
`fast` + `check_path('internal/cockpit/web', diff_only:true)` 一并兜住；6 靠清单自证。
新增规则必须带**可执行判定命令**与一条已知违规的反例，否则不算规则。
