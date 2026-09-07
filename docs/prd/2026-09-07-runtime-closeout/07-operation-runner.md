# 运营 Runner 实际接入
旧来源 extensions/hxc/operation_cycles/local_connector.py 与 docs/operation_cycles/local_codex_connector_runbook.md；V3 internal/operationcycle/http 已有固定Token独立接口，六项OpenPlatform范围不扩。只用已有真实执行器与本机安全配置，先核执行器安装位置、线程绑定、心跳/领取/结果协议差异，不发明执行目标。运行状态/任务由现有领域持久化，禁止另建CRM任务队列。连接器如需Go等价适配，由独立PR交协议、权限与恢复测试；不能只生成Token就宣称Runner在线。没有用户行动请求时不得启动Codex业务执行或外部发送。旧系统执行器保持独立，不将旧待执行动作切到V3。
