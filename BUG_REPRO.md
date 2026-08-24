# Bug Reproduction

## 包的性质

当前 tasks/wp-timezone-sequence/green 保存 diagnosis 的最终交付提交：生产源码与下面固定的 parent SHA 完全一致，不包含模型修复；该提交只增加与 red R1 相同的任务测试和交付文件，运行题目验证命令仍应得到 red。完整验证日志仍只在本地留存。

## 问题现象

样品标签在配置了水源本地时区且采样发生在 UTC 日期边界时使用了错误的业务日期，导致同一监测站的样品编号跨日重复或落入错误日期，请先不要修改代码并定位发放样标签日期为何没有遵循水源时区

## 含 Bug 版本

- 仓库：vance1852/waterpro
- 仓库地址：https://github.com/vance1852/waterpro.git
- parent SHA：811517d31425d7daf8e29600843866a8f1a675f0

## 复现步骤

```bash
git clone -- https://github.com/vance1852/waterpro.git bug-repro
cd bug-repro
git checkout --detach 811517d31425d7daf8e29600843866a8f1a675f0
go test ./internal/integration -run '^TestWPWaterSourceTimezoneDrivesSampleSequence$' -count=1
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/integration -run '^TestWPWaterSourceTimezoneDrivesSampleSequence$' -count=1
--- FAIL: TestWPWaterSourceTimezoneDrivesSampleSequence (0.24s)
    waterpro_timezone_test.go:55: sample label = "NIGHT-20260825-0001", want local business day label NIGHT-20260824-0001
FAIL
FAIL	github.com/vance1852/waterpro/internal/integration	0.248s
FAIL

```

stderr：

```text
(empty)
```

## 通过条件

配置水源时区后，采样标签必须按该时区计算业务日期并保持序列唯一，UTC 日期边界的样品应落在本地日期且既有采样流程仍然通过
