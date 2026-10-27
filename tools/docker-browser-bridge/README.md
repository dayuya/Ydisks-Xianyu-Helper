# Docker 人工验证接入实验

这是独立于 Ydisks 的本机实验，已于 2026-10-26 完成一次真实账号人工登录、会话同步与在线状态验收。

- 可视浏览器：http://127.0.0.1:19388
- CDP：127.0.0.1:19389，仅本机可访问。
- Ydisks：http://127.0.0.1:19288
- 独立 Compose 项目：ydisks-browser-poc；独立配置卷：ydisks-browser-poc_browser_profile。与本机实验目录使用同一个项目名，迁移命令路径不会新建第二套账号浏览器。
- 镜像：jlesage/chromium v26.09.2，Compose 固定镜像摘要；Chromium 152.0.7977.82。

## 验证方法

需要 Node.js 24、Docker Compose，以及已配置项目根目录 `.env` 的本机 Ydisks 部署。该部署应用端口为19288、PostgreSQL 容器为 `ydisks-xianyu-helper-postgres-1`，数据库名与用户名均为 `xianyu`；若更改部署布局，应先对应修改工具内固定的本机地址和容器参数。在项目根目录执行：

```powershell
docker compose -f tools/docker-browser-bridge/compose.yml up -d
node tools/docker-browser-bridge/bridge.mjs check
node --test tools/docker-browser-bridge/bridge.test.mjs
node tools/docker-browser-bridge/bridge.mjs apply <目标账号ID>
```

保持容器浏览器只有一个标签页，运行 apply 后，在该浏览器打开闲鱼官方消息页并由本人扫码、人工验证。工具最多观察 15 分钟；不会拖动滑块，也不会反复触发平台登录。

仅在官方消息登录接口返回 SUCCESS 且包含消息登录凭据时检查账号身份。身份匹配、必要会话字段完整后，调用已有 Ydisks 更新接口。读取消息接口域和首页域，保留消息域特有的验证信息；不把秘密打印或写入报告。管理登录仅指向本机现有后台。

apply 启动时只读取目标账号数据库的非敏感版本号，提交时由 Ydisks 执行原子版本检查。若返回 LOCAL_API_409，说明等待期间有更新，应重新核对现状，不能盲目重试覆盖。管理密码从本机 `.env` 读取；若已在后台修改管理员密码，需要按当前本机安全配置调整，不要在脚本内写死密码。

出现 ydisksSessionUpdated 只表示写入成功，必须另查目标账号 runtime-status 的 online 与 connected 才算接入成功。真实业务自动回复和发货不属于本次测试。

watch 模式只观察，不更新后台：

```powershell
node tools/docker-browser-bridge/bridge.mjs watch <目标账号ID>
```

暂停实验浏览器（保留配置）：

```powershell
docker compose -f tools/docker-browser-bridge/compose.yml stop
```

没有改动 Ydisks 自带的滑块实现、浏览器配置或业务服务。此工具是单账号可行性验证，不是后台原生接入，也未实现多个账号的独立浏览器调度。禁止把当前无外部认证的实验端口公开到网络。

## 已核验

2026-10-26：容器启动、可视界面、中文显示、官方消息页扫码窗口、CDP 会话读取通过；五个本地用例覆盖目标接口、官方成功语义、消息域验证信息保留、错误身份与不完整会话拒绝。

用户本人完成登录后，观察工具捕获官方消息接口成功响应，核对身份匹配，并经带版本检查的本机接口同步成功。Ydisks 实际运行状态为 online / connected=true，管理界面同样显示在线。旧后台目标账号仍为 disabled。

独立重启实验浏览器后，账号身份与登录会话保持一致；Ydisks 仍在线且两个业务容器健康。持久化验证工具为 check-persistence.mjs，只重启实验浏览器：

```powershell
node tools/docker-browser-bridge/check-persistence.mjs
```

测试时不要在该浏览器进行其他操作；重启会短暂中断可视页面。截图、浏览器数据和本机环境文件不应提交 Git。

这次成功登录时未观察到 x5sec，因此结果证明的是 Docker 可视浏览器人工登录、会话同步和接入可行；不能证明遭遇任意风控时都可通过，也不能宣称自动滑块已修复。尚未验证多账号隔离调度、长期运行、真实消息回复或发货。
