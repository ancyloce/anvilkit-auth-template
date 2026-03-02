# GitHub Milestones & Issues（M4-M6 Email Service）

> 本文档定义邮件服务集成的里程碑和任务清单
>
> 约定 labels：
> - `milestone:4` / `milestone:5` / `milestone:6`
> - `area:email` / `area:auth` / `area:worker`
> - `type:feature` / `type:chore` / `type:test` / `type:ci`
> - `priority:p0` / `priority:p1` / `priority:p2`
> - `service:auth-api` / `service:email-worker`

---

## Milestones（最佳实践建议）

### Milestone 4: Email Service Core（邮件服务基础）
- **Title**: `M4 - Email Service Core`
- **Description**: 完成邮件服务基础设施：数据库表结构、Redis 队列、email-worker 服务、SMTP 集成，形成可发送邮件的最小闭环。
- **Success Criteria**:
  - email-worker 可从 Redis 队列消费邮件任务
  - 支持通过 SMTP 发送邮件（配置化）
  - 邮件发送状态可追踪（email_records + email_status_history）
  - 基础单测与 CI 集成

### Milestone 5: Email Verification MVP（邮件验证核心）
- **Title**: `M5 - Email Verification MVP`
- **Description**: 实现完整的邮件验证流程：OTP + Magic Link 双验证机制、注册/登录流程改造、防滥用限流、同设备检测。
- **Success Criteria**:
  - 注册后用户状态为 pending，必须验证邮件才能登录
  - 支持 6 位数字 OTP 验证（15 分钟有效期）
  - 支持 Magic Link 验证（同设备自动验证，跨设备回退到 OTP）
  - 重发邮件限流：90 秒延迟 + 6 次上限
  - Bootstrap 流程也需要邮件验证
  - 关键路径具备稳定单测

### Milestone 6: Email Service Advanced（生产级增强）
- **Title**: `M6 - Email Service Advanced`
- **Description**: 完成生产级邮件服务：邮件送达率优化（SPF/DKIM/DMARC）、Bounce 处理、Webhook 集成、分析埋点。
- **Success Criteria**:
  - 配置 SPF/DKIM/DMARC 文档完善
  - 实现 Bounce 处理逻辑（硬退信拉黑、软退信重试）
  - Webhook 接收 ESP 状态回调（delivered/opened/bounced）
  - Mixpanel/Segment 埋点集成
  - 监控告警配置（发送失败率、队列积压）

---

## Milestone 4 — Email Service Core（邮件服务基础）

### Issue: M4-01 邮件服务数据表 migration
**Labels**: `milestone:4, area:email, type:feature, priority:p0, service:auth-api`

#### 表结构变更
- 新增 `services/auth-api/migrations/004_email_service.sql`
- `email_verifications` — 验证令牌表（OTP + Magic Link）
- `email_jobs` — 邮件任务批次表
- `email_records` — 单封邮件记录表
- `email_status_history` — 邮件状态历史表（时序日志）
- 修改 `users` 表：添加 `email_verified_at TIMESTAMP`，修改 `status` 默认值为 0（pending）

#### Checklist
- [ ] 编写 migration（幂等：`IF NOT EXISTS`）
- [ ] `email_verifications` 包含字段：`id`, `user_id`, `token_hash`, `token_type`（otp/magic_link）, `expires_at`, `verified_at`, `attempts`, `created_at`
- [ ] `email_verifications.token_hash` 设为 `UNIQUE`
- [ ] `email_records` 包含 `external_id`（ESP 返回的 message ID）
- [ ] `email_status_history` 支持状态：queued/sent/delivered/opened/clicked/bounced/failed
- [ ] 更新 README：新增表说明

#### 验收标准
- [ ] migration 可重复执行不报错
- [ ] 验证令牌可通过 `token_hash` 唯一定位
- [ ] 邮件状态历史支持时序查询（按 `created_at` 排序）

---

### Issue: M4-02 Redis 队列基础设施（common-go）
**Labels**: `milestone:4, area:email, type:chore, priority:p0`

#### Checklist
- [ ] 在 `modules/common-go/pkg/queue/` 创建 Redis 队列抽象
- [ ] 实现 `Enqueue(queueName, payload)` — 使用 `RPUSH`
- [ ] 实现 `Dequeue(queueName, timeout)` — 使用 `BLPOP`
- [ ] 实现 `QueueLength(queueName)` — 使用 `LLEN`
- [ ] 支持 JSON 序列化/反序列化
- [ ] 添加单测（mock Redis）

