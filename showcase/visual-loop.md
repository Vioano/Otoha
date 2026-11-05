# Showcase visual loop

这次改版按“真实页面 → 参考 → 生图方向 → 真实实现 → 浏览器对照”的循环完成。

## 输入

- 当前页面：`showcase/index.html`，通过本地 `http://127.0.0.1:4173/showcase/` 截图。
- Raycast：命令面板的键盘优先层级与简洁结果行。[参考图](https://user-images.githubusercontent.com/12066405/89204337-d7838280-d5ad-11ea-8f36-2a9229f1988d.png)
- Linear：高密度任务信息、稳定对齐和侧栏层级。[参考图](https://ui.aceternity.com/linear.webp)
- Homeworld: Deserts of Kharak：地图、信号路径和实时状态 HUD。[参考图](https://media.rawg.io/media/screenshots/2d4/2d49e054961058bb47e669102686b4e8.jpg)

## 生图方向

`assets/otoha-redesign-prototype.png` 是把当前页面截图与上述三张参考一起交给 imagegen 后得到的构图方向。它只作为材质、比例和光线参考；原型中的标题、按钮、Agent 标签和数字没有直接作为产品数据使用。

## 实现与验证

- 左侧使用真实 DOM 文案、按钮、指标和主题切换。
- 右侧使用生成图的共享状态核心和信号地图作为视觉底材。
- 六分钟故事分成八个阶段：PLAN、WRITE、SYNC、ROUTE、READ、ACK、GRAPH、ALIGN。
- `Start 6-minute story` 默认进入手动模式；讲解者用 Prev、Next、Play、滑杆或键盘左右键控制节奏，不自动跳过内容。
- Architecture 视图展示 Agent、Otoha CLI、渠道、权威消息数据库、Roster + ACL、本地 inbox/outbox 和成员投影之间的关系。
- 每个阶段会更新阶段卡、时间线、事件数、回执数、进度和发光数据包。
- 浏览器实际验证了手动 Start、Next、Architecture 打开/关闭、compact demo、深浅主题和阶段推进；浏览器日志没有 error 或 warning。
