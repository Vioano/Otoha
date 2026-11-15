# 工作页换版保留阅读状态（v1）

工作页替换 opaque iframe 时，宿主与子页必须同时支持本协议。发布文档显式声明 `view_state: {version: 1, topic: 'stable-topic'}`；同一话题后续版本沿用 topic，不同话题使用不同 topic。宿主仅在当前浏览器内存中按 **team + topic** 保存，不跨团队、话题或成员同步，不持久保存表单草稿。

## 子页采用

把 [view-state.js](../assets/view-state.js) **内联到最终 HTML**，在页面自己的初始渲染完成后安装：

```js
const disposeViewState = window.OtohaViewState.install();
```

gzip 打包页必须把适配器放在解压、`document.write` 后的真正内容文档中。不能装在随后会被替换的解包外壳。先完成页面初始渲染再安装；适配器等待 DOM、字体及两帧布局后发送 ready。页面有额外异步渲染时，作者应等该渲染完成再安装。卸载页面组件时可调用返回的 dispose。

默认主题读写 `document.documentElement.dataset.theme`，只接受 `dark` / `light`。采用 class 等其他主题机制的页面可传 `readTheme()` 与 `applyTheme(theme)`，接入页面现有主题函数；不要求改变整个网站的主题机制。

多视图页面可显式提供固定视图 ID：

```js
window.OtohaViewState.install({
  readViewId: () => currentViewId,
  applyViewId: id => showView(id) // 可返回 Promise；完成后再恢复布局状态
});
```

`currentViewId` 必须来自页面固定的视图集合；`showView` 应验证 ID，未知 ID 留在安全的默认视图。它们不能编码筛选文本、输入值、HTML 或任意应用对象。给需要保留的 `<details>` 和焦点目标分配跨版本稳定且唯一的元素 `id`，同一个 ID 始终指同一个概念。缺失 ID 不按文案或位置猜测。独立静态页 `window.parent === window` 时安装为 no-op，不发消息，不使用存储。

## 消息与恢复顺序

所有消息带 `version: 1`。子页只接受 `event.source === window.parent` 的消息；opaque origin 下使用 `postMessage(message, '*')`，不能依赖同源存储或父页 DOM。

| 方向 | type | 载荷 |
| --- | --- | --- |
| 子页 → 宿主 | `otoha:view-state:ready` | 最终文档可接收恢复 |
| 宿主 → 子页 | `otoha:view-state:capture` | `request_id` |
| 子页 → 宿主 | `otoha:view-state:snapshot` | 原 `request_id`、`state` |
| 宿主 → 子页 | `otoha:view-state:restore` | `state` |

白名单 state 为 `theme`、`details: [{id, open}]`、`scroll: {x, y}`、`focus_id: id | null`，可选 `view_id: id | null`。ID 长度 1–256，details 最多 1000 个且 ID 不重复，坐标为非负有限数；非法状态不应用，额外字段不转发。未提供有效主题时不生成可恢复快照。

恢复先等待 `applyViewId`，再应用主题及现存 DETAILS 的 open；字体与两帧布局后恢复滚动，最后 `focus({preventScroll: true})`。页面须给异步图片等可能改变布局的资源预留尺寸；两帧等待不能保证任意未来网络内容稳定。适配器不读取或写入表单 value、文本、HTML、任意对象；焦点恢复只使用元素 ID。

## 验证

仓库正常轻量入口：`node --test test/test_view_state_adapter.mjs`。测试验证消息来源、版本、白名单、恢复顺序、卸载和静态页行为。最终验收还须在真实宿主中发布两版并观察视图、展开、滚动、主题和焦点恢复；Node 测试不证明浏览器布局或所有页面的适配正确。

已接入现有仓库测试入口 `npm run test:view-state` 和 `npm test`；宿主生命周期回归位于 CLI 的 `TestPageHostViewStateLifecycle`，随既有 Go CLI 测试运行。

反例是“发布完全相同 HTML 的新 revision，就应自动保留阅读状态”：宿主重建的是新 iframe，未适配的页面仍会回到默认主题、展开、滚动和焦点。正例须同时采用宿主和子页协议，并实际比较换版前后同一个具名视图、展开集、滚动地标与焦点；另用不同 topic 验证不会套用旧状态。可以把一个无敏感信息的测试复选框作为边界哨兵，确认阅读状态恢复没有顺带保存表单值。旧失败保留，不能因代码测试通过追认旧页面已恢复。
