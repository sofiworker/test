# 生产化审阅包（待审阅 · 未提交）

自 `7a43da0`（M9）以来的全部改动都在工作区，**未提交、未推送**。
审阅方式：

```bash
git diff 7a43da0            # 全部变更
git status --short          # 变更清单（31 项）
```

## 变更总览

| 轮次 | 主题 | 主要文件 |
|---|---|---|
| M10 | 链式构建器 + Go 1.27 双 API + OpenAPI 自动反推 | builder.go、builder_go127.go、openapi.go、endpoint.go |
| M11 | 路由优先级排序 + 零分配错误路径 + slog 日志体系 | router.go、render.go、middleware.go |
| M12 | WebSocket（gorilla）+ SSE 补全 + Serve/PProf | ws.go、render.go、serve.go、pprof.go |
| M13 | 可信代理 + 文件输出 + SPA + 模糊/覆盖率起步 | trust.go、compat.go、fuzz_test.go、prod_test.go |
| M14 | matcher 重写（消除每段 IndexByte） | router.go |
| 测试 | k6 压测脚本 + 冒烟脚本 | scripts/k6.js、scripts/smoke.sh |

## 关键决策（需你确认）

1. **gorilla/websocket v1.5.3 进入 go.mod**（第 ⑨ 条明确要求评估采纳——已采纳）；
   零依赖承诺因此调整为"仅一个 websocket 依赖"。
2. **builder_go127.go 用 `//go:build go1.27` 门控，当前 1.26 无法编译验证**
   （1.27.0 工具链尚不可下载）；若 1.27 泛型方法未如预期落地，删除该文件即可，
   `NewChain` 形态不受影响。
3. **验证流程调整**：因 1.27 文件 gofmt 会报类型错误，统一改为仅 gofmt
   `go list` 报告的实际构建文件。

## 验证证据

- 单元测试 + race：全绿（含 examples/tasks 端到端）；
- 模糊测试：路由 1.7M 执行、提取器 336K 执行，零 panic；
- 覆盖率：84.1%；
- 冒烟：11 项生命周期断言全过（scripts/smoke.sh）；
- k6：60s 爬坡压测 66K 请求、~1100 RPS、p95 24.5ms、0 服务端错误；
- 横评（gk 13 场景，同工同酬）：对 gin 8 胜 3 极小负项（≤20ns），
  对 ghttp 全胜；并发争用：静态 2.7× gin、全链路 1.75× gin。

## 待你审阅后

确认后执行：`git add -A && git commit`（由你或经你同意后由我执行）。
