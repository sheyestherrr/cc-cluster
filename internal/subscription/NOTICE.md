# 来源说明

`links.go`（base64 订阅与分享链接的解析）改写自 [Resin](https://github.com/Resinat/Resin) 的
`internal/subscription/parser.go`（提交 `8e9f74e`，MIT 许可）。

取用的是：格式识别的顺序（先看是不是 Clash 格式，再按行认分享链接，都不是时把整段当 base64 解开再试一次），
以及 `ss`、`vmess`（v2rayN、Shadowrocket、标准写法）、`vless`、`trojan`、`hysteria2`、`socks`、`http` 各种链接的拆法：
字段从哪里取、有哪些别名、宽松的 base64 解码、Shadowsocks 插件参数和 WebSocket 提前数据的处理。

改动的是：Resin 把节点转换成 sing-box 的 outbound，这里输出 Mihomo 的 proxy 片段，每种协议的结果和传输层的写法都是重写的；
名称里的转义按路径而不是查询串的规则解码；Shadowsocks 2022 的明文用户信息会做百分号解码；重名的节点加序号而不是丢弃。
Surge / Quantumult X 的代理行、`vmess1`、`ssd`、Telegram 与 Netch 链接、不带协议的 “IP:端口” 没有搬过来。

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
