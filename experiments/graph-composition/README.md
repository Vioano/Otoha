# 图引用的数据库边界实验

复用 SurrealDB 3.3.5 与[既有探针](../graph-db-surreal/probe.py)的 `Probe/SCHEMA`，验证同一 namespace 内 `enterprise`、`shop`、`pay` 三个数据库的存储表达与显式路由。它不接入真实 Otoha 项目状态。

```sh
python3 experiments/graph-composition/probe.py \
  --binary /absolute/path/to/existing/surreal \
  --output /tmp/otoha-graph-composition-result.json
```

需要 Python 3.10+ 标准库和已经取得的数据库可执行文件；脚本不下载、不安装、不注册服务。`importlib` 从仓库相邻目录加载共享 helper，不依赖早期 `/tmp` helper。它使用随机测试身份、认证后的 `127.0.0.1` 进程与独立临时目录，运行后停止该进程并删除临时数据库。

八项预期检查：

1. 企业图中两个普通节点均可保存同一逻辑图 ID，各有不同的显示属性。
2. 三个数据库中的 `node:shared` 保持各自内容；只有本地 ID 不能区分图。
3. 测试代码按显式 `GraphID → database` 映射切换后，能够读取目标图。
4. 支付图的目标对象不存在于企业库本地范围。
5. 企业库中的原生 `ENFORCED` 边不能自动找到仅存在于支付库的对象。
6. 被拒绝的边未落盘。
7. 删除一个企业图引用，不会删除另一个引用。
8. 删除企业图引用，不会删除支付库中的目标对象。

`p.ref` 和 `graph_id` 只是这个样本约定的普通 Properties，不是 Core 保留字段，也不是已经实现的引用类型。映射保存在测试代码中；“显式路由成功”不证明数据库自动支持跨库边。

本轮 [Linux x86_64 证据](../../docs/evidence/graph-composition-20261106/linux-x86_64.json)记录了 8/8 结果、全部查询/响应、脚本与共享 helper 的 SHA-256、数据库版本及二进制 SHA-256。二进制大小和一次进程启动耗时仅描述本次运行，不是吞吐、容量或性能比较。

未验证：Otoha Resolver、真实权限、历史版本固定、联合快照、跨库事务、联邦查询、图迁移、引用循环处理、真实用户状态或 Mac 复跑。通过这些检查只说明该小样本能以普通属性表示图引用，并由显式路由访问目标。
