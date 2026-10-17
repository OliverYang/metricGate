package cockpit

// Chinese copy for the rule catalogue.
//
// The tables live here rather than in internal/rules on purpose: the rule pack
// is also consumed by the CLI, the SARIF report and the MCP tools, and none of
// those should receive UI prose. internal/cockpit is the layer that exists to
// serve the page, which is why RULE_REASONS already lives in app.js.
//
// Coverage is enforced, not hoped for: TestRuleChineseTablesCoverEveryBuiltinRule
// walks every language's pack and fails if an id is missing from either table.
// The fallback for an uncovered id is the English title and an empty
// description, never a machine translation — inventing a sentence nobody wrote
// would be a claim about the engine that no one can check.

// ruleTitlesZh is the one-line name of a rule, by rule id. Rule ids are shared
// across languages (empty-block means the same thing in Go and in Java), so one
// entry covers every language that carries the rule.
var ruleTitlesZh = map[string]string{
	"empty-block":        "空的控制流块",
	"todo-comment":       "代码里遗留的 TODO/FIXME 注释",
	"unknown-dependency": "依赖未声明（疑似臆造的包）",
	"call-chain-depth":   "方法调用链过长（火车残骸）",
	"test-no-assert":     "测试函数里没有任何断言",
	"print-stack-trace":  "printStackTrace() 绕过日志管道",
	"silent-catch":       "捕获的异常既未使用也未抛出或记录（被吞掉）",
}

// ruleDescriptionsZh says what counts as a violation, which the title alone does
// not: "空的控制流块" does not tell the reader whether an else-branch with a
// comment is empty. One sentence per rule, addressed to whoever has to decide
// whether the finding in front of them is real.
var ruleDescriptionsZh = map[string]string{
	"empty-block": "if / for / while / switch 的块里没有任何语句。" +
		"只有注释不算空——注释说明这里有意为之，是留下痕迹的写法。",
	"todo-comment": "注释里出现 TODO 或 FIXME。" +
		"默认关闭：它是待办清单，不是缺陷，只有在把未完成工作当作交付标准时才该打开。",
	"unknown-dependency": "import / require / include 的目标不在依赖清单里，" +
		"可能是包名拼错、包不存在，或是模型凭印象写出来的名字。" +
		"判断依据是仓库的 go.mod / package.json / pom.xml 等清单，不是网络的包索引。",
	"call-chain-depth": "一条表达式里连续调用的层数超过 max_depth。" +
		"典型形态是 a.b().c().d()：每一层都是对内部结构的假设，" +
		"上游改一次字段名，这一行就断。",
	"test-no-assert": "测试函数体里没有任何断言调用。" +
		"这类测试永远通过，因此它既不能发现回归，也不构成对行为的任何主张——" +
		"比没有测试更危险，因为它看起来有覆盖。",
	"print-stack-trace": "直接调用 printStackTrace()，输出绕过项目的日志管道。" +
		"异常因此进不了日志聚合、没有级别、也没有关联 ID，生产环境里等于丢失。",
	"silent-catch": "catch 到的异常对象从未被引用、重新抛出或写入日志。" +
		"程序继续运行，但失败被就地抹掉：调用方看到的是成功。",
}

// ruleChineseCopy returns the page's copy for one rule id. An id the tables do
// not cover returns ("", "") — the caller substitutes the English title and
// leaves the description empty. The coverage test is what keeps that branch
// from being the normal path.
func ruleChineseCopy(ruleID string) (title, description string) {
	return ruleTitlesZh[ruleID], ruleDescriptionsZh[ruleID]
}
