# SurrealDB 最小契约探针

这是候选后端实验，不接入现有 Otoha CLI 或用户状态。选型理由、结果及限制见[调研记录](../../docs/GRAPH-DATABASE-EVALUATION.zh-CN.md)。

依赖：Python 3.10+ 标准库、SurrealDB **3.3.5** 的原生数据库二进制。下载后的数据库路径由调用者传入；脚本不安装软件，不注册服务。它只使用随机测试凭据和 `127.0.0.1`，正常结束或断言失败都会停止自己启动的进程并删除临时数据。输出保留全部查询和响应，退出码 0 表示预期检查通过。

```sh
python3 experiments/graph-db-surreal/probe.py \
  --binary /absolute/path/to/surreal \
  --output /tmp/otoha-surreal-result.json
```

3.3.5 发行包：

| 平台 | 官方下载 | 压缩包 SHA-256 |
| --- | --- | --- |
| macOS ARM64 | [surreal-v3.3.5.darwin-arm64.tgz](https://github.com/surrealdb/surrealdb/releases/download/v3.3.5/surreal-v3.3.5.darwin-arm64.tgz) | `534140236e7246d86969015ba9ae8dd2f7f8f277ed1412cb17f3d397f95a1090` |
| Linux x86_64 | [surreal-v3.3.5.linux-amd64.tgz](https://github.com/surrealdb/surrealdb/releases/download/v3.3.5/surreal-v3.3.5.linux-amd64.tgz) | `8f3bc163405085343fc6db9746a8e0cd55503c939802ad2b35a466675a9c09cf` |

SHA-256 来自本轮 GitHub release API 的 asset `digest`。不要将此版本写成最新版本；调研时 4.0.0 已发布，未使用本探针验证。

输入是五个节点、五条边。28 项检查包含结构与属性、失败回滚、条件拒绝、分组与来源、假路径反例、过期展开拒绝、边重连差异、删除差异、正常重启和导出导入。独立的有限图可达性函数仅作为反例校验，不是新图引擎。

限制：记录的查询耗时来自极小样本，不能用于吞吐或容量比较。没有验证真实并发、幂等回执、完整历史、千级查询、Template 约束、Windows 或真实用户迁移。使用其他版本前应先检查并更新预期行为；尤其不要用 `OK` 响应代替端点读回和遍历验证。
