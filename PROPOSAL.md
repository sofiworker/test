# 设计提案 v3：端点即数据（Tapir 范式 Go 化）——已批准并实现

> **状态：已批准（2026-08-14）、已实现。** 实现与提案的差异：
> 1. Go 禁止泛型方法，提案里的链式 `Get→In→Out→Handle` 不可编译；实现采用
>    `web.Handle(builder, in, out, fn)` 扁平自由函数（全类型推断）为主入口，
>    `web.WithIn`/`web.WithOut` 嵌套组合用于端点复用，`Bound.Handle` 方法保留；
> 2. `path.Param[T]` 位置绑定提取器被 `web.PathInt64(name)` 等按名描述器取代，
>    消除了位置歧义；`path`/`req` 子包已删除，职责并入 `web.Req` 访问器；
> 3. 结构体输入（旧"字段名绑定"）被显式 `web.InFunc` 构造函数取代——无隐性绑定；
> 4. 依赖注入 v1 用闭包捕获（已批准），`web.Provide`/反射注入层删除；
> 5. 注册层糖（GetJSON/PostJSON/0 变体）、变参 `Must`、`Group`、
>    输入组合 `All/All3/Pair/Triple` 已加入（均为一行的包装或纯数据类型，零新机制）。
>
> 实测结果与提案 §3 预期一致（见 README 性能表）：文本 +10.8%、JSON +8.3%、
> 参数 +11.2%、五中间件 +5.6%，零反射、无车道。

---

（以下为提案要点保留，作为设计论证。）

## 0. 三路调研结论摘要

