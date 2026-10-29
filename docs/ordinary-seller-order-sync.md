# 普通卖家订单同步兼容

## 行为

商家工作台已售列表在 HTTP 成功响应中明确返回 `PERMISSION_EXCEPTION` 时，使用普通卖家 `mtop.taobao.idle.trade.sold.get` 5.0 版读取所有状态。其他 HTTP、网络、登录失效和风控错误沿用原分类，不作为回退条件。

回退请求使用普通卖家页面来源，并沿用既有签名和响应 Cookie 会话。首个权限拒绝之后，本次分页和 Token 重试固定普通接口；游标直接使用平台 `lastEndRow`，不按每页数量推算。同步中的接口选择和游标由调用方局部变量拥有，没有账号间共享状态。

每条记录必须包含订单号、商品号和与实际请求一致的卖家身份。JSON 长数字保持原始精度；金额取订单总额，不能用商品单价替代。状态不明时保持 `unknown`；待刀成、待成团不会作为普通待发货处理。

普通接口的可见历史范围不能证明未返回订单已经不存在，因此只合并返回记录，不软删除本地缺失记录。正常商家完整列表的既有行为保持不变。

## 验证记录（2026-10-29）

- 同一普通账号对照读取：商家接口的 `json` 与 `originaljson` 均被拒绝，普通接口成功返回已完成订单。修改仅请求参数不足以解决此次问题。
- 修复后的正式客户端已验证读取、身份校验、金额解析和有限范围标记。
- 应用容器更新后，正式刷新任务返回 `succeeded`、失败数0、`partial_failure=false`；订单已写入且页面可以查看，三个已接入账号仍在线。未执行发货、发卡或测试消息。
- 应用使用本机镜像 `ydisks-xianyu-helper:1.0.14-orders-fix1`，保留原正式镜像的 Chromium 与系统依赖。数据库及旧环境已在本机私有目录备份，部署说明包含回退原则。

## 自动检查

- 定向：`go test ./internal/xianyu/mtop ./internal/application/orders ./internal/adapter -run 'Sold|Standard' -count=1` 通过，包含归属拒绝、异常信封、传输及取消、历史保留和游标衔接。
- 全量 Go：Linux Go 1.26.5 执行 `go test -coverprofile=/reports/cover-linux.out ./...` 通过，statement 90.5%。未设置 `RUN_BROWSER_INTEGRATION=1`；在无外网的独立测试容器运行，避免宿主机 Fake-IP DNS 把 `.invalid` 保留域名解析成可连接地址。后续补充的身份拒绝用例也已定向通过。
- 浏览器：独立测试环境包含源码、Go工具链及正式镜像同版 Chromium。以 `RUN_BROWSER_INTEGRATION=1 /verification/browser-linux.test -test.coverprofile=/verification/cover-browser-linux.out` 运行通过，statement 52.6%；全部页面为本地测试页，没有真实账号验证操作。
- 前端：Linux原生临时目录执行 `npm run test:coverage -- --reporter=dot`，105个文件、642项测试通过，statement 92.71%。前端业务代码没有改动。
- Go架构、Go中文注释、受影响包 `go vet` 与 `git diff --check` 通过。
- OpenAPI严格生成检查在Linux、使用Git中规范的LF产物时通过；本次没有修改规范或生成类型。Windows工作区的CRLF会令原脚本的逐字节比较失败，已另行确认生成内容除换行外一致。
- 原有前端中文注释检查仍有1项：`frontend/app/features/accounts/qrLogin.test.tsx:207` 的类型成员 `signal` 缺少中文注释。该文件与本次修复前完全一致，未通过改动门禁或白名单隐藏。

没有修改HTTP/OpenAPI、数据库schema、前端业务或冻结的滑块实现。真实多页历史范围仍由平台决定，不能将单账号成功外推为任意账号、任意历史数据都可读取。