#### 验收标准
- [ ] 可入队/出队任意 JSON payload
- [ ] 支持阻塞式消费（timeout 可配置）
- [ ] `go test ./modules/common-go/pkg/queue/...` 通过

---

### Issue: M4-03 邮件配置与 SMTP 工具（common-go）
**Labels**: `milestone:4, area:email, type:chore, priority:p0`

#### Checklist
- [ ] 在 `modules/common-go/pkg/email/` 创建邮件工具包
- [ ] 实现 `SMTPConfig` 结构体：`Host`, `Port`, `Username`, `Password`, `FromEmail`, `FromName`
- [ ] 实现 `SendEmail(to, subject, htmlBody, textBody)` — 使用 `net/smtp` 或 `gomail`
- [ ] 实现 `GenerateOTP()` — 生成 6 位数字随机码
- [ ] 实现 `GenerateMagicToken()` — 生成 URL-safe 随机 token（32 字节 base64url）
- [ ] 实现 `HashToken(token)` — SHA256 哈希（hex 编码）
- [ ] 添加单测

#### 验收标准
- [ ] 可通过 SMTP 发送 HTML + 纯文本邮件
- [ ] OTP 生成符合高熵要求（6 位数字，范围 000000-999999）
- [ ] Magic token 生成 URL-safe（无 `+` `/` `=`）
- [ ] `go test ./modules/common-go/pkg/email/...` 通过

---

### Issue: M4-04 email-worker 服务骨架
**Labels**: `milestone:4, area:worker, type:feature, priority:p0, service:email-worker`

#### Checklist
- [ ] 创建 `services/email-worker/` 目录结构
- [ ] `cmd/worker/main.go` — 服务入口
- [ ] `internal/config/config.go` — 配置加载（SMTP、Redis、DB）
- [ ] `internal/consumer/consumer.go` — Redis 队列消费者
- [ ] `internal/sender/sender.go` — 邮件发送逻辑
- [ ] `internal/store/store.go` — 数据库操作（更新 email_records/status_history）
- [ ] 添加 `go.mod`（独立模块）
- [ ] 更新 `go.work` 包含 email-worker

#### 验收标准
- [ ] `go run services/email-worker/cmd/worker/main.go` 可启动
- [ ] 启动时加载配置不报错
- [ ] 可连接 Redis 和 PostgreSQL

---

### Issue: M4-05 email-worker 消费与发送逻辑
**Labels**: `milestone:4, area:worker, type:feature, priority:p0, service:email-worker`

#### 接口清单
- Redis 队列：`email:send`
- Payload 格式：`{ "record_id": "uuid", "to": "...", "subject": "...", "html_body": "...", "text_body": "..." }`

#### Checklist
- [ ] consumer 从 `email:send` 队列阻塞消费（`BLPOP`，timeout 5s）
- [ ] 解析 payload，调用 sender 发送邮件
- [ ] 发送成功：更新 `email_records.external_id`，插入 `email_status_history`（status=sent）
- [ ] 发送失败：插入 `email_status_history`（status=failed），记录错误信息
- [ ] 支持优雅关闭（捕获 SIGTERM/SIGINT）

#### 验收标准
- [ ] 入队邮件任务后，worker 可消费并发送
- [ ] 发送成功后 `email_records` 有 `external_id`
- [ ] 发送失败后 `email_status_history` 有 failed 记录
- [ ] 手动测试：发送到真实邮箱可收到邮件

---

### Issue: M4-06 Docker Compose 集成 email-worker
**Labels**: `milestone:4, area:worker, type:chore, priority:p1`

#### Checklist
- [ ] 在 `deploy/docker-compose.yml` 添加 `email-worker` 服务
- [ ] 依赖 postgres、redis、migrate
- [ ] 挂载 `.env` 文件
- [ ] 添加健康检查（可选）
- [ ] 更新 `.env.example`：添加 SMTP 配置变量

#### 验收标准
- [ ] `make up` 后 email-worker 正常启动
- [ ] `docker-compose logs email-worker` 无错误
- [ ] worker 可连接 Redis 和 PostgreSQL

---

### Issue: M4-07 邮件服务单元测试
**Labels**: `milestone:4, area:email, type:test, priority:p1, service:email-worker`

#### Checklist
- [ ] email-worker consumer 测试（mock Redis）
- [ ] email-worker sender 测试（mock SMTP）
- [ ] common-go email 工具测试（OTP/token 生成、哈希）
- [ ] common-go queue 测试（Redis 队列操作）

