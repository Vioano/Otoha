# Otoha Graph

Otoha 的工程图谱与人机共享视图组件。保留稳定 ID、接受版本、有界查询、独立任务看板、签名团队变更、历史与恢复；人类界面和 Agent 接口读取同一份接受模型。

从仓库根目录运行：

```sh
npm run setup
node bin/otoha.mjs graph preview packages/graph/examples/service.system.json --repo-root packages/graph
node bin/otoha.mjs graph manifest packages/graph/examples/service.system.json
node bin/otoha.mjs graph query packages/graph/examples/service.system.json --mode overview
```

manifest/query 读取已接受状态，先保持 preview 运行；也可读取已保存的接受历史。独立运行可用 `node packages/graph/bin/otoha-graph.mjs`。

组件内运行 `npm test` 执行维护中的回归测试，`npm run test:browser` 是额外的浏览器检查入口。查看 [SKILL.md](SKILL.md)、[查询契约](references/agent-interface.md)、[任务模型](references/task-board.md)及[团队协议](references/collaboration.md)。

图谱和看板不自动调度或唤醒 Agent。任务完成不提升模块成熟度；loopback 预览不是已认证的多人托管服务。深浅主题、视角与草稿保留沿用既有交互。

渲染基础来自 Archify；相关许可和署名保存在 [LICENSE](LICENSE)，迁入来源见根目录 [NOTICE.md](../../NOTICE.md)。Otoha 的完整设计依据见[五项核心哲学](../../docs/PRINCIPLES.zh-CN.md)。
