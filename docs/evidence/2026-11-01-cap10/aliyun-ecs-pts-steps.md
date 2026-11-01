# 一台ECS + 阿里云PTS：准备与操作步骤

2026-11-01。方案是把Go API和现有MySQL / Redis / Kafka / ES部署在一台ECS，由PTS的两个JMeter引擎经VPC私网发压。本文是操作指南；**当前先等本地适配和交叉审查完成，不必先付费开ECS，也不要在PTS点击调试、启动、保存并压测或创建定时任务。** 尚未上传文件、部署云端或取得云端通过结果。

本轮继续CAP-10剩余名额：最多8个60秒主窗、最多同档2个180秒正式窗、最多1项有证据的优化；主负载最多840秒。每窗10秒warm另账，准备、引擎租用和报告等待也单列，不因为使用PTS增加压力轮，不重跑Feed。实际执行以[STATUS](../../../STATUS.md)和[计划](plan.md)为准。

## 1. 现在只准备账号与连接方式

1. 确认阿里云账号可登录ECS、VPC、PTS控制台，并能查看本账号的余额、试用资格和按量单价。试用额度、可选机型和地域以账号实际页面为准，不假定16核32GiB或PTS免费。
2. 准备SSH密钥登录方式；已有密钥可复用公钥，私钥保留在自己的本地私有路径，不粘贴到聊天、Git或截图。
3. 等agent明确本地准备完成后，再进行下述付费创建。现在无需提供AccessKey值，也无需先开通资源包。

PTS启动任务会产生资源消耗，按任务实际VUM结算；warm主窗分成独立平台任务时，两者均可能计费。平台引擎初始化、公共起跑屏障、收尾与日志采样等会使实际计费时长不同于业务主窗。费用预估和试用抵扣应查看本账号；不以14分钟主负载直接等同云账单。[官方计费说明](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/product-overview/pts-3-0-billing-overview/)

## 2. 获得开通通知后，创建一台ECS

在ECS控制台选择创建实例，并按下表核对。这里是本批选定配置，不是对生产容量的承诺。

| 项目 | 本批配置 |
| --- | --- |
| 地域 | 华东2（上海）；后续PTS同地域 |
| 计费 | 按量付费；暂不购买包年包月、承诺消费或额外资源包 |
| 计算 | 16 vCPU、32GiB，x86_64 / amd64；不选ARM、突发积分或共享型以免混入另一种限额 |
| 系统 | Ubuntu 24.04 LTS 64位x86 |
| 磁盘 | 100GiB ESSD系统盘；记录实际性能级别，所有窗口保持相同 |
| 网络 | 新建或明确归属本次的VPC、交换机和安全组，记录私网IPv4与交换机CIDR |
| 管理入口 | SSH密钥；安全组22端口只允许自己的当前公网IP `/32` |
| 公网 | 仅用于SSH及必要的软件/镜像获取；业务压测走私网，不把API、collector或数据库放开到公网 |

创建后提供给主agent：实例ID、地域、私网IPv4、用于SSH的公网地址、实际登录用户名、VPC / 交换机 / 安全组ID，以及**本地SSH私钥路径**。这些连接信息只用于本次私有配置，不加入公开证据包。不要通过聊天发送私钥内容、密码或永久AK。

agent通过SSH核实机器、磁盘、Docker、时钟和资源后，准备独立Compose项目、新卷、新合成会话及观察账号；不复制已撤销的本机会话，也不操作CRM或无关资源。本指南没有要求用户手动运行压测命令。

## 3. 配置私网访问范围

