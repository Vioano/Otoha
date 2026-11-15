# 通用审查协议与评分通过器

2026-11-15。本协议把观察问题、判定规则和执行程序分开。同一评分通过器可以执行不同任务的题包；网页成品绑定、PPTX 几何分析等专门能力保留在各自的适配器或事实采集器中。

## 三个部分

| 部分 | 输入与责任 | 不负责什么 |
| --- | --- | --- |
| 事实采集 | 程序、独立审查者或人填写实际观察与证据 | 不提交通过、评分或自批豁免 |
| JSON 题包 | 定义问题、适用分支、规则、得分及预先批准的条件豁免 | 不执行内嵌 Python、Shell 或任意表达式 |
| 确定性引擎 | 校验输入，计算条件、全部发现、得分和放行结果 | 不凭规则计算证明观察真实，也不替代人的最终验收 |

问题询问事实，规范性判断留在规则中。例如询问“目标文字的实测对比度是多少”，由规则比较阈值；不询问“是否应当通过”。“是否好看”不属于客观事实题。重复文案、视觉关系是否清楚等观察仍可能包含语义判断：换成选项或 JSON，并不会让它自动客观。此类标准应继续拆成可取证的问题，并用真实正反例校准审查者。

## 为什么采用 JSON

JSON 是当前唯一执行格式，版本标识为 `"protocol": "otoha.review/v1"`。它有跨语言支持，能直接校验与版本比较，适合保存同一题包内的分支和结果规则。解释写入 `description`、`prompt` 或 `reason`，不另引入 YAML 解析器。

JSON Schema 可以辅助检查文件结构；它不能代替这里的三值条件、评分和放行算法。题包是受限的声明数据，不是一种通用编程语言。

## 题包和观察

题包必填 `protocol`、`areas`、`questions`、`rules`；可选 `id`、`title`、`version`、`description`、`scoring` 和 `waivers`。`areas` 对问题与规则分组，每项含 `id`、`name`，不预设任何网站专用领域。

问题包含 `id`、`area`、`prompt`，以及可选的 `required_when`。支持四种类型：

| `type` | 定义或观察 |
| --- | --- |
| `choice`，默认 | 定义 `choices`，每项有 `id`、`label`；未知项标记 `unknown: true`。观察为 `{"choice":"选项 ID","evidence":"实际观察"}` |
| `boolean` | 观察为 `{"value":true,"evidence":"实际观察"}` |
| `number` | `value` 为有限数值；布尔值不充当数值 |
| `text` | `value` 为字符串 |

数值、布尔或文字无法确认时，记录 `{"value":null,"unknown":true,"evidence":"无法确认的具体原因"}`；`null` 值本身也视为未知。缺失观察、未知选项或空证据均不能构成已确认事实。观察输入不接受 `status`、`score` 或 `released` 这样的结论字段。

条件既支持旧题包的 `{"问题 ID":["选项 ID"]}` 合取格式，也支持以下有限条件树：

```json
{
  "all": [
    {"question": "rendered", "op": "eq", "value": true},
    {"not": {"question": "ratio", "op": "gte", "value": 0.99}}
  ]
}
```

组合为 `all`、`any`、`not`；叶子比较为 `eq`、`ne`、`in`、`lt`、`lte`、`gt`、`gte`。未知使用三值逻辑，`not unknown` 仍为未知；分支条件未知时不能当作“不适用”。校验拒绝重复 ID、未知引用、非法类型和适用条件循环。

`required_when` 为假时，问题为 `inactive`。这是事实决定的不适用，区别于发现问题之后的豁免。

## 规则、评分与处置

每条规则有唯一 `id`、`area`、`when`、`outcome` 和具体 `reason`。`when` 命中后产生发现；其事实尚未确认时产生待核验项。

| `outcome` | 含义与处理 |
| --- | --- |
| `blocked` | 已确认违反要求，修复后复查 |
| `unverified` | 缺少或无法确认所需事实，补证据 |
| `needs_arbitration` | 已知冲突或需要有权者裁决，保留待仲裁 |
| `on_hold` | 题包定义的依赖或搁置条件成立，条件解除后重查 |

所有发现均保留。主状态按 `blocked > needs_arbitration > on_hold > unverified > pass` 显示，不因最高优先级的一项而隐藏其他未解决项。未知不会被自动改称仲裁或搁置。

