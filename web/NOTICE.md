# 来源与许可

这个目录是 cc-cluster 的网页界面。它的版面、配色、字体和样式取自
[Resin](https://github.com/Resinat/Resin) 项目的前端（`webui/`，提交 `8e9f74e`），在此感谢。
构建配置和几个基础组件的行为沿用自 [Claude-Hydra](https://github.com/jin-yu-liu/Claude-Hydra)
的前端（`app/src`，v1.1.6，提交 `664f8210`），同样感谢。两个项目都以 MIT 许可发布，许可全文附在本文件末尾。

## 取自 Resin 的部分

| 本目录的文件 | 上游文件（`webui/src/` 下） | 改动 |
| --- | --- | --- |
| `src/styles/resin.css` | `styles/theme.css` | 原文件，不在里面改 |
| `src/styles/resin-switch.css` | `components/ui/Switch.css` | 原文件 |
| `src/components/AppShell.tsx` | `components/AppShell.tsx` | 沿用侧栏加内容区的结构与样式类；导航项、标志和底部的状态换成本项目的 |
| `src/components/ui/card.tsx`、`button.tsx`、`input.tsx`、`switch.tsx` | `components/ui/` 下的 `Card.tsx`、`Badge.tsx`、`Button.tsx`、`Input.tsx`、`Select.tsx`、`Textarea.tsx`、`Switch.tsx` | 沿用样式类；属性与变体的名字按本项目原有的写法 |
| `src/components/ui/dialog.tsx`、`toast.tsx`、`src/components/Menu.tsx`、`PageHeader.tsx`、`Form.tsx` | 样式表里的 `modal-*`、`toast-*`、`module-header`、`form-grid`、`field-group` 等 | 只用了样式类，组件是本项目写的 |
| `src/pages/OverviewPage.tsx`、`src/components/charts.tsx` | `features/dashboard/DashboardPage.tsx` | 沿用“页头 + 指标卡 + 面板网格”的版面，和趋势图的样子（带框的画布、虚线网格、平滑曲线、下行蓝色上行绿色、提示框）；数据与内容是本项目的 |
| `src/pages/SlotsPage.tsx`、`NetworkPage.tsx`、`SubsPage.tsx` | `features/platforms/PlatformPage.tsx`、`PlatformDetailPage.tsx`，以及订阅页、节点池页 | 沿用“卡片网格 + 带页签的详情页”的版面、筛选栏和数据表的样式；内容是本项目的 |
| `src/pages/SystemPage.tsx`、`src/components/ExitCard.tsx`、`SlotTraffic.tsx` | 样式表 | 只用了样式类 |

`src/styles/app.css` 是本项目自己的样式，写在 Resin 的样式表之上：补了它没有、这里需要的几个部件，沿用它的圆角、描边和字号。
Resin 的图标、名称和接口代码没有使用。分享链接的解析取自它的 Go 代码，来源与许可见 `internal/subscription/NOTICE.md`。

## 取自 Claude-Hydra 的部分

| 本目录的文件 | 上游文件（`app/` 下） | 改动 |
| --- | --- | --- |
| `tailwind.config.cjs`、`tsconfig*.json`、`vite.config.ts` | 同名文件 | 去掉 Tauri 相关设置，产物输出到 Go 的嵌入目录；升级到 Tailwind 4 后样式由它的 vite 插件处理；颜色与字体改为对齐 Resin |
| `src/lib/utils.ts` | `src/lib/utils.ts` | 时间显示改为接受 ISO 时间，另加了几个格式化函数 |
| `src/components/ui/dialog.tsx`、`toast.tsx`、`src/components/Menu.tsx` | 同名文件 | 开合、叠放与关闭的行为沿用；外观换成 Resin 的 |

界面最初的风格和页面骨架也取自 Claude-Hydra，后来整体换成了 Resin 的版面与样式。

## 本项目自己写的部分与其他依赖

`src/lib/api.ts`、`src/lib/live.ts`（定时取数）、`src/App.tsx`、`src/components/NoteDialog.tsx`、`src/components/Flag.tsx`、`src/favicon.svg`，
以及各页面的内容、各对话框的业务逻辑是本项目自己写的：页面经 HTTP 调用 cc-cluster 的 Go 接口（`internal/web`）。
订阅的拉取与解析在 Go 一侧完成（`internal/subscription`）。

- 图表用 [recharts](https://github.com/recharts/recharts)（MIT）画。
- 国旗的图形来自 [country-flag-icons](https://gitlab.com/catamphetamine/country-flag-icons)（MIT）的 3x2 SVG，构建时原样放进产物。
- 字体是 [Sora](https://github.com/sora-xor/sora-font) 与 [Manrope](https://github.com/sharanda/manrope)（都是 SIL OFL 1.1），
  经 [Fontsource](https://fontsource.org) 的 `@fontsource/sora`、`@fontsource/manrope` 打进产物，不从外面加载；中文用系统字体。

## Resin 的许可（MIT）

```
MIT License

Copyright (c) 2026 Resinat and contributors

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
