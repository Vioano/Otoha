# 需求、实例、反馈与决策

`transport-mvp-objects.json` 是当前最薄的对象关系样本。它把四类对象和 Otoha Graph 的稳定实体 ID 连接起来：

```text
requirement → instance → feedback → decision → shared state
       ↑                                      │
       └────────────── revision ─────────────┘
```

它表达的是设计和对齐记录，不代表需求管理系统已经完成。Skill 负责生成和整理这些对象；Graph 负责展示关系、证据和状态；后续 CLI 或协作服务再提供持久化与查询命令。
