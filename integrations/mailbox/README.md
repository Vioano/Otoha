# Otoha Mailbox

GitHub 收信与协作指引组件。Issues 保存话题，收信脚本导出当前身份可访问的相关历史，Agent 在已有授权范围内决定后续处理。脚本只读，不发送消息，也不唤醒空闲 Agent。

从本目录安装到一个已有 Git 项目：

```sh
python3 scripts/install.py --project /PATH/TO/PROJECT
```

默认安装到 `.agents/skills/otoha-mailbox`；Claude Code 使用 `--agent claude`。安装不登录、不发送消息、不修改项目规则，默认不开 Hook。接入后阅读 [Skill](skills/otoha-mailbox/SKILL.md) 和[协议](skills/otoha-mailbox/references/protocol.md)。

测试：

```sh
python3 -m unittest discover -s skills/otoha-mailbox/tests -v
python3 -m unittest discover -s tests -v
```

需要 Python 3.10+、Git 和 GitHub CLI。完整抓取不等于实际阅读；可选 Hook 的冷却间隔不等于持续收信时限。未安装到任何真实项目，也未启用后台接收。

[MIT 许可](LICENSE)，代码来源见根目录 [NOTICE.md](../../NOTICE.md)。
