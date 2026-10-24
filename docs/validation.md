# 首批基线验证记录

日期：2026-10-24。

| 验证 | 结果 | 覆盖与边界 |
| --- | --- | --- |
| 后端 `mvn test package` | 59 项通过，0 失败、0 错误 | 鉴权、编码/矩阵路径绕过、控制器写入兜底、缓存、订单幂等/事务路径、H2 商家门店范围 |
| 前端 `npm run build` | 通过 | 包含 vue-tsc 类型检查和 Vite 生产构建 |
| 前端 `npm run format:check` | 通过 | 源文件与配置格式 |
| Chrome + Mock API | 9 项场景通过 | 浏览、开发验证码、登录跳转、单次抢购提交、异步完成、大整数 ID、刷新恢复、商家门店、401；不等于真实后端联调 |
| Docker Compose 配置检查 | 通过 | `docker compose config --quiet`；未实际创建服务 |
| 文档链接和迁移环境检查 | 通过 | 本地链接有效；未导入原课程环境地址和凭证；构建产物/本地环境文件已忽略 |
| GitHub Actions 干净环境构建 | 前后端通过 | Java 21 / Node.js 24；[首批基线运行记录](https://github.com/tech-cove01/project-full-stack/actions/runs/40091125874) |
| MySQL + Redis HTTP 联调 | 未执行 | 本机 Docker Desktop 引擎不可用；提供 `scripts/smoke.py` 供环境就绪后运行 |
| k6 / 并发与故障实验 | 未执行 | 已提供店铺查询负载脚本，尚无性能结果 |

本地构建使用 JDK 24.0.1 编译到 Java 21，Node.js 24.15.0。GitHub Actions 配置使用 Java 21 和 Node.js 24，远端结果以仓库 Actions 记录为准。

下一步优先完成真实依赖环境联调，再实现商家活动发布、核销和售后闭环，最后基于同一环境做负载与故障实验。
