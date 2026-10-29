# GO-073 最终提交 Go 与 Kafka 差量核验

2026-10-29，核验提交 `399705a1fb92e5075192004c1cf1563f120dee47`，开始前 `main` 与 `origin/main` 一致、工作区干净；创建本目录后状态中出现本目录的未跟踪文件。作为非实现者，审阅了 `communityconsumer/worker.go` 的元数据就绪、延迟创建 reader、关闭及同步位点提交逻辑，未发现本次范围内确定的阻断缺陷。`Poll` 的单 goroutine 所有权与 `Close` 的锁保护是该结论的前提，运行中删除并立即重建主题仍未验证。

## 实际运行

- Go 1.27.0/windows/amd64：在 `backend/` 运行 `gofmt -l .`、`go vet ./...`、`go test -count=1 ./...` 和 `go build ./...`，均退出 0；见 [格式](gofmt.txt)、[vet](vet.txt)、[普通测试](test.txt) 与 [build](build.txt)。vet/build 在成功时没有输出；普通测试并未开启全部真实依赖或付费模型测试，不能视为其已运行。Linux race 和远端 CI 不在这组 Windows 命令中，依实际 Actions 结果另行判断。
- 专用 Compose 项目 `project-full-stack-review073-kafka`、全新卷，Kafka 3.9.0，端口 `127.0.0.1:9122`；启动前确认无监听，启动后核验项目/服务标签、端口绑定和 healthy。实际命令（Go 环境显式设 `GO073_KAFKA_FRESH=1`、`KAFKA_BROKERS=127.0.0.1:9122`）为 `go test -count=1 -run 'Test(IntegrationFreshTopicStartsAllGroupsAndPreservesOffsets|IntegrationUnavailableBrokerCancelsReadiness|TopicReadinessCancellationAndCloseDoNotStartReader|ConstructorUsesSeparateStableSynchronousGroupsAndRejectsUnknownProjection)$' -v ./internal/communityconsumer`。四项通过，见 [真实 Kafka 回归](kafka-real.txt)。
- 实测随机私有 topic `community.go073.fresh.8b44d2f91f850890`：消费者运行 4 秒未自动创建主题，生产者创建后 follow/feed/search 三个独立组各消费第一条并提交 offset 1；同组重启后消费第二条而未重读第一条。测试完成后额外只读查询，三个组各为 committed 2 / log end 2 / lag 0、无活动成员，见 [位点记录](kafka-offsets.txt)。回归 processor 为测试回调，不能将此单 broker 协议回归当成 MySQL/ES 业务闭环或持续吞吐；业务闭环、负载和历史故障分别以其已有证据及独立证据审查为准。
- 测试后只停止本项目 Kafka，进程 exit 143、OOM=false，见 [停止状态](kafka-stop.json)；[专用卷](volume-preserved.txt)保留，私有 topic/组未删除或重置。没有启动、停止或更改其他服务，未调用付费模型。

本报告是 GO-073 的 Go/Kafka 差量复跑与代码审阅，不单独代表整票验收通过；交易、缓存、投影证据的独立复算及最终提交远端 CI 状态应结合其他核验报告。
