# 配置与账号池恢复

2026-10-03。保留配置、账号授权及账号池的持续状态；日志、在途请求和可重建模型目录可排除。恢复验证不调用模型、刷新令牌或启动 scheduler/server。

## 需求与实现

- 以实际 LaunchAgent 的 WorkingDirectory、config 参数及 `WB2A_*` 覆盖定位配置，不能仅凭仓库默认路径。原 `cmd/server.Load` 负责默认值、JSON、env 与参数校验；优先用现有 `config.example.json` 重建无秘密配置。
- 保留原 `auth_dir` 全部账号文件和 `state_file`，以及实际配置的 prompt/device-token 文件引用。授权文件含 access/refresh token、realm、设备值和账号绑定，必须加密备份，文件 0600、恢复目录 0700，禁止输出或提交。目录采用已有 config_sync tree，新增账号随下次捕获纳入；不以固定单个文件代替整个目录。
- 状态文件原子写入，恢复完整 JSON 的积分、系统禁用、人工停用原因、冷却/熔断、连续失败与模型成本。原 Pool 会过滤过期冷却/成本、钳制无效 expiring 值并迁移旧错误计数；不把到期项复活，不将停用账号自动启用。
- 若配置了 Upstash，还需原生备份其持久状态及 TTL，并保存原本地文件时序以遵循 `RestoreFromSnapshot` 择新规则。当前通用文件恢复不保留原 mtime，不能用解密产生的新 mtime 选择旧状态；启用 Redis 的部署须先补时序恢复。本次实际配置未启用 Upstash，仅证明本地文件恢复。模型缓存可从原入口重建，缓存缺失不能自动触发上游验收。

## 重建与验收

1. 从原 Git 版本与锁定依赖构建，在新建空目标解密；将原配置、授权目录与状态对应同一批次，保持服务停止。
2. 核对私有 metadata 的路径和权限；仅在 QA 配置中重定位引用，原配置和源文件保持不变。恢复非空目录不直接覆盖已有授权或较新状态。
3. 原 Load/Auth.Parse/LoadDir 与 Pool.New 在禁网副本校验。LoadDir 可能给旧 realm 补标并写回，因此必须使用可写 QA 授权副本，且关闭其账号日志输出；不能直接用生产 auth_dir 做演练。缺失/坏授权可能被原 loader 跳过，需逐文件 Parse 并核对加载数量，空池不算恢复成功。
4. 重开两次须保留账号对应关系、凭据、人工/系统停用与持续计数；有时效的冷却/账本遵守原有效期。不执行 Pick/ReviveDisabled/签到/keepalive 或上游请求。
5. 授权仍有效及真实服务恢复另行验证；文件能解析不证明提供方接受令牌。按原部署与认证边界启动，保留原停用意图。

定向回归：`go test ./cmd/server -run 'Test(Config|Load|Restored|Recovery)'`、`go test ./internal/pool -run 'Test(Restored|.*Persist|.*Restore|.*Manual)'`。实际恢复用 `WB2A_RECOVERY_ROOT`、`WB2A_RECOVERY_STATE_FILE` 仅传隔离路径；缺引用明确跳过。原生 macOS sandbox 限写 QA/禁网，并设 `WB2A_RECOVERY_NETWORK_DENIED=1`；三项显式 race 检查必须通过且无跳过。测试只输出固定诊断，源授权值和账号标识不进入日志。

## 授权生命周期审计

授权 owner 是本机账号操作者；运行位置由私有配置引用，expiry/revocation 不可仅凭文件存在判断。现有 scheduler 的 refresh/keepalive 与原账号 disabled/session-dead 状态属于现有检测路径；本次未调用或证明该路径当前健康。

操作者已确认：沿用现有登录入口，由本人完成人工登录后恢复原账号池。按 README 选择原 realm，运行原 `login.sh` 并完成浏览器批准，再核对受控账号文件和原账号池读取；登录脚本包含启动/重启动作，仅在实际恢复窗口运行，不用于隔离演练。不得拿备份的旧 token 覆盖刚更新的授权；人工停用意图保持，需恢复自动禁用时沿用原管理入口。

目前尚未验收独立检查到 Hub/Pusher 的去重告警、人工恢复后的 incident 清除及新一轮故障重报，列入父工作区审计。后续复用原 scheduler/账号状态检测和 Hub/Pusher 事件合同，不另建登录或通知链；先测 rejection/无法检查、同一事件去重、人工恢复后健康清除与再次失效重报，再验收真实运行。

本次源授权目录 0755、文件/状态/配置 0600；未改变生产权限。目录收紧、周期激活、跨机解密密钥和离机副本仍需单独验收。
