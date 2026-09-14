# 发布验收报告模板

## 测物

| 字段 | 值 |
| --- | --- |
| 候选 SHA | `<40-char SHA>` |
| Harness SHA | `<40-char SHA>` |
| 工作树与运行器 | `<clean worktree / runner>` |
| 数据库 | `<localhost + aicrm_test_* only>` |
| Provider 状态 | `disabled / 白名单真实验收另列` |
| 报告目录 | `<outside source tree>` |

候选 SHA 与 harness SHA 不同是允许的；每项结果必须明确其各自 SHA。任何运行前或运行后源码脏状态均使该次结果无效。

## Lane 结果

| Lane | 环境前提 | 首次结果 | 最终结果 | 证据 | 结论 |
| --- | --- | --- | --- | --- | --- |
| preflight | Go/Node/npm、dedup baseline |  |  |  |  |
| backend | PostgreSQL 16、冻结 donor |  |  |  |  |
| frontend | Node/npm、冻结 donor |  |  |  |  |
| browser | Linux amd64、Chrome、Noto、PostgreSQL 16、冻结 donor |  |  |  |  |
| archive-sdk | Linux amd64 SDK 环境 |  |  |  |  |

## Skip 与适用性

| 用例或检查 | lane | 原因 | 适用性 | 闭合 lane/SHA/证据 | 状态 |
| --- | --- | --- | --- | --- |
|  |  |  | `不适用 / 必测` |  | `开放 / 已闭合` |

每一个 skip 都必须出现在此表。平台专项若对当前运行器不适用，可记录为“不适用”，但不转换为通过；任何必测 skip 只有同一候选 SHA 的明确执行 lane 通过后才可闭合。

## 真实白名单验收与发布后 readback

| 能力 | 测试身份/白名单 | Provider receipt | CRM 回读 | 业务确认 | 状态 |
| --- | --- | --- | --- | --- | --- |
| 企微/渠道 |  |  |  |  |  |
| OAuth/H5 |  |  |  |  |  |
| 支付/退款 |  |  |  |  |  |

## 放行结论

`允许上线 / 禁止上线`：`<结论与阻断项或已接受例外>`

例外必须逐项列出影响、临时办法、负责人、修复期限和接受人。未执行、超时、失败或缺证据的必测项不属于例外，除非发布负责人明确重新定义其适用性并留下记录。
