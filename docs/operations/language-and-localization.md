# 语言选择与本地化

Nginx UIX v1.1.0 提供简体中文（`zh-CN`）和英语（`en-US`）界面。登录页与登录后的全局导航都提供语言选择器，切换后立即生效，不需要刷新页面。

## 选择优先级

页面启动时按以下顺序确定语言：

1. 当前 URL 中有效的 `lang` 参数；
2. 浏览器 `localStorage` 中的 `nginx-uix.locale` 偏好；
3. 浏览器报告的语言顺序；
4. 默认 `en-US`。

只接受精确的 `?lang=zh-CN` 与 `?lang=en-US`。URL 参数有效时具有最高优先级；参数缺失或无效时，页面会按后续规则选择语言，并把 URL 规范化为受支持的值。

浏览器语言以语言族归一化：`zh` 及 `zh-*` 使用 `zh-CN`，`en` 及 `en-*` 使用 `en-US`，其他语言最终回退到 `en-US`。

示例：

```text
https://admin.example.test/login?lang=zh-CN
https://admin.example.test/config/workspaces?lang=en-US
```

## URL、登录与持久化

- 语言选择器会替换当前 URL 的 `lang`，保留路径、其他查询参数和 fragment。
- 站内导航持续携带当前 `lang`；未登录跳转到登录页以及登录后的返回地址也保留它。
- 每次确认语言后只把 `zh-CN` 或 `en-US` 写入 `localStorage` 的 `nginx-uix.locale`。
- 语言偏好不是凭据。Session 仍只保存在 `HttpOnly` Cookie 中，不写入 `localStorage`、`sessionStorage`、Cache Storage 或 IndexedDB。
- 浏览器禁用或拒绝 Web Storage 时，当前页面仍可切换语言，只是不跨新会话保存偏好。

## 翻译边界

第一方导航、表单、ARIA 标签、Toast、Banner、Modal、状态说明和安全确认会随语言切换。日期与数字也按当前 locale 格式化。

为了避免误改运维证据，下列内容保持原样，不执行机器翻译：

- Nginx 配置正文、指令、文件路径、域名、URI 和 header；
- workspace、release、backup、task、request 等标识符；
- API error code、枚举值、Nginx/ACME/Cloudflare 返回的原始诊断；
- 用户输入以及后端提供的其他技术数据。

本地化错误说明不会替代诊断标识。存在 request ID 时，界面会继续显示它，便于在服务端日志中关联问题。
