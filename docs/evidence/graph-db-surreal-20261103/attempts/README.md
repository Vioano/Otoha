# 初始失败记录

保留原始失败输出，最终探针和平台结果另存，不覆盖这些文件。

| 记录 | 原因 | 后续处理 |
| --- | --- | --- |
| [macos-harness-result-index.json](macos-harness-result-index.json) | 探针把事务尾部的 COMMIT 空响应算入分组结果；数据库实际返回了正确分组 | 按实际响应边界取出结果，修正探针 |
| [macos-native-rewire.json](macos-native-rewire.json) | 原生 UPDATE 返回 OK，但边端点未改变 | 明确记录后端限制，另验同一事务内删除/同 ID 重建 |
| [linux-native-rewire.json](linux-native-rewire.json) | Linux 上观察到同样的原生端点更新行为 | 同上；不是平台特例 |

这些是开发过程中的输入/探针版本，fixture 哈希可在各文件核对；不对应最终探针哈希。最终版本见上一级的 [macOS](../macos-arm64.json)和 [Linux](../linux-x86_64.json)记录。
