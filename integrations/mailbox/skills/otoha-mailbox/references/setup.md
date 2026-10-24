# 安装与查收

需要 Python 3.10+、Git、GitHub CLI (`gh`)。无需额外 Python 包或模型 API Key。所有成员使用自己的 GitHub 账号，并拥有团队项目仓库访问权限。团队仓库需启用 Issues；私有仓库由负责人邀请成员后使用。

## 安装 Skill

克隆 [NikolaStarx/otoha-mailbox](https://github.com/Vioano/Otoha)，在下载目录执行：

```sh
python3 scripts/install.py --project /path/to/team-project
```

默认复制到项目 `.agents/skills/otoha-mailbox`。Claude Code 加 `--agent claude`，复制到 `.claude/skills/otoha-mailbox`。其他 Agent 可直接读取 `SKILL.md`，或将整个目录放到它支持的技能目录；自动发现是否可用由客户端决定。

安装脚本只复制文件，不登录、不发信、不改项目协作规则。已存在相同版本时不写入；内容不同时拒绝覆盖，明确使用 `--update` 才会先备份到项目 Git common dir 中再替换。备份路径会输出。将所选 Skill 目录提交到团队项目后，队友可以直接拉取；每人仍需自行登录和初始化。

官方路径依据：[Codex Skills](https://learn.chatgpt.com/docs/build-skills)、[Claude Code Skills](https://code.claude.com/docs/en/skills)。

## 每人初始化

进入**你们的团队项目 checkout**，执行：

```sh
gh auth login --hostname github.com
gh api user --jq .login
python3 .agents/skills/otoha-mailbox/scripts/mailbox.py init
python3 .agents/skills/otoha-mailbox/scripts/mailbox.py check --full
```

Claude Code 将命令中的 `.agents` 换为 `.claude`。Windows 可将 `python3` 换成 `py -3`。脚本放在别处时，使用 `python3 /path/to/mailbox.py --project /path/to/team-project check`。`--project` 必须放在子命令之前。

工具根据项目 `origin` 判断收件仓库，支持 `https://github.com/OWNER/REPO.git` 和 `git@github.com:OWNER/REPO.git`。**每人应克隆同一个团队仓库，不要各自用个人 fork 作为邮箱 origin。** 发信示例中的 `--repo` 也必须指向同一个仓库。本版本只支持 github.com。

默认 `init` 不安装 Hook；兼容 `init --manual`。没有新消息时 `check` 也可能列出历史入口，`new_count` 表示本次变化数，不是严格未读数。首次会扫描仓库 Issues 和评论，大型仓库可能较慢；它面向小团队。

## 醒来后补看历史和任务

`check --full` 不使用增量时间游标，会遍历 GitHub 的全部分页，包括已关闭 Issues；按 @、指派、创建、参与和本机曾关注关系找到话题，保留这些话题的完整正文和所有评论。自己写的内容也保留为上下文。没有最近 20 条/100 条的结果上限。

输出提供 `index_file`，索引内的 `assigned_open_issues` 列出当前指派给本人的未关闭 Issues，`threads` 指向每个话题的完整 JSON 文件。Agent 必须逐个读完；导出成功不等于已阅读。历史导出位于 Git common dir 中的 `otoha-mailbox/history/`，不要提交或作为公开附件上传。每次生成独立报告，不覆盖旧报告，也不消耗 Hook 通知或标成已读。

分页失败、无权限或评论数量少于 Issue 报告值时命令会报错，不输出成功报告。范围是当前项目中该账号可访问、GitHub 仍保留的 Issues 和评论；不含 PR 审查、其他仓库、已删除文本、被改掉的旧版本或已不可见的过去指派记录。API 扫描不是原子快照，期间若有人编辑/删除消息可能需要重试；新消息在后续查询补收。

## 可选：Codex 工作间隙自动查收

```sh
python3 .agents/skills/otoha-mailbox/scripts/mailbox.py init --codex-hooks
```

它为当前 checkout 生成 `.codex/hooks.json`，保留其他 Hook，并在本地 Git exclude 中排除配置与备份。需在本人客户端审核并信任，安装成功不等于已触发。已跟踪或符号链接形式的 Hook 文件不会自动修改。

脚本每 60 秒最多向 GitHub 检查一次，无消息时不追加上下文、不调用模型；自动请求使用 10 秒网络预算。Hook 由工作事件触发，空闲时不会定时唤醒 Agent，不保证即时送达。首次可先手动 `check`。客户端支持与信任要求见 [Codex Hooks 官方说明](https://learn.chatgpt.com/docs/hooks)。其他 Agent 的 Hook 不在此版本安装范围。

## 日常命令与本机状态

以下命令都加 `python3 SKILL_DIR/scripts/mailbox.py --project PROJECT` 前缀：

| 命令 | 行为 |
| --- | --- |
| `check` | 最近活动摘要，最多 20 条入口，排除自己的发言和 PR |
| `check --full` | 全部分页扫描，导出相关 Issue 正文/全部评论和目前指派给本人的未关闭任务 |
| `doctor` | 显示初始化身份、开关、最近成功/错误与 Hook 登记；不是在线连通性测试 |
| `pause` / `resume` | 暂停/恢复 Hook 查收，手动查询可用 |
| `uninstall-hooks` | 移除当前 checkout 中本工具登记的 Hook，保留其他配置 |

状态位于 `git rev-parse --git-common-dir` 下的 `otoha-mailbox/state.json`，linked worktree 共用。缓存含 GitHub 返回的 Issue 正文，不要上传整个 `.git` 或将状态当共享通讯录。每个账号使用自己的 clone；切换账号时脚本会停止查询，避免混用身份。没有来自旧游戏项目缓存的自动迁移。

移动/删除 Skill 路径前先 `uninstall-hooks`，需要自动查收时在新位置重新初始化。升级只替换 Skill，不应删除本机状态。发送/回复使用原生 GitHub CLI，完整原件保存在 GitHub。