#### 验收标准
- [ ] `go test ./services/email-worker/...` 通过
- [ ] `go test ./modules/common-go/pkg/email/...` 通过
- [ ] `go test ./modules/common-go/pkg/queue/...` 通过

---

### Issue: M4-08 CI 集成 email-worker 测试
**Labels**: `milestone:4, type:ci, priority:p1`

#### Checklist
- [ ] CI workflow 增加 email-worker 测试 job
- [ ] 启动 postgres+redis service
- [ ] 运行 `go test ./services/email-worker/...`
- [ ] deploy workflow 增加 `needs: [test]`

#### 验收标准
- [ ] CI 中 email-worker 测试失败时 deploy job 不运行
- [ ] CI 通过时可继续部署

---

## Milestone 5 — Email Verification MVP（邮件验证核心）

### Issue: M5-01 注册流程改造：发送验证邮件
**Labels**: `milestone:5, area:auth, type:feature, priority:p0, service:auth-api`

#### 接口变更
- `POST /api/v1/auth/register`
- 行为变更：创建用户后 `status=0`（pending），生成验证令牌，入队邮件任务

#### Checklist
- [ ] 修改 `handler.Register()`：用户创建后调用 `Store.CreateVerification()`
- [ ] 生成 OTP（6 位数字）和 Magic Link token
- [ ] 存储到 `email_verifications` 表（token_hash，expires_at=now+15min）
- [ ] 创建 `email_records` 记录
- [ ] 入队到 Redis `email:send` 队列（包含 OTP 和 Magic Link）
- [ ] 返回 202 Accepted，提示用户检查邮箱
- [ ] 更新 Postman collection

#### 验收标准
- [ ] 注册成功返回 202，用户 `status=0`
- [ ] `email_verifications` 表有记录
- [ ] Redis 队列有邮件任务
- [ ] email-worker 消费后用户收到验证邮件（包含 OTP 和 Magic Link）

---

### Issue: M5-02 实现 OTP 验证接口
**Labels**: `milestone:5, area:auth, type:feature, priority:p0, service:auth-api`

#### 接口清单
- `POST /api/v1/auth/verify-email`
- req: `{ "email": "...", "otp": "123456" }`
- resp: `{ "message": "Email verified successfully" }`

#### Checklist
- [ ] 查询 `email_verifications` 表（email + token_type=otp + verified_at IS NULL）
- [ ] 校验：未过期、OTP 匹配（hash 比对）
- [ ] 验证成功：更新 `users.email_verified_at=now()`，`users.status=1`
- [ ] 更新 `email_verifications.verified_at=now()`
- [ ] 验证失败：增加 `attempts` 计数，超过 5 次锁定
- [ ] 返回统一错误码（invalid_otp / expired_otp / too_many_attempts）
- [ ] 更新 Postman collection

#### 验收标准
- [ ] 正确 OTP 验证成功，用户可登录
- [ ] 错误 OTP 返回 invalid_otp
- [ ] 过期 OTP 返回 expired_otp
- [ ] 超过 5 次尝试返回 too_many_attempts
- [ ] 单测覆盖：成功 / 错误 OTP / 过期 / 超限

---

### Issue: M5-03 实现 Magic Link 验证接口
**Labels**: `milestone:5, area:auth, type:feature, priority:p0, service:auth-api`

#### 接口清单
- `GET /api/v1/auth/verify-magic-link?token=<token>&state=<state>`
- 行为：验证 token，检查 state 是否匹配浏览器 session

#### Checklist
- [ ] 查询 `email_verifications` 表（token_hash + token_type=magic_link + verified_at IS NULL）
- [ ] 校验：未过期、token 匹配
- [ ] **同设备检测**：从 cookie/session 读取 state，与 URL 参数 state 比对
- [ ] 同设备：自动验证，更新 `users.email_verified_at`，`users.status=1`，重定向到前端成功页
- [ ] 跨设备：返回提示页面，要求用户手动输入 OTP
- [ ] 更新 Postman collection（可选，主要是浏览器测试）

#### 验收标准
- [ ] 同设备点击 Magic Link 自动验证成功
- [ ] 跨设备点击 Magic Link 提示输入 OTP
- [ ] 过期 token 返回错误页面
- [ ] 单测覆盖：同设备成功 / 跨设备回退 / 过期

---

### Issue: M5-04 实现重发验证邮件接口（含限流）
**Labels**: `milestone:5, area:auth, type:feature, priority:p0, service:auth-api`

#### 接口清单
- `POST /api/v1/auth/resend-verification`
- req: `{ "email": "..." }`
- resp: `{ "message": "Verification email sent", "retry_after": 90 }`