ECS被压服务与PTS必须选择同一上海地域、VPC和交换机。PTS场景选“阿里云VPC内网”，所有被压URL使用该ECS私网IP，不能填公网IP或127.0.0.1。首次控制台可能要求授予PTS访问该VPC的服务权限，由账号所有者核对授权内容后完成。[官方VPC压测要求](https://help.aliyun.com/zh/pts/performance-test-pts-2-0/use-cases/how-to-initiate-an-internal-performance-test)

| 端口 | 用途 | 入方向范围 |
| --- | --- | --- |
| 22 | SSH管理 | 仅自己的公网IP `/32`，不使用`0.0.0.0/0` |
| 8281 | Go真实业务API | 仅PTS所在交换机CIDR或经核实等效的PTS安全组私网来源 |
| 8481 | 本批collector | 同上；请求还必须携带本批控制令牌，不是公开采集服务 |
| 8381 | loopback诊断 | 不开放给PTS或公网 |
| 3481 / 6581 / 9401 / 9281 | MySQL / Redis / Kafka / ES宿主端口 | 不向公网或PTS开放，保留本机/容器内部访问 |

agent负责实际监听配置：私网8281必须能到Go API，同时保留本机健康检查；8381和依赖端口仍保持loopback边界。只在安全组允许两个必要私网入口，不使用全端口放通。PTS施压安全组的出方向与交换机白名单按官方VPC要求核对；不要为图省事关闭系统防护或改无关服务配置。

## 4. 准备最小PTS控制权限

首次控制台建场景、VPC服务授权由用户完成；之后agent只控制这一批独占的SceneId，不操作其他场景。可使用临时STS或专用RAM身份；若已有凭据文件，只提供本地私有文件路径，不发送内容。

API客户端读取标准环境变量`ALIBABA_CLOUD_ACCESS_KEY_ID`、`ALIBABA_CLOUD_ACCESS_KEY_SECRET`、可选`ALIBABA_CLOUD_SECURITY_TOKEN`，或显式私有JSON文件。Linux私有文件权限为0600。不要把AK、STS、collector令牌或签名OSS链接写入命令参数、公开日志、截图或Git。

基础控制/导出所需RAM操作如下；保存/上传场景的控制台权限与ECS管理权限不需要交给压测运行客户端：

```json
{
  "Version": "1",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "pts:GetOpenJMeterScene",
      "pts:GetJMeterSceneRunningData",
      "pts:StartTestingJMeterScene",
      "pts:StopTestingJMeterScene",
      "pts:GetJMeterReportDetails",
      "pts:GetJMeterSampleMetrics"
    ],
    "Resource": "*"
  }]
}
```

这些PTS API目前不提供SceneId级RAM资源约束，官方授权表使用`Resource:"*"`；工具另外绑定专用SceneId和已确认ReportId，并要求场景整批独占。若确需API更新已存在场景，才另加`pts:SaveOpenJMeterScene`，不额外授予创建ECS、购买、删除或广泛管理员权限。[API与授权入口](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/developer-reference/api-reference-1/)

## 5. 首次建立JMeter场景，只上传并保存

等agent提供冻结的上传包后再操作。此包采用原生JMeter场景，不转换为PTS原生HTTP场景。

1. 在PTS控制台选择创建场景 → JMeter压测，命名为本批专用场景，并记录SceneId。
2. 选择JMeter **5.6.3**，与本地验证版本一致。若账号界面没有该版本，先反馈，不自行替换。[官方版本与配置说明](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/getting-started/jmeter-stress-test-quick-start)
3. 上传agent提供的三个文件，保持文件名：`cap10-jmeter.jmx`、`slots.csv`、`cap10-pts-private.json`。JMX已包含所需Groovy逻辑；不要把本机绝对文件路径写进场景。
4. **仅对`slots.csv`启用CSV切分**。CSV只含0..511槽号，无业务ID或用户token；两个引擎应取得互斥分片，collector会拒绝重复或缺失槽。私有JSON仅有collector私网URL与控制令牌，不切分、不公开。JMX和私有JSON每个引擎均需可读。[官方JMeter文件/CSV说明](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/user-guide/create-a-jmeter-scenario1/)
5. 压力来源选上海的阿里云VPC内网，填写前述VPC、交换机和安全组；指定**2个施压引擎**。
6. 本批脚本普通线程512、tearDown线程1；控制台总并发配置为**513**，固定并发从100%开始，禁用递增/阶梯和自动扩引擎。513是线程配置，不是513 mutation/s；实际发起速率由collector下发的50及后续授权档位控制。PTS会改写普通线程组，并按setUp/tearDown规则分配压力，云端须核对两个引擎合计恰好512个普通槽，不能仅凭控制台数字认为接线已通过。[官方线程改写规则](https://help.aliyun.com/en/pts/performance-test-pts-3-0/getting-started/jmeter-stress-test-quick-start)
7. 任务时长字段最小填60秒。脚本中的warm只发10秒，主窗发60或180秒；agent每个phase按冻结配置调整足够的**平台总时长**以覆盖引擎初始化、公共起跑屏障和收尾，主窗实际时长由完整样本证实，不能让平台60秒硬截止截断60秒主窗。严禁把warm改成60秒凑平台下限。[场景API时长限制](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/developer-reference/api-pts-2020-10-20-saveopenjmeterscene)
8. 使用**仅保存**；不要点击调试、启动、保存并压测、定时执行或平台自动探测。把SceneId和所选版本/并发/2引擎/CSV切分配置提供给主agent。页面若没有仅保存选项，停在确认前反馈。

通用JMX、槽CSV和私有配置首次上传后，后续phase从私网collector获取runId、warm/main、rate、seconds与最新关系状态。无需每窗上传账号token CSV或签名OSS文件；若上传文件确有变更，先冻结新哈希再替换，不能在正式两轮间改包。

## 6. 开测前还需确认的两项边界

**施压JVM与独立主机RSS。** Apache JMeter官方启动脚本默认堆为`-Xms1g -Xmx1g`，但这不能证明PTS实际采用同一参数。PTS环境管理公开的是JAR和JMeter `.properties`，当前未从官方页面证实可修改SaaS JVM启动堆；不能把`Xmx`填成JMeter属性就声称已生效。本批PTS新环境已事前声明：每台远程引擎进程RSS分别小于2GiB，远端合计只展示；ECS本地Python collector和Node helper另合计小于2GiB。不得将两个独立远端主机相加后套用原同机k6预算，也不能把该环境适配称为代码优化收益。agent须核真实资源，不能把Xms已提交堆等同VmRSS；超出对应门槛或无法核实时先报告，不临时改限额或增加引擎。[本批资源声明](pts-preparation-plan.md)、[Apache启动脚本](https://raw.githubusercontent.com/apache/jmeter/rel/v5.6.3/bin/jmeter)、[PTS环境管理](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/user-guide/jmeter-environment-management)

**远程停止与完整数据。** PTS停止API只接受SceneId，不能原子地限定ReportId，因此同批SceneId不得由另一个人或定时任务启动。启动超时结果未知时工具不重发Start；未证实远端终态就不开始下一phase。PTS采样日志不是全样本JTL，报告API也不直接提供本批要求的P95；完整脱敏分批记录、真实native起止、资源与终态由私网collector保存，平台报告作为补充。[停止接口](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/developer-reference/api-pts-2020-10-20-stoptestingjmeterscene)、[报告接口](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/developer-reference/api-pts-2020-10-20-getjmeterreportdetails)

agent将先核环境转移、干净构建、依赖健康、夹具、时基、网络、预算和完整采集，再按剩余名额执行；本地mock或有限接线通过不代表PTS云端容量通过。用户只需提供SSH/配置的私有路径与上述场景信息，不需要自行先跑一轮验证。

## 7. 测完先导出验证，再释放

1. agent确认PTS远程任务已结束，导出完整脱敏记录、API/报告补充原件、版本/环境/费用用量、SQL/Redis/Outbox/三消费组与资源核对、所有成功/失败和清理记录。
2. 将需要保留的工件复制到本地并核哈希与可读性；私有凭据不进入公开包。撤销合成会话和临时凭据，关闭collector，按完整PID/CID精确停本批服务，保留原失败证据。
3. 确认资料安全导出后，由用户在ECS控制台释放本次按量实例，逐项核系统盘/数据盘“随实例释放”设置，以及EIP、独立云盘、快照、OSS临时文件等是否仍保留/计费。释放会永久删除相关数据，不能先释放再找报告。[官方释放说明](https://help.aliyun.com/zh/ecs/user-guide/release-an-instance)、[云盘释放说明](https://help.aliyun.com/zh/ecs/user-guide/release-a-disk)

普通关机不等于停止计费。未启用节省停机模式时，按量实例主动停机仍可能正常收费；即使启用该模式，云盘、EIP、快照等仍可能继续收费。不要把“已经关机”当费用收尾完成，最后到费用与成本页面核实际关联资源及账单。[官方按量计费规则](https://help.aliyun.com/zh/ecs/pay-as-you-go-1)

后续展示素材按[素材计划](showcase-capture-plan.md)整理；本机时基拒绝、历史Feed结果和新的ECS+PTS结果分开，不将平台报告、环境更换或本地接线写成未发生的优化收益。
