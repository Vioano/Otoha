# Otoha 六分钟路演

这套路演把“分级异步通信”讲成一条能看见的链路：多个 Agent 并行工作，完整正文持久化在 Otoha Core，其他 Agent 先收到小信封，在任务边界再取正文并回传已读回执。浏览器页面里的数据是合成的演示数据；CLI 的 `team init`、`status`、`doctor` 使用本机临时目录，实际调用当前代码。

## 开场前

在仓库根目录执行：

```sh
npm run build:cli
./scripts/roadshow-demo.sh
```

脚本会在临时 `OTOHA_HOME` 中启动一个本机协作服务，演示结束后清理它。它不会读取或覆盖已有身份、真实消息和真实服务。浏览器部分直接打开 `showcase/index.html`，也可以在仓库根目录运行 `python3 -m http.server 4173` 后访问 `http://127.0.0.1:4173/showcase/`。

## 六分钟节奏

| 时间 | 画面与动作 | 讲解重点 |
| --- | --- | --- |
| 00:00–00:35 | 终端输入 `otoha`，让 ASCII 标志完成一次波形动画 | Otoha 是每个 Agent 本机的通信控制面；先看清楚状态，再决定读什么 |
| 00:35–01:15 | 输入 `d`，播放终端事件流；停在 `SYNC` | 发件箱先保存，中心服务按 `uid` 去重并给出顺序 |
| 01:15–02:00 | 展示 `team init`、`team status`、`team doctor` 的 JSON | 这是可运行的本机闭环；`doctor` 只检查，不悄悄接管旧状态 |
| 02:00–03:00 | 打开网页，点击 **Start 6-minute story**，让网络开始发光 | planner、builder、reviewer、graph 和人类反馈并行推进 |
| 03:00–04:10 | 等事件流经过 `ROUTE` 和 `READ`，强调两次状态变化 | 通知只带摘要；只有接收方明确取正文才算已读 |
| 04:10–05:00 | 点击 **Reduce motion**、主题按钮，再恢复深色 | 录屏时可控动效，也保留浅色主题和键盘/焦点入口 |
| 05:00–05:40 | 回到 CLI，运行 `./dist/otoha doctor` 或展示脚本结果 | 把事实、模拟数据和未验证项分开说：本地链路已测，真实跨机/Telegram 仍需单独验收 |
| 05:40–06:00 | 回到网页的 `DONE`，停在共享核心 | Otoha 不替 Agent 决策；它让独立工作在同一个可追踪状态上汇合 |

## 录屏用的三个命令

```sh
./dist/otoha              # 默认进入终端控制台
./dist/otoha demo --slow  # 只播放终端故事，不需要凭据
./dist/otoha doctor       # 输出当前本机运行时与团队服务检查
```

## 需要诚实标出的边界

- `showcase/index.html` 的多 Agent 网络和事件时间线是合成数据，用来解释协作过程，不证明真实五个 Agent 已经在外部平台并行运行。
- `team init`、服务健康检查、CLI 测试和本机收发属于本地验证；真实 Telegram Bot-to-Bot、跨机器 TLS、Windows 服务和离线超过平台保留期限仍需对应环境验收。
- 终端 UI 继承 JSON 子命令，不改变现有显式命令的机器可读输出；没有参数时才进入人类演示界面。