#### Checklist
- [ ] Redis 限流：`resend:{email}`，90 秒窗口，6 次上限
- [ ] 校验用户存在且 `email_verified_at IS NULL`
- [ ] 撤销旧的未验证令牌（可选：软删除或标记为 revoked）
- [ ] 生成新的 OTP 和 Magic Link token
- [ ] 入队邮件任务
- [ ] 返回 202 Accepted + `retry_after` 字段
- [ ] 超过限流返回 429 Too Many Requests
- [ ] 更新 Postman collection

#### 验收标准
- [ ] 重发成功后用户收到新邮件
- [ ] 90 秒内重发返回 429
- [ ] 超过 6 次重发返回 429
- [ ] 已验证用户重发返回 400 Bad Request
- [ ] 单测覆盖：成功 / 触发限流 / 已验证

---

### Issue: M5-05 登录流程改造：阻止未验证用户登录
**Labels**: `milestone:5, area:auth, type:feature, priority:p0, service:auth-api`

#### 接口变更
- `POST /api/v1/auth/login`
- 行为变更：校验 `email_verified_at IS NOT NULL`，否则返回 403 Forbidden

#### Checklist
- [ ] 修改 `handler.Login()`：密码验证通过后，检查 `user.email_verified_at`
- [ ] 未验证返回错误码 `email_not_verified`，提示用户验证邮箱
- [ ] 更新 Postman collection

#### 验收标准
- [ ] 未验证用户登录返回 403 + `email_not_verified`
- [ ] 已验证用户正常登录
- [ ] 单测覆盖：未验证阻止 / 已验证通过

---

### Issue: M5-06 Bootstrap 流程改造：owner 也需验证
**Labels**: `milestone:5, area:auth, type:feature, priority:p1, service:admin-api`

#### 接口变更
- `POST /api/v1/bootstrap`
- 行为变更：创建 owner 后 `status=0`，发送验证邮件

#### Checklist
- [ ] 修改 `handler.Bootstrap()`：创建 owner 用户后调用 auth-api 的验证令牌生成逻辑（或复用）
- [ ] 入队邮件任务
- [ ] 返回提示：owner 需验证邮箱才能登录
- [ ] 更新 Postman collection

#### 验收标准
- [ ] Bootstrap 成功后 owner 收到验证邮件
- [ ] owner 验证邮箱后可登录
- [ ] 单测覆盖（至少 handler happy path）

---

### Issue: M5-07 邮件模板设计（HTML + 纯文本）
**Labels**: `milestone:5, area:email, type:chore, priority:p1, service:email-worker`

#### Checklist
- [ ] 设计验证邮件 HTML 模板（包含 OTP 和 Magic Link）
- [ ] 设计纯文本版本（fallback）
- [ ] 使用 Go `html/template` 渲染
- [ ] 模板变量：`{{.OTP}}`, `{{.MagicLink}}`, `{{.ExpiresIn}}`
- [ ] 存储在 `services/email-worker/templates/` 目录

#### 验收标准
- [ ] 邮件美观、响应式（移动端友好）
- [ ] OTP 和 Magic Link 清晰可见
- [ ] 纯文本版本可读性良好

---

### Issue: M5-08 Email Verification 集成测试
**Labels**: `milestone:5, area:auth, type:test, priority:p1, service:auth-api`

#### Checklist
- [ ] 端到端测试：注册 → 收到邮件 → OTP 验证 → 登录成功
- [ ] 端到端测试：注册 → 收到邮件 → Magic Link 验证 → 登录成功
- [ ] 测试重发限流：连续重发触发 429
- [ ] 测试过期令牌：等待 15 分钟后验证失败
- [ ] 测试跨设备 Magic Link：state 不匹配回退到 OTP

#### 验收标准
- [ ] 集成测试稳定通过
- [ ] 覆盖关键路径和边界情况

---

## Milestone 6 — Email Service Advanced（生产级增强）

### Issue: M6-01 SPF/DKIM/DMARC 配置文档
**Labels**: `milestone:6, area:email, type:chore, priority:p1`

#### Checklist
- [ ] 编写 `docs/email-deliverability.md`
- [ ] SPF 记录配置示例
- [ ] DKIM 密钥生成与 DNS 配置
- [ ] DMARC 策略配置（p=reject for production）
- [ ] 专用 IP 策略说明
- [ ] 常见问题排查（bounce、spam）

#### 验收标准
- [ ] 文档清晰易懂，可操作
- [ ] 包含真实配置示例

---

