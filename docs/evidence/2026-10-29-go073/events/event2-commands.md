# GO-073 event2 执行命令与环境

以下是本轮实际命令和非秘密的本地开发配置；执行时依次在仓库根目录、Go 构建目录以及单独的 API 进程设置相同环境。**本轮命名卷已保留且包含夹具；不要复用该项目执行新的首启实验。** 新一轮须选择新项目、端口和卷，并同步修改脚本的安全白名单。

```powershell
$env:GO_MYSQL_PORT='3411'
$env:GO_REDIS_PORT='6511'
$env:GO_KAFKA_PORT='9121'
$env:GO_ES_PORT='9221'
docker compose -p project-full-stack-go073-event2 -f compose.go.yaml --profile kafka --profile search up -d --wait mysql redis kafka elasticsearch
```

```powershell
$env:GO_ENV='development'
$env:API_ADDR='127.0.0.1:8123'
$env:API_BASE='http://127.0.0.1:8123'
$env:MYSQL_HOST='127.0.0.1'
$env:MYSQL_PORT='3411'
$env:MYSQL_DATABASE='project_full_stack_go'
$env:MYSQL_USER='fullstack'
$env:MYSQL_PASSWORD='fullstack_go_dev_only'
$env:REDIS_HOST='127.0.0.1'
$env:REDIS_PORT='6511'
$env:KAFKA_BROKERS='127.0.0.1:9121'
$env:KAFKA_TOPIC='community.events'
$env:KAFKA_CONTAINER='project-full-stack-go073-event2-kafka-1'
$env:ES_CONTAINER='project-full-stack-go073-event2-elasticsearch-1'
$env:ES_URL='http://127.0.0.1:9221'
$env:ES_INDEX='go073-event-notes'
$env:AI_ENABLED='false'
node scripts/migrate.mjs
```

```powershell
Set-Location backend
go build -o bin/go073-event2-api.exe ./cmd/api
go build -o bin/go073-event2-search.exe ./cmd/note-search
Set-Location ..
$env:GO_SEARCH_REBUILD_OFFLINE='1'
./backend/bin/go073-event2-search.exe rebuild
```

API 和测量脚本分别运行于相同连接环境的两个进程；API 仅连接本项目，`GO_SEARCH_REBUILD_OFFLINE` 仅用于离线重建：

```powershell
./backend/bin/go073-event2-api.exe
```

```powershell
$env:GO_SEARCH_BINARY=(Resolve-Path backend/bin/go073-event2-search.exe).Path
$env:EVENT_OUTPUT=(Join-Path (Resolve-Path docs/evidence/2026-10-29-go073/events).Path 'run-2.json')
node scripts/go073-event-measure.mjs
```

在预热结束、确认稳态后从具有 Docker 访问权限的独立进程运行只读采样：

```powershell
python scripts/go073-event-offset-sampler.py docs/evidence/2026-10-29-go073/events/offset-samples-2.json
```

完成后先停止专用 API，再停止精确项目服务并保留卷：

```powershell
docker compose -p project-full-stack-go073-event2 -f compose.go.yaml --profile kafka --profile search stop --timeout 60 mysql redis kafka elasticsearch
```

实验实际的脚本快照和散列参见 [报告](event2-report.md)，每次输出路径必须为不存在的新文件。ES 停启和 `note-search replay` 由脚本在核对专用 Compose 标签后执行，重放坐标来自本轮停车记录。