| 调研 | 关键结论 |
|---|---|
| Go 泛型框架（Huma/ogen/papi/fuego/chai/simba/foundation） | 共同模式：**结构体声明请求 + 泛型进路由签名 + 自动 OpenAPI**；核心分歧在反射时机（运行期/注册期/代码生成）；Echo v5 拒绝泛型（"反射慢、tag 易错、不 Go 化"）；foundation 实质废弃 |
| 其它语言（axum/Rocket/FastAPI/Hono/tRPC/Tapir） | axum：extractor + State + IntoResponse，元数上限 16 藏在宏里；Rocket：类型即解析器（FromParam）；FastAPI：签名即契约 + Depends DI + 自动 OpenAPI；tRPC 端到端类型安全在 Go 只能靠 codegen |
| Go 能力边界 | 任意函数类型提案 [#41478](https://github.com/golang/go/issues/41478) 未实现；泛型方法可能进 1.27 未定案；变长泛型 [#66651](https://github.com/golang/go/issues/66651) 无期；`reflect.Call` 实测 17–47 倍慢且每次分配；**工程上只有三条路：注册期反射（[Huma](https://huma.rocks/why/benchmarks/)、fuego 路线）、代码生成（[ogen](https://ogen.dev/blog/ogen-intro/) 路线）、具体形状类型断言快车道** |

## 1. 硬约束（设计的地基）

1. Go 无法表达"任意函数签名"（[#41478](https://github.com/golang/go/issues/41478) 至今无进展），所以"一个 `app.GET(path, fn)` 吃任意 handler"必须靠注册期反射或代码生成——**没有第三条零成本的路**。上一版 `H0..H100` 是试图绕开这个约束的错误绕法，已废弃。
2. `reflect.Value.Call` 每请求 17–47 倍慢（[实测](https://datasea.cn/go0813710472.html)），因此**热路径必须走类型断言快车道或代码生成**。
3. 元数上限连 axum 都躲不开（宏生成到 16），只是它把上限藏进了宏。

## 2. 从各框架吸收的模式

| 来源 | 模式 | 本框架怎么用 |
|---|---|---|
| [FastAPI](https://fastapi.tiangolo.com/tutorial/dependencies/) | 签名即契约 | 输入契约由描述器/构造函数显式声明 |
| [axum](https://deepwiki.com/tokio-rs/axum/4.1-core-extractor-traits) | extractor 是类型化单元；tuple 组合 | `In[I]` 描述器 + `All/All3` 组合 → `Pair/Triple` |
| [Rocket](https://rocket.rs/guide/v0.5/requests/) | 类型即解析器（FromParam） | 自定义类型在 `InFunc` 里按需解析 |
| Huma/[Fuego](https://github.com/go-fuego/fuego) | 结构体声明请求 + 自动 OpenAPI | 结构体输入（多参数的答案）；OpenAPI 走 M3（端点即数据，零反射） |
| [ogen](https://ogen.dev/blog/ogen-intro/) | 代码生成 = 零反射 | 本范式已零反射；M4 代码生成降级为可选 |
| Echo v5 [拒绝泛型的理由](https://github.com/labstack/echo/issues/2454) | "反射慢、tag 易错、不 Go 化" | 零反射、零 tag、net/http 兼容 |
| [Tapir](https://tapir.softwaremill.com/en/latest/server/logic.html) | **端点即数据**：描述与逻辑分离 | **核心范式** |

## 3. 核心 API（现行实现）

```go
// 完整契约（Handle）+ 注册层糖（GetJSON 等）+ 输入组合（All）
app.Must(web.GetJSON("/users/{id}", web.PathInt64("id"),
    func(id int64) (*User, error) { ... }))

app.Must(web.PutJSON("/users/{id}",
    web.All(web.PathInt64("id"), web.BodyJSON[user]()),
    func(p web.Pair[int64, user]) (user, error) { ... }))
```

## 4. 路线图

- **M1''**（已完成）：统一 API + 零反射执行 + 结构体输入 + 基准 ≈ gin。
- **M2**（进行中）：query/body 描述器扩展、`All/All3` 组合（已落）、更多组合子。
- **M3**（v1 与深化已完成）：OpenAPI 生成——`app.Doc(info)` 从挂载路由直接产出 3.0 文档；
  内置描述器自动携带参数/请求体元数据，`All/All3` 组合元数据自动合并；
  类型 schema 仅在文档构建时内省一次（启动期），请求路径依旧零反射；
  深化：描述器约束 `Min/Max/Enum`（运行时 400 + schema 同步）、
  `HeaderString` 描述器、`InFuncMeta` 自定义元数据；
  M3.2 输出与校验深化：`BodyJSON[T](validate...)` 显式校验钩子（400 携带原因）、
  `Stream(ct)` 流式输出契约（SSE/分块）、`Docs(rd, extras)` 声明式错误响应、
  `PathFloat64`/`QueryBool` 描述器、map 类型 schema、递归类型内省守卫。
- **M4**：可选代码生成加速器（已无反射，非性能需求，降级为可选）。

### M3.3 工程化装备（已完成）

- `FromStd/FromStdFunc`：net/http 生态一行适配；`Static(prefix, dir)` 静态文件；
- 中间件电池：`RequestID`（类型键 + 响应头 + 透传入站 id）、`Timeout`、`BodyLimit`、
  `CORS`（打标）+ `UseCORS`（预检在 App 级应答——路由器的自动 OPTIONS 先于中间件链，
  鉴权中间件不应拦截预检，这是架构级修正）；
- `WithHeader(rd, k, v)`：响应头进入输出契约，渲染时设置 + OpenAPI response.headers 同步；
- `ServeRoute(route, req)`：路由单测一行化。

### M4 性能收官（已完成）

- 默认 JSON 引擎切换为 goccy/go-json（纯 Go、无汇编依赖），构建标签
  `std_json`/`jsoniter`/`sonic` 保留（与 gin 的引擎切换机制一致；
  sonic 需 avx+amd64 组合标签，Go 1.26 下上游暂不可用）；
- profile 驱动三项框架侧优化：冻结响应头切片（跳过 Set 键规范化）、
  糖入口直编闭包（跳过 Renderer 接口分派）、叶节点切片分派（跳过 map 查找）、
  参数切片随 Ctx 单池；
- 结果：静态 JSON 快 gin 27%、参数 JSON 快 21%、五中间件快 2%；
  文本慢 15%（gin 的 String 路径极瘦，字节数仍为 gin 的 1/3）。

### M17 审阅整改：单一 API + 纯标准库 JSON + 渲染补全（工作区，待审阅）

用户审阅后的五项整改：
1. **单一 API**：删除链式构建器（NewChain/builder_go127.go），保留 Tapir 式扁平
   `Handle` + `WithIn/WithOut` 为唯一形态；文档选项改为 `route.Doc(...)` 挂载；
2. **纯标准库 JSON**：删除全部构建标签文件（std_json/go_json/jsoniter/sonic）与
   第三方 JSON 依赖（go.mod 仅剩 gorilla/websocket）；新增 `UseJSONCodec(m, u)`
   注入点，序列化实现完全外部化；
3. **渲染/非 JSON 补全**：`XML[T]()`、`HTML[T](tmpl, name)`、`Bytes(ct)`、
   `FormValues()`（urlencoded → url.Values 显式映射）；静态文件 Static/SPA 已有；
4. **命名清理**：删除 GetText0/GetJSON0 等 0 变体家族，无输入路由统一
   `NoIn() + web.None` 唯一形态；
5. **MapIn/MapIn3 组合子**：`MapIn(a, b, func(A,B) T)` 映射到自定义命名结构体，
   解决 Pair 命名问题（Pair/All 保留）；错误短路语义与 All 一致；
   自评估修正：补上 OpenAPI 元数据合并（组合输入的参数/请求体不因自定义
   映射丢失，与 All/All3 一致）。
- 性能基线更新为标准库同引擎（JSON 微基准慢 10-23%，端点级 5 胜 2 平不变；
  注入 goccy/sonic 可恢复编码密集路径优势）；
- 全部测试 + race 全绿；gk 基准适配器同步更新。

### M16 超大规模压力 + 超长参数 SIMD（工作区，待审阅）

- 压力基准 `benchmarks/stress_test.go`：2 万资源 × 6 形态 = **12 万条混合路由**
  （静态 GET/参数 GET/POST/PUT/DELETE/通配符），含状态码一致性断言；
- 注册性能：web 163ms vs gin 62ms（惰性排序修复后均为百毫秒级）；
- 首字节分派表 int16（内存减半）；param 段尾扫描换 SIMD `IndexByte`——
  **超长参数（2000 字符）771→136ns，反超 gin 4.2 倍**；
- 12 万路由稳态（stdlib 同引擎）：静态 94.4 vs 109.5（快 14%）、参数打平、
  通配符慢 23ns、深层未命中慢 34ns、分支未命中慢 16ns、超长路径未命中打平；
  405 慢 3× 属语义差异（web 写 Allow+JSON body 3 alloc，gin 直接空 404 0 alloc）；
- 正确性：状态码一致性测试（含 405/404/200 各场景）通过；模糊 15s 通过。

### M15 规模化路由：首字节分派 + 惰性排序（工作区，待审阅）

用户追问"5 参数与 200 路由为何比不过 gin、更大规模如何"的完整答复：
1. 规模化基准实测（200/1000/5000 路由）：旧实现随规模增长（静态 87→120ns），
   gin 平坦——原因定位为**宽节点（数字兄弟）线性扫描**（memeq 占 match 22%）；
2. 修复一：**首字节分派表**（httprouter 同款思想）——权重排序时对 ≥4 子节点且
   首字节互异的节点一次性构建 [256]int32 索引，匹配 O(1) 跳转；
3. 修复二：顺带发现**注册性能 bug**——旧实现每次 Mount 全树 sortByWeight
   （5000 路由注册 O(N² log N) 数秒），改为首次请求 sync.Once 惰性排序；
4. 结果（stdlib 同引擎）：规模化 200/1000/5000 **静态全反超 22-37%、参数全打平
   或反超**，且 web 随规模保持平坦（59.6→70.6ns）；gk 13 场景对 gin 收窄至
   **9 胜 1 平 3 负（负项 ≤16ns）**；5 参数 172.9 vs 159.1（14ns，剩余为输入
   构造链的固定成本，本地基准反超）；NotFound 反超（34.7 vs 47.2）。

### M14 生产化第 5 轮：matcher 重写，Param5 收口（工作区，待审阅）

- 路由 match 重写为路径直走：静态子节点用"前缀+边界字符"直接判定，消除旧实现
  每段一次的 IndexByte 段尾扫描（profile：match+nextSeg 占 37.5%）；
  参数/catch-all 才扫段界；radix 段内分裂由"边界未到继续深入"自然表达；
- 结果（gk 13 场景体系，同工同酬）：对 gin 8 胜 3 极小负项（全部 ≤20ns）——
  静态 +23%、通配符 +21%、JSON 绑定 +68%、JSON 响应 +44%、中间件 +25%、
  全链路 +57%、Query +6%、Param1 +4%；Param5 打平（本地基准反超 5%）、
  Scale200 慢 11%（10ns）、NotFound 慢 5.5ns；对 ghttp 保持全胜；
- 正确性保障：全部路由语义测试 + race + 20s 模糊测试（1.7M 执行）通过。

### M12 生产化第 3 轮：WebSocket/SSE 补全 + 生产设施（工作区，待审阅）

- WebSocket（采纳 gorilla/websocket v1.5.3）：升级是输入契约（`WSConn`/
  `UpgradeWS(u)`），"升级后不写响应"是输出契约（`Upgraded[O]()`）；Ctx 增加
  hijacked 标记，升级后框架不再触碰响应流与错误管道；升级失败走正常错误管道
  （400）；echo 端到端实测通过；
- SSE 补全：`SSEEvent.ID/Retry`、`SSEWriter.Comment`（id/retry/注释字段）；
- 生产设施：`app.Serve(addr)` 信号优雅停机（10s 排空）、`PProf()` pprof 路由组
  （尾斜杠与深层路径双路由，`app.Must(web.PProf()...)`）。

### M13 生产化第 4 轮：可信代理/文件输出/测试体系起步（工作区，待审阅）

- `TrustedProxies(cidrs...)`（参照 ghttp trust.go）：仅受信 CIDR 内的直连对端
  才接受 X-Forwarded-For/X-Real-IP，XFF 从右向左跳过受信跳；`Req.ClientIP()`
  读取结果，无中间件时回退 RemoteAddr；
- 文件输出：`Download(filename)`（Content-Disposition 附件）、
  `StreamFile(ct)`（io.ReadSeeker 流式，先 Seek 后 io.Copy）；
- `SPA(prefix, dir)`：存在即服务、缺失回退 index.html（根/非根前缀/深路由测试）；
- problem+json 补 `instance` 字段；`Healthcheck()` 标准 /healthz 路由；
- 测试体系起步：Go 原生模糊测试（路由 130 万次执行、提取器 33 万次，零 panic）；
  覆盖率 82.8% → 84.1%；k6 压测与冒烟脚本留待全部设计完成后执行。

### M10 生产化第 1 轮：链式构建器 + OpenAPI 自动反推（工作区，待审阅）

- 链式构建器 `NewChain(app, in, out).GET(path).Doc(...).With(...).To(fn)`：
  契约在起点声明、类型由描述器推断、方法链固定类型参数（<1.27 形态）；
- `builder_go127.go`（`//go:build go1.27`）：1.27 泛型方法标准形态
  `app.Route().GET(path).MustTo[I,O](in, out, fn)`——工具链 1.27.0 尚不可下载，
  无法本地编译验证，已注明回退方案；验证流程改为仅 gofmt 实际参与构建的文件；
- OpenAPI 文档选项 `Doc(Summary/Description/Tags/OperationID/Deprecated)`：
  未声明字段自动反推（summary="METHOD path"、operationId 由 method+path 生成且
  保留参数花括号防撞名）；演示见 demo `/v3/users/{id}`。

### M11 生产化第 2 轮：路由/热路径优化 + 日志体系（工作区，待审阅）

- 路由：静态子树权重排序（注册期静态优先级，运行期零变更、race 安全）；
  Scale200 场景 28%→18% 差距；
- 零分配错误路径：writeJSONErrorStatic 冻结头直写 → 404 场景 **0 alloc、
  46.0ns 与 gin 打平**（原 80.6ns/1alloc）；
- 日志体系分层定义：`LoggerSlog`（slog 结构化：method/path/有效状态码/时长/
  request_id/错误）、`RecoverSlog`、`SecureHeaders`（基线安全头）、
  `SlogContext`（类型键注入 logger）；全部测试覆盖；
- 已知待优化：Param5 场景仍慢 gin 24%（参数提取链，文档化跟踪）。

### M9 gk 体系接入与跨框架排名（已完成）

接入 gk 的 13 场景基准体系（`benchmarks/web_test.go`，同工同酬对齐 handler 语义）：
- 对 ghttp 11 场全胜（1.05×–12×；其每请求固定开销 ~700–1100ns）；
- 对 gin 7 胜 1 平 3 负（静态/JSON 绑定/JSON 响应/中间件/全链路领先 21%–66%；
  3 个负项绝对值 <100ns）；
- 本轮优化：`PutText/PatchText/DeleteText` 补全文本糖家族；**Ctx 查询解析缓存**
  （Query×3 场景从慢 58% 反转为快 6%，全链路 18→14 alloc）。

### M8 同引擎公平对照（已完成）

`-tags std_json` 回退标准库后（双方同引擎），端点级 6/7 仍反超（4%–68%）：
body 绑定与错误路径的优势是设计红利、与引擎无关；微基准 5–11% 框架开销
是"接近 gin"承诺的无水分基线；goccy 默认引擎为纯增量（encode 密集路径
+5.8% → -27%）。修复：codec.go 补负向构建约束（-tags std_json 编译冲突）。

### M7 真实端点对照基准（已完成）

`benchmarks/tasks_bench_test.go`：examples/tasks 的同一批端点（分页列表/读写/
组合更新/状态机/鉴权失败/404），web 与 gin 各自惯用写法、相同校验与状态码：
7 个端点中 6 个 web 显著更快（20%–61%），分页列表打平。
关键优化：错误体缓存（writeJSONError 按 code+msg 复用已序列化字节，
错误路径 7 alloc → 2-3）。

### M6 参考应用（已完成）

`examples/tasks/`：唯一完整样例（demo 与 examples 已合并于此），覆盖渲染全景、表单/上传、SSE/WebSocket、静态/SPA、CRUD/鉴权/分页、OpenAPI、优雅停机——
- 资源组 + 组中间件（Bearer 鉴权、类型键）；路由级中间件 `.With(requireAuth)`；
- 分页/过滤（InFunc 显式校验）、path+body 组合更新（`All`）、状态机 409、
  204 删除、`BodyJSON` 校验钩子；
- OpenAPI 文档即路由、RequestID/Recover/Timeout/Logger/CORS 电池、优雅停机；
- 端到端测试覆盖生命周期/分页/鉴权/文档。
- 用法要点：**组内根路由用空路径**（`GetJSON("")`，前缀拼接后才是完整路径，
  避免尾斜杠禁令）。

### M5 完整性收尾（已完成）

- 错误模型：`app.UseProblemJSON()` 切换 RFC 7807 problem+json，httperr 的
  `With(k,v)` 结构化字段作为额外成员并入响应；默认信封不变；
- 通配符路径描述器 `PathRest(name)`；
- Cookie：读访问器 `r.Cookies().Get(name)`、输出契约包装 `SetCookie(rd, ck)`；
- 查询补全：`QueryFloat64`（带约束）、`QueryStrings`（多值数组，OpenAPI 数组 schema）；
- 文件上传描述器 `FormFile(name, maxBytes)` → `Upload{Name, Content, Size}`，
  multipart 解析错误自动 400。

### M3.4 生产级中间件与流式深化（已完成）

- `Compress()`：流式 gzip（Accept-Encoding 探测、免缓冲、Content-Length 剔除、Flusher 透传）；
- `RateLimit(rps, burst, key)`：进程内令牌桶，超限 429（文档注明横向扩展需外置存储）；
- `CacheControl(s)` / `NoCache()`：缓存头；
- `web.SSE()`：类型化事件写入器（`Event().Data/Text`、`Ping()`、自动 flush），取代裸 Stream 的样板；
- 嵌套 `Group.Group(prefix, mws...)`：前缀与中间件组合；
- `Route.Method()/Path()`：路由元数据访问器。
