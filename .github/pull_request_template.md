## 变更

- 用户可观察的变化：
- 明确未改变的范围：

## 验证

- [ ] `make check`
- [ ] 已检查数据 Owner、幂等/重放和外部效果（如适用）
- [ ] 无 Secret、PII 或生产数据进入代码、日志和测试固定值

## 能力影响与风险

- OneID 分类及理由：
- 持久化 / 内部任务 / Provider 读取 / 外部效果分类及理由：
- 影响报告：`governance` artifact（能力 → 共享依赖 → 消费者 → 既有测试/旅程）

高风险变更必须填写下列三行。Head 填 PR 分支当前完整 SHA；追加提交后更新并重跑失败的 governance job。这是作者影响声明，不是独立人工批准。独立审核依保护分支和实际 review 记录确认。

Governance-Head: <当前 PR head 的 40 位 SHA>
Governance-Preservation: <原能力保持不变的业务断言和受影响消费者，至少 12 字符>
Governance-Validation: <实际验证命令、结果及尚未验证项，至少 12 字符>
