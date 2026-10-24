# Otoha 组件与源码入口

通用代码已复制到本仓库，并以 Otoha 命名。源提交、原始路径、文件哈希及排除范围保存在 [import-manifest.json](provenance/import-manifest.json)，原项目名称仅用于[来源记录](provenance/foundations-before-import.zh-CN.md)和版权署名。

| 组件 | 目录 | 能力与入口 |
| --- | --- | --- |
| Otoha Graph | [packages/graph](../packages/graph/README.md) | 共享接受模型、有界图查询、任务看板、视图、签名协作、历史与恢复；`design/`、`team/`、`schemas/` |
| Otoha Collaboration | [services/collaboration](../services/collaboration/README.md) | Python/SQLite 协作服务、身份、频道、查询投影、回执、stdio MCP、接入与会话桥接 |
| Otoha CLI | [apps/cli](../apps/cli/README.md) | 原生 Go 客户端、项目接入、消息接收、Git 代理、连接路由与宿主适配 |
| Otoha Mailbox | [integrations/mailbox](../integrations/mailbox/README.md) | GitHub 收信、完整历史导出、可选 Hook、Agent 操作指引和安装工具 |

根目录的 `bin/otoha.mjs` 提供源码统一入口。上述组件已在同一仓库可独立构建和测试，但工程图谱与消息库仍各有自己的职责；五项哲学中的分级通知、持续对齐、需求实例关联和自适应调度尚未全部实现。

## 移植边界

本轮迁入通用代码和相关测试，不迁移旧项目的真实凭据、邀请、状态数据库、团队消息、已安装 receiver 或生产服务。Otoha 的环境变量、默认目录、签名域和邀请前缀使用独立名称；既有生产状态不会因构建新代码被接管。

协作服务原目录包含私服、桌面与网络运维材料，以及尚未提交的存储工作。本轮只迁入协作服务与通用接入工具，另保留一个旧服务源码作为数据库升级回归夹具；未迁入机器专用配置、运维证据或私有状态。全部原目录和未提交修改保留原状。

## 许可与验证

保留各组件适用的原许可和版权署名，见 [NOTICE.md](../NOTICE.md)。具体测试结果、构建环境与未验证范围见[移植验证记录](MIGRATION.zh-CN.md)。
