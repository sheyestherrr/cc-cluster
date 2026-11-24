# 来源与许可

这个目录是 cc-cluster 的网页界面。它的界面风格、设计变量、基础组件和页面骨架取自
[Claude-Hydra](https://github.com/jin-yu-liu/Claude-Hydra) 项目的前端（`app/src`，v1.1.6，提交 `664f8210`），在此感谢。
Claude-Hydra 以 MIT 许可发布，许可全文附在本文件末尾。

取用与改动的范围：

| 本目录的文件 | 上游文件（`app/` 下） | 改动 |
| --- | --- | --- |
| `tailwind.config.cjs`、`tsconfig*.json`、`vite.config.ts` | 同名文件 | 去掉 Tauri 相关设置，产物输出到 Go 的嵌入目录；升级到 Tailwind 4 后样式由它的 vite 插件处理，上游的 `postcss.config.cjs` 不再需要，已删除 |
| `src/index.css` | `src/index.css` | 为 Tailwind 4 改了入口写法和三处基础样式，见文件开头的说明 |
| `src/lib/utils.ts`、`src/lib/theme.ts` | 同名文件 | 时间显示改为接受 ISO 时间；本地存储的键改名 |
| `src/components/ui/*.tsx`、`src/components/Menu.tsx` | 同名文件 | 原样或只改了注释 |
| `src/components/PageHeader.tsx` | `src/components/PageHeader.tsx` | 页头与分组原样；内容区加了“列表 + 右侧详情栏”的两栏布局和可选中的行，这部分是本项目自己写的 |
| `src/components/AppShell.tsx` | `src/components/AppShell.tsx` | 去掉窗口控制、更新提示和应用图标，导航项换成本项目的 |
| `src/pages/SlotsPage.tsx` | `src/pages/AccountsPage.tsx` | 沿用列表行与菜单的结构，内容换成槽位 |
| `src/pages/NetworkPage.tsx` | `src/pages/EnvironmentsPage.tsx`、`src/components/EnvironmentDialog.tsx` | 沿用列表与表单的结构，内容换成网关与节点 |
| `src/pages/SubsPage.tsx`、`src/components/RowShell.tsx` | 同上（经 `NetworkPage.tsx`） | 订阅原先是 `NetworkPage.tsx` 里的一组，后来单独成页；列表行的组件一起搬了出来 |
| `src/pages/SystemPage.tsx` | `src/pages/SettingsPage.tsx`、`src/pages/AboutPage.tsx` | 沿用分组与键值行的结构 |

`src/lib/api.ts`、`src/App.tsx`、`src/components/ExitCard.tsx`（出口评估卡片）、`src/components/NoteDialog.tsx`（备注的显示与编辑）、各页面右侧的详情栏和各对话框的业务逻辑是本项目自己写的：上游经 Tauri 调用 Rust 后端，
这里改为经 HTTP 调用 cc-cluster 的 Go 接口（`internal/web`）。上游的图标和名称没有使用。
订阅的拉取与解析在 Go 一侧完成（`internal/subscription`），没有取用上游的 `clashNodes.ts`。

`src/components/Flag.tsx`（国旗与行首的地区图标）、`src/favicon.svg` 也是本项目自己写的。国旗的图形来自依赖
[country-flag-icons](https://gitlab.com/catamphetamine/country-flag-icons)（MIT）的 3x2 SVG，构建时原样放进产物。

图表（`src/components/charts.tsx`）用依赖 [recharts](https://github.com/recharts/recharts)（MIT）画；`src/components/SlotTraffic.tsx`（槽位的流量与连接）
和 `src/lib/live.ts`（定时取数）是本项目自己写的。

订阅页“订阅列表 + 节点列表 + 筛选栏”和总览页（`src/pages/OverviewPage.tsx`，本项目自己写的）“指标卡 + 分布图 + 最近动态”的安排
参考了 [Resin](https://github.com/Resinat/Resin)（MIT）的订阅页、节点池页与看板页，
没有取用它的前端代码；分享链接的解析取自它的 Go 代码，来源与许可见 `internal/subscription/NOTICE.md`。

## Claude-Hydra 的许可（MIT）

```
MIT License

Copyright (c) 2026 Claude-Hydra contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
