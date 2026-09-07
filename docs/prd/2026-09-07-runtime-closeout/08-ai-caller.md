# AI 计划提交调用端接通
CRM端已经启用AI派发及ai-review-production OAuth2调用方（凭据仅生产/etc/aicrm/integrations/ai-review-production.json）。调用端按用户实际AI/Agent服务定位，采用V1 REST POST /open/v1/ai/review-plans 或MCP create_ai_review_plan；六项目录不扩。只创建pending review，客户用OneID，原稳定幂等键与每人内容保留，机器无审批/发送权。接通实际token获取/刷新与错误回读，保管凭据、最小授权，测试不可将正文/Token写日志。须核实际部署/调用端请求抵达并保留0提前发送；未知调用端位置时向用户问具体服务或配置路径，不假称已接好。用户真实内容和人工发送验收另行。
