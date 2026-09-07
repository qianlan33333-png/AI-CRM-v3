# 问卷外推生产接通
沿已有PRD02/SurveyCompletionProvider及旧问卷外推行为。OneID由现有身份Port读取受信外部标识；Survey完成收据/任务同事务，外推归outbound/External Effects。定位旧生产受保护的目标、签名和字段契约，映射配置引用，不能把商品外推密钥/载荷想当然共用。未配置目标时不得把接收成功等同已投递。确认真实接收方契约后启用已开发Provider；不重发历史问卷、不制造用户未批准的真实业务推送。必要协议适配由执行任务独立PR，本文补测试/目标引用/生效证明，不含实际秘密或PII。


生产目标配置准备（2026-09-07）：旧启用问卷共 7 份配置，对应 2 个去重 HTTPS 目标，鉴权只在 V3 `/etc/aicrm/integrations/survey-completion-targets-prepared-20260907.json`（root 0600）。`hxc-questionnaire-production-v1-1` 对应旧配置 ID 20/21/29/37/52/54；`hxc-questionnaire-production-v1-2` 对应旧配置 ID 19。该映射只供新问卷选择同一目标时参考，不表示已导入旧问卷或旧提交。目标已包含实际旧运行 HMAC signer 和 V3 作用域内 `unionid` 身份合同，未复制旧问卷的 day/frequency/expiry 业务配置；这些仍由新问卷管理员明确设置。当前仅准备，未应用/未实际推送。
