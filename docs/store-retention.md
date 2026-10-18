# 历史库的保留与清理（`store prune`）

`metricgate check` 每次写库都会让 `.metricgate/history.db` 变大：一行 `runs`、若干
`findings`、以及成百上千行 `metric_snapshots`。`store prune` 是唯一的删除入口，
它按 policy 里的 `store.retain` 决定哪些 run 可以退休。

这份文档回答三件在终端里看不出来的事：**这次跑处于哪种状态**、**为什么删了 211 MB
文件却没变小**、**以及一条真实删除在动手前必须满足什么**。

## 1. 三种状态，读法不同

`store prune` 的输出第一行是 `retention:`，它决定后面该看什么：

| 状态 | `retention:` 行 | 删除行 | 该读什么 |
|---|---|---|---|
| 无界（默认） | `unlimited (no bound set): nothing is ever removed` | `nothing to retire: no bound is set, so N runs stay in …` | 到这里为止；这一支根本不开事务 |
| 有界但无可删 | `keep newest N runs [-runs/-days]` | `nothing to retire: all N are kept by policy (archived runs and the newest live run of each project_root are never removed)` | 括号里那半句解释了 0 从哪来 |
| 有界且要删 | 同上 | `removed N runs (F findings / S snapshots), about B` + `kept: K runs` + `file: X -> Y; Z is now free pages` | 三行都要读，见 §2 |

无界是**默认**，不是遗漏：`store.retain` 的零值 = 全部保留（iter-25 决策 D22-5，
`docs/iterations/iter-25-store-retention.md:181`：「默认配置下（不写 `retain`）行为与今天
逐字节相同——写库不触发任何删除」）。所以把 `store prune` 装进 cron 之前必须先在 policy 里
写出界，否则它永远删 0 行并且报得没问题——捞回这个功能时发现的正是这一点
（`docs/iterations/iter-28-cockpit-dark-and-store-retention.md:136` 那条实测：
`nothing to retire: no bound is set`）。

两个删除例外由代码保证，不由阈值保证：`archived = 1` 的 run 永不删除（归档是人的
判断，不是时间的判断）；每个 `project_root` 至少保留一条 live run，否则 Cockpit 上
那张项目卡会静默变空。

## 2. `prune` 删行，`-vacuum` 才还字节

`DELETE` 只是把页放回 freelist，文件不缩短。本轮在真库副本上的实测（`.metricgate/history.db`，
104 runs / 78,067 findings / 2,708,249 metric snapshots / 281.5 MiB / `page_count` 72075）：

```console
$ metricgate store prune -dry-run -runs 20      # 1.737s
would remove 78 runs (29503 findings / 1869629 snapshots), about 211.2 MiB
kept: 26 runs
dry run: database unchanged — page_count 72075 -> 72075, runs 104 -> 104

$ metricgate store prune -runs 20 -yes          # 4.558s
removed 78 runs (29503 findings / 1869629 snapshots), about 211.2 MiB
kept: 26 runs
file: 281.5 MiB -> 281.5 MiB; 179.2 MiB is now free pages. Re-run with -vacuum to return them (not run automatically).
```

顺序因此是固定的：**先 `prune` 删到想要的保留集，再一次 `-vacuum` 回收空间**。
`-vacuum` 只在真实删除且 `removed > 0` 时才执行，`-dry-run` 与 `-vacuum` 同时给会在
解析期就被拒绝（`-vacuum and -dry-run contradict: a dry run changes nothing, a vacuum
rewrites the file`）——空跑不该重写整个文件。`-vacuum` 自己的 flag 说明也提醒它需要
与库等量的空闲磁盘。同一份副本接着回收：

```console
$ metricgate store prune -config named.yaml -runs 10 -yes -vacuum
removed 10 runs (2522 findings / 223457 snapshots), about 125.1 MiB
kept: 18 runs
file: 350.4 MiB -> 95.9 MiB; vacuumed, so the freed pages are back on disk.
```

上面两组数字都来自**副本**（`cp .metricgate/history.db` 后在临时目录跑），
真实开发库在整个迭代里只被读过，没被写过一次。
`about 211.2 MiB` 是估算：用本库自己的「每 run 平均字节」× 待删 run 数，含索引页；
换成按行数估会算错，因为 2.9 GB 的库里两个索引就占 660 MB。