可选评分格式为 `{"minimum":20,"items":[{"id":"render-score","when":{"question":"rendered","op":"eq","value":true},"points":20}]}`。分项得分由条件计算；未知分项仍需核验。已确定低于门槛时阻断，尚待确认的分项可能补足时保持待核验。达到最低分不能抵消任何未解决发现。只有观察齐备、适用规则没有未解决项且达到已配置的分数门槛，才得到 `status: "pass"` 和 `released: true`。

豁免使用题包内的 `waivers`：每项含 `id`、`rule_id`、`when`、`reason`，且目标规则必须明确 `waivable: true`。它表达题包已经批准的、有事实条件的例外。条件未知不授予豁免；原始发现及所应用豁免一并保留。观察者不能以普通答案自批豁免。需要人临时批准的例外应先保持待仲裁；当前实现没有新增人工授权、签名或审批系统。

## 执行和版本边界

核心源码为 `packages/review-gate/otoha_review.py`，通用入口为 `scripts/review-gate.py`。两者使用 Python 标准库。网页适配器继续负责页面身份、逐页范围、制作者与独立审查者身份、版本和宿主 Stop 接入；原题包字节与历史记录保持兼容。通用引擎本身不将这些前端约束推广给所有任务。

核心由既有 `frontend-acceptance/scripts/acceptance.py` 的 `evaluate` 提取并扩展；原文件未在 Git 中发布，本轮来源快照 SHA-256 为 `9f05a9e4ee3156074f2846ef1b961bb00a57df5c3c82b47f9f8160af410712b1`。原题包、双页面展开与 264 个原报告摘要向量保存在 `test/fixtures/review-gate/`，用于检查兼容性。旧题包不能直接增加通用评分或豁免字段；引擎会明确拒绝，避免字段被静默忽略。

```sh
python3 scripts/review-gate.py validate --rules examples/review-gate/artifact-review.json
python3 scripts/review-gate.py start --rules examples/review-gate/artifact-review.json --state /tmp/review.json --artifact /path/to/artifact
python3 scripts/review-gate.py next --state /tmp/review.json
python3 scripts/review-gate.py observe ratio_error --state /tmp/review.json --value 0.01 --evidence '引用当前几何报告中的实测最大相对误差'
python3 scripts/review-gate.py finish --state /tmp/review.json
```

`observe` 使用互斥的 `--choice`、`--value` 或 `--unknown`；`--value` 接受 JSON 标量。`status` 和 `finish` 重算结果，观察者不填写计算结果。开始时绑定题包摘要、引擎摘要和显式声明的产物摘要；任何绑定输入变化都需新一轮记录，不能沿用原结果放行。未声明的产物不在该摘要绑定范围内。

`finish` 仅在计算放行时返回 0，未解决项返回 1，格式、绑定或执行错误返回 2。其他命令返回 0 只表示该操作成功，应读取报告状态，不能当作放行。

已有前端 Skill 的分发适配使用 `python3 scripts/sync-review-core.py --frontend-skill /path/to/frontend-acceptance` 先预览，加 `--apply` 后同步核心并替换原判定函数。它不改题包、状态或 hook；更新核心前运行 `npm run test:review-gate`。题包采用新的通用协议不意味着前端专用流程可省略。

这一拆分提供正常接口上的约束：观察者不能提交结论，规则修改不会无声地改变旧记录。它不提供本机权限隔离；拥有引擎、题包和记录写权限的模型仍能改文件。需要不可篡改的执行时，应由现有受保护的 CI 或权限边界托管执行，不能把普通本地 CLI 宣称为这样的安全边界。

本轮提供源码入口和前端适配器复用，尚未把它作为下载版 Go CLI 的 `otoha review` 子命令发布。

## 与每个问题单写程序的取舍

| 方式 | 适用工作 | 代价 |
| --- | --- | --- |
| 通用引擎与题包 | 标准问题、条件分支、覆盖检查、评分、阻断和结果说明 | 受限协议不能描述所有领域算法；题包仍需验证 |
| 每项专用程序 | OOXML 几何、AST 分析、媒体测量等复杂事实提取 | 容易重复实现未知处理、规则聚合和报告格式 |
| 专用采集器接通用引擎 | 已有领域算法输出事实，由同一协议形成判定 | 需明确每个事实的来源与适用版本 |

默认采用第三种组合。把可复用的判定固定在引擎里，把会变化的题目与标准放进题包，把确实需要专门算法的事实提取留在代码中。题包通过只覆盖已编码标准和已记录范围，不等于整套作品或全部用户需求已被验收。
