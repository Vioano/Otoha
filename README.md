# Otoha

面向复杂项目的人与多 Agent 协作基础设施。

Otoha 是新的项目名称，后续开发在本仓库继续。它将基于 System Atlas、Atlas CLI 和 Team Mailbox 的已有工作，连接系统理解、项目协作与 Agent 执行。旧项目中的实现可以复用，但各自的产品边界和部署方式仍需根据 Otoha 的目标重新判断。

## 文档

- [核心原则](docs/PRINCIPLES.zh-CN.md)：设计讨论中的明确要求与原文核对状态。
- [已有基础与复用入口](docs/FOUNDATIONS.zh-CN.md)：三个旧项目的源码位置、固定版本和能力边界。
- [架构起点](docs/ARCHITECTURE.zh-CN.md)：共同模型、协作协议、消息投递和运行适配之间的关系。
- [开发起点](docs/DEVELOPMENT.zh-CN.md)：首个场景建议、验收方式和尚待决定的问题。

## 当前状态

仓库处于初始化阶段，已经建立文档和旧项目的版本索引。尚未导入三个项目的实现，也没有可运行的 Otoha 产品或已完成的集成验证。文档中的架构和首个开发场景是后续设计的起点。

## 项目来源

- [System Atlas](https://github.com/Vioano/system-atlas)：共享系统模型、人类视图与 Agent 查询。
- [Atlas CLI](https://github.com/Vioano/atlas-cli)：项目接入、持续收信与 Agent 会话适配。
- Team Mailbox：跨成员、跨 Agent 的协作协议与 GitHub 收信工具；本机已有源码，原远端地址的可访问性见[复用记录](docs/FOUNDATIONS.zh-CN.md)。

本仓库公开保存通用设计与代码。个人凭据、邀请密钥、真实团队消息、运行日志及私有服务配置保存在各自的私有存储中。
