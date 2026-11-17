# Responses 搜索与缓存兼容修复

日期：2026-11-17。NewAPI 基线 `90f95a8b`，配套 CPA 基线 `ea25dffb`。本轮授权本地修复与验证。

## 目标

修复 Codex Responses 在搜索控制、缓存控制和多候选拒绝叠加时的兼容缺口。NewAPI 负责准确解析 hosted 搜索模式、保留缓存控制、执行统一实时搜索与原计费，并记录不含客户内容的控制字段摘要；CPA 负责实际模型的能力判断、兼容选路及完整拒因诊断。

## 产品约束

- 显式 `external_web_access:false` 的非 Preview 工具保留原模型原生缓存搜索；不自动联网、不换回答模型、不新增 DeepSeek 工具费。供应商不支持时准确报错，禁止伪造搜索成功。
- Preview 工具忽略其 `external_web_access` 标志，按实时搜索处理；其他位置、域名、输出及调用次数限制仍须保留或明确拒绝。
- 实时搜索继续既有 DeepSeek Search/Fetch 工具循环、历史归属与费用规则。
- 缓存 Key 与保留时间必须到达兼容的原模型协议；不静默删除来绕过 CPA 检查。
- 两端诊断仅记录固定枚举、字段类型和计数；不保存缓存 Key、任意工具名、客户正文或凭据。

## 验收

普通函数、缓存与实时搜索、Preview false、非法类型、版本化类型、JSON/SSE、原生 cached 计费、历史恢复、零调用拒绝和已执行后不重放均有回归。增加与配套 CPA 的合成 HTTP 验证，核对实际出站模型、工具、控制字段、终态及调用次数。保留现有客户端与生产配置。

## 状态

2026-11-18 本地修复完成：Preview false 按原生语义忽略；admin_info 新增 `request_controls` 安全类型摘要与 `web_search_mode` 处理模式。该模式描述服务端选择，不表示已经实际执行搜索；实际次数与费用继续以 `managed_web` 和消费记录为准。

websearch、types、relay、service、controller 五包完整测试通过，相关 race 与 vet 通过。与独立 CPA 进程完成六组实际 HTTP 合成联测，覆盖普通 JSON/SSE、实时搜索、Preview false、DeepSeek cached 零调用拒绝及兼容原生 cached。缓存字段保留；两轮模型与一次搜索共 62524 quota，拒绝组返还预扣且零消费日志。供应商与搜索后端为测试实现，未调用付费上游。

旧 `plan.go` overlay 能复现新增 Preview false 回归失败，当前实现通过。语义依据：[OpenAI 官方 Web search 文档](https://developers.openai.com/api/docs/guides/tools-web-search)。

完整跨项目 PRD、联合测试命令与能力限制见配套 CPA 仓库 `docs/responses-compat-prd-20261117_CN.md`。未提交、推送或部署，原客户场景尚未在线验证。

2026-11-18 续接：新增 `history_only`。只在协议原生搜索引用位置恢复本站历史，不再扫描任意正文中的 `ws_na_`；保留函数定义和缓存字段，避免未开实时搜索服务的普通请求被误拒。跨调用者引用仍 409，Claude 无原调用的孤立本站结果明确拒绝。新增回归在旧 handler overlay 下复现 503/误查 Redis，修复后通过；四个相关包完整测试、两包定向 race、vet 及八组 CPA 联合 HTTP 检查通过。

已核对上游：NewAPI #7867/#7690/#8250 已合并；CPA #5382/#4094 仍未合并。腾讯 11-12 文档已列出 1017 的 Chat/Responses 原生搜索，须开 TokenHub 插件，不能用本项目未验收推断供应商无能力。继续保留现有独立 DeepSeek Search/Fetch 和原回答模型；腾讯原生搜索是另外的工具供应商/计费路径，本次未切换。完整来源与设计判断见 CPA `docs/deepseek-search-upstream-review-20261118_CN.md`。


2026-11-18 用户授权合并、推送、CI 与部署。扩展整合范围和发布要求见 [转发整合说明](../operations/forwarding-integration-20261118_CN.md)，实际发布结果以固定提交、镜像和业务验证记录为准。