### Issue: M6-02 Bounce 处理逻辑
**Labels**: `milestone:6, area:worker, type:feature, priority:p1, service:email-worker`

#### Checklist
- [ ] 解析 SMTP 错误码（5xx=硬退信，4xx=软退信）
- [ ] 硬退信：标记邮箱为 invalid，拉黑（新增 `email_blacklist` 表）
- [ ] 软退信：重试 3 次，间隔 1h/4h/24h
- [ ] 更新 `email_status_history` 记录 bounce 类型
- [ ] 添加单测

#### 验收标准
- [ ] 硬退信邮箱被拉黑，后续不再发送
- [ ] 软退信自动重试 3 次
- [ ] `email_status_history` 有 bounce 记录

---

### Issue: M6-03 Webhook 接收 ESP 状态回调
**Labels**: `milestone:6, area:worker, type:feature, priority:p2, service:email-worker`

#### 接口清单
- `POST /webhooks/email-status`（email-worker 新增 HTTP 服务）
- 接收 ESP 回调：delivered/opened/clicked/bounced

#### Checklist
- [ ] email-worker 启动 HTTP 服务器（独立端口，如 8082）
- [ ] 实现 webhook 接收接口
- [ ] 验证 webhook 签名（ESP 提供的签名机制）
- [ ] 根据 `external_id` 查询 `email_records`
- [ ] 插入 `email_status_history` 记录
- [ ] 更新 Docker Compose 暴露 8082 端口

#### 验收标准
- [ ] ESP 回调成功更新状态
- [ ] 签名验证失败返回 401
- [ ] `email_status_history` 有 delivered/opened 记录

---

### Issue: M6-04 Mixpanel/Segment 埋点集成
**Labels**: `milestone:6, area:email, type:feature, priority:p2, service:auth-api`

#### 事件清单
- `verification_email_sent`
- `verification_email_bounced` (Property: bounce_type)
- `verification_link_clicked` (Property: latency_from_sent)
- `account_activated` (Property: method: magic_link|otp)

#### Checklist
- [ ] 在 `modules/common-go/pkg/analytics/` 创建 Mixpanel/Segment 客户端
- [ ] auth-api 在关键节点埋点（注册、验证、激活）
- [ ] email-worker 在邮件发送/bounce 时埋点
- [ ] 配置化：`ANALYTICS_ENABLED`, `MIXPANEL_TOKEN`

#### 验收标准
- [ ] Mixpanel/Segment 控制台可看到事件
- [ ] 事件属性完整（user_id, email, timestamp, method）

---

### Issue: M6-05 监控告警配置
**Labels**: `milestone:6, area:email, type:chore, priority:p2`

#### Checklist
- [ ] 定义关键指标：邮件发送失败率、队列积压长度、平均发送延迟
- [ ] 使用 Prometheus + Grafana（或云服务）
- [ ] email-worker 暴露 `/metrics` 端点
- [ ] 配置告警规则：失败率 >5%、队列积压 >1000
- [ ] 编写 `docs/monitoring.md`

#### 验收标准
- [ ] Grafana 仪表盘可视化邮件指标
- [ ] 告警触发时发送通知（Slack/Email）

---

### Issue: M6-06 KPI 分析与优化
**Labels**: `milestone:6, area:email, type:chore, priority:p2`

#### KPI 目标
- **Activation Rate**: >85% 注册用户完成验证
- **TTV (Time to Value)**: 中位数 <3 分钟（注册到验证完成）

#### Checklist
- [ ] 从 Mixpanel/Segment 导出数据分析
- [ ] 计算 Activation Rate 和 TTV
- [ ] 识别瓶颈（邮件延迟、用户未点击、OTP 输入错误）
- [ ] 优化建议：缩短过期时间、优化邮件文案、增加重发提示
- [ ] 编写分析报告

#### 验收标准
- [ ] KPI 达标或有明确优化方向
- [ ] 分析报告包含数据和建议

---

## 总结

**M4-M6 总计 25 个 Issue**，覆盖：
- **M4（8 个）**: 邮件服务基础设施
- **M5（8 个）**: 邮件验证核心流程
- **M6（6 个）**: 生产级增强

**关键依赖关系**：
- M5 依赖 M4 完成
- M6 可与 M5 并行开发（部分功能）

**预估工作量**：
- M4: 3-5 天（1 名后端工程师）
- M5: 5-7 天（1 名后端工程师 + 1 名前端工程师配合）
- M6: 3-5 天（可分阶段实施）

**总计**: 约 2-3 周完成完整邮件服务集成。