`prune_on_write`（默认关）让每次 `check` 写库后顺带 prune。它默认关的成本理由也在这张表里：
删除前要先数，而裸的两个 `COUNT(*)` 全扫在这份库上是 0.329s——不该把这个成本塞进每次保存。

## 3. 护栏：真实删除要求「被命名的文件」

`store prune` 的目标库由**当前工作目录发现的 policy** 决定。在仓库根目录跑
`metricgate store prune -runs 20`，会读到那里的 `.metricgate.yaml`，
于是删的是这个仓库自己的开发库。这件事在本轮真实发生过：78 runs / 29,503 findings /
1,869,629 snapshots 被删，靠事先手工 `cp` 的那份副本还原。

现在同一句在仓库根目录（那里没有 policy 文件）拿到的是内置默认，于是实删被拒：

```console
$ metricgate store prune -runs 5 -yes
note: no policy file found; using built-in defaults
metricgate: refusing to prune: no policy file was named
```

退出码 2，而且它**连库文件都没创建**：那个目录下跑完一句被拒的实删，`ls -R` 仍是空。
护栏在 `store.Open` 之前，所以不存在「拒了但静静地新建了一个空库」这种痕迹。

所以真实删除（非 `-dry-run` 且有界）现在必须过 `authorizePrune`：

1. **policy 必须来自一个磁盘上的文件**（`cfg.SourceKind == "policy_file"`）。用内置默认
   模板跑实删直接被拒，`-yes` **不解锁这一条**——`-yes` 回答的是「别问我」，而这里的问题是
   「哪个文件？」：
   ```console
   refusing to prune: no policy file was named
   ```
2. 打印将要删掉的**具体计数**与 store 的**绝对路径**（`cfg.Store.Path` 可以是相对路径，
   护栏自己 `filepath.Abs`）：
   ```console
   about to remove 78 runs (29503 findings / 1869629 snapshots)
     from /Users/dev/work/metricGate/.metricgate/history.db
   ```
3. 有终端就问一句 `remove this history? [y/N]`，只认 `y`/`yes`，EOF 算 no；
   没有终端（管道、CI）且没给 `-yes` 就拒：`refusing to prune non-interactively without -yes`。
4. `-yes` 跳过提问，但**不跳过打印，也不跳过记账**（步骤 2 那两行照样输出）。

判 tty 用的是 `os.Stdin.Stat()` + `os.ModeCharDevice`，没为此把 `go-isatty` 从 indirect
提上来。`/dev/null` 是字符设备，所以一个被放弃的提问落在「取消」那一支而不是「非交互」
那一支——两支都给可执行的下一步，而不只是一句取消。

## 4. `last_prune` 与 `last_prune_intent` 是两个东西

两个 key 都存在 `store_meta` 里，都是「活得比它描述的那些 run 更久」的信息，但回答不同
问题：

- `last_prune`：最后一次删除**实际**删掉了多少（`PruneRecord{At, Runs, Findings, Snapshots, Kept}`）。
- `last_prune_intent`：这次删除**声称**要删多少、**结果**删掉多少、界从哪来（`Source`：
  `store.retain` 或 `-runs/-days`）、以及被删库的绝对路径。

`intent` 是事后重放的基础：拿它里面的 `Declared` 与 `Removed` 一比，就知道删完之后再跑
`store prune -dry-run` 应当报什么。两者不等时终端上会有一行

```console
warning: declared N runs (F findings / S snapshots) but removed … — a run was added or removed while the prune ran
```

这只在真实删除且确实不等时打印。并发写造成的不等是关于库的事实，不是 prune 的失败，
所以它是一行 warning，不是非零退出。

## 5. 空库

`runs` 表为空时 `Stats()` 里的 `MIN(started_at)` / `MAX(started_at)` 返回 SQL NULL。
这两列在读取时被 `COALESCE(..., '')` 收成空串，所以新克隆的仓库（没有 `.metricgate/`）
或清空的库上，`store prune` 正常报无界并 exit 0——曾经它们会
`converting NULL to string is unsupported` 然后 exit 2。
