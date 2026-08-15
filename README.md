# web — 端点即数据的 Go Web 框架

泛型、显式优先、非 gin 血统：**路由是类型化的一等值，handler 是普通函数**。
设计来源与完整论证见 [`PROPOSAL.md`](PROPOSAL.md)（Tapir 范式的 Go 化，吸收 axum/FastAPI/Rocket/Huma/ogen 的优点）。

## 设计要点

- **端点即数据**：描述（方法/路径/输入契约/输出契约）与逻辑（纯函数）分离；
  同一契约可挂多个实现、多个 app，测试/文档/OpenAPI（M3）免费；
- **零反射**：类型信息写在描述器值里，全程编译期定死——无 H0..H100、
  无性能车道、无注册期反射魔法；
- **输入/输出契约分离**：输入是显式构造函数（或内置描述器），输出是显式渲染器；
  handler 不接触 Ctx、不接触响应写入器。

## 快速开始

```go
app := web.New()
app.Use(web.Recover(), web.Logger(log.Default()))

app.Must(web.Handle(
    web.Get("/users/{id}"),
    web.PathInt64("id"),   // 输入契约：{id} 解析为 int64，失败自动 400
    web.JSON[*user](),     // 输出契约：JSON
    func(id int64) (*user, error) {          // 纯函数，无 Ctx
        u, ok := users[id]
        if !ok {
            return nil, httperr.NotFound().With("id", id)
        }
        return &u, nil
    },
))
```

完整可运行示例：`examples/basic/main.go` 与 `demo/main.go`（含注册层糖、输入组合、Group）。

## API 速览

| 关注点 | API |
|---|---|
| 端点描述 | `web.Get/Post/Put/Patch/Delete/Head/Options(path)` |
| 绑定逻辑 | `web.Handle(builder, in, out, handlerFn)` —— 扁平调用，类型全推断 |
| 端点复用 | `web.WithIn(builder, in)` / `web.WithOut(endpoint, out)` 嵌套组合 → `contract.Handle(impl)` |
| 注册层糖 | `web.GetJSON/PostJSON/PutJSON/PatchJSON/DeleteJSON/CreatedJSON`、`GetText/PostText`；0 变体 `GetJSON0/GetText0/...` |
| 输入契约 | `web.PathInt64/PathString/PathBool(name)`、`QueryInt/QueryIntDefault/QueryString`、`HeaderString(name)`、`BodyJSON[T]()`、`NoIn()` |
| 描述器约束 | `web.Min(v)/Max(v)/Enum(vals...)` 变参挂在描述器上：运行时违规→400，schema 同步（三合一） |
| 自定义元数据 | `web.InFuncMeta(fn, meta)`：自建描述器自报 OpenAPI 元数据 |
| 请求体校验 | `web.BodyJSON[T](validate...)`：显式校验函数，失败→400 且客户端可见原因（无 tag） |
| 流式输出 | `web.Stream(contentType)`：O = `func(io.Writer) error`，SSE/分块/长轮询的显式契约 |
| 声明式错误响应 | `web.Docs(renderer, map[string]*Response)`：给 OpenAPI 声明 404 等语义，运行时不变 |
| 万能输入 | `web.InFunc(func(r web.Req) (I, error))` —— 任意组合、任意参数个数，100 参数=一个结构体 |
| 输入组合 | `web.All(a, b)` → `In[Pair[A,B]]`；`web.All3(a, b, c)` → `In[Triple[A,B,C]]`；错误短路 |
| 输出契约 | `web.JSON[T]() / Text() / Status(code, inner) / NoContent[O]() / Redirect[O](code, url)` + 自定义 `Renderer[O]` |
| JSON 引擎 | 默认 [goccy/go-json](https://github.com/goccy/go-json)；构建标签 `std_json`/`jsoniter`/`sonic` 切换（同 gin 机制） |
| 错误信封 | 默认 `{"error":...}`；`app.UseProblemJSON()` 切 RFC 7807（typed fields 并入响应） |
| 通配符路径 | `web.PathRest(name)`：`{name...}` 段的输入契约 |
| 查询补全 | `QueryFloat64`（可带约束）、`QueryStrings`（多值数组） |
| Cookie | 读：`r.Cookies().Get(name)`；写：`web.SetCookie(rd, cookie)` 输出契约包装（可链式） |
| 文件上传 | `web.FormFile(name, maxBytes)` → `web.Upload{Name,Content,Size}` |
| 请求访问器 | `web.Req`：`r.Path().Int64("id")`、`r.Query().Int("page")`、`r.Header().Get`、`web.DecodeBody[T](r)`、`r.Context()`、`r.Raw()`（逃生舱） |
| 错误 | `httperr.New(code, msg).Wrap(err).With(k, v)`；handler/构造器返回错误即映射状态码 |
| 中间件 | `func(next Handler) Handler` 显式高阶函数；`app.Use` 全局、`route.With` 路由级 |
| 分组 | `app.Group(prefix, mws...)` → `g.Must(routes...)` |
| 类型安全键 | `web.NewKey[T]("name")` → `key.Set(c, v)` / `key.Get(c)` |
| 路由语法 | Go 1.22 风格 `{param}` 与 `{path...}`；冲突挂载期报错；自动 405/OPTIONS、HEAD→GET |
| 逃生舱口 | `web.Raw(method, path, web.Handler)` —— 手写热路径/流式/协议升级 |
| 标准库兼容 | `web.FromStd(h)` / `FromStdFunc(fn)` —— net/http 中间件一行适配；`web.Static(prefix, dir)` 静态文件 |
| 中间件电池 | `RequestID`（类型键+响应头）、`Timeout(d)`、`BodyLimit(n)`、`CORS(cfg)`；预检用 `app.UseCORS(cfg)`（App 级应答，鉴权中间件不会拦预检） |
| 响应头契约 | `web.WithHeader(rd, name, value)`：渲染时设置 + OpenAPI 同步（可链式） |
| 单测助手 | `web.ServeRoute(route, req)` —— 路由即数据，一行测试一个端点 |
| 中间件电池续 | `Compress()`（流式 gzip，免缓冲）、`RateLimit(rps, burst, key)`（令牌桶→429）、`CacheControl(s)`/`NoCache()` |
| SSE 帮手 | `web.SSE()`：O = `func(*SSEWriter) error`，类型化 `Event().Data/Text` + `Ping()`，自动 flush |
| 嵌套分组 | `g.Group(prefix, mws...)`：前缀与中间件组合 |
| 路由元数据 | `route.Method()` / `route.Path()` |
| OpenAPI | `app.Doc(web.Info{...})` 从挂载路由直接生成 3.0 文档；描述器自动携带参数/请求体/schema 元数据，挂一条 `/openapi.json` 路由即服务 |

## 性能（实测，AMD Ryzen 7 8845HS，Go 1.26，vs gin v1.10 同机，双方各自默认引擎）

`cd benchmarks && go test -bench . -benchmem -benchtime=1s`：

| 用例 | web | gin | 结果 |
|---|---|---|---|
| 静态 JSON | 150.6 ns，64 B，2 alloc | 205.4 ns，64 B，2 alloc | **快 27%** |
| 参数 JSON（含 int64 解析） | 183.4 ns，64 B，2 alloc | 233.0 ns，64 B，2 alloc | **快 21%** |
| 5 层中间件 | 88.7 ns，16 B，1 alloc | 90.7 ns，48 B，1 alloc | 快 2% |
| 静态文本 | 89.0 ns，16 B，1 alloc | 77.4 ns，48 B，1 alloc | 慢 15%，字节 1/3 |
| 404 | 69.8 ns，16 B，1 alloc | 42.5 ns，0 B，0 alloc | 错误路径；gin 404 几乎不做事 |

**零反射 + 引擎优势**：默认 JSON 引擎为 goccy/go-json（纯 Go、任何 Go 版本可用；
构建标签 `std_json`/`jsoniter`/`sonic` 可切换，与 gin 的引擎切换同机制）。
profile 驱动优化：冻结响应头切片（跳过 Set 规范化）、糖入口直编闭包（跳过渲染器
接口分派）、叶节点切片分派（跳过 map 查找）、参数切片随 Ctx 单池。
分配预算由 `alloc_test.go` 作为 CI 硬约束（文本 ≤1、参数 ≤1、JSON ≤3）。

### 真实端点对照（examples/tasks 同语义，双方惯用写法 + 各自默认引擎）

| 端点 | web | gin | 结果 |
|---|---|---|---|
| GET /tasks 分页+过滤 | 4304 ns | 4366 ns | 打平 |
| GET /tasks/{id} | 242 ns，2 alloc | 302 ns，3 alloc | **快 20%** |
| GET 404 | 294 ns，3 alloc | 599 ns，7 alloc | **快 51%** |
| POST 创建（body 绑定） | 793 ns，7 alloc | 1452 ns，13 alloc | **快 45%** |
| PUT 更新（path+body） | 738 ns，7 alloc | 1457 ns，13 alloc | **快 49%** |
| POST done（409） | 279 ns，2 alloc | 599 ns，7 alloc | **快 53%** |
| 未鉴权 401 | 204 ns，2 alloc | 521 ns，7 alloc | **快 61%** |

来源：body 路径 gin 的 ShouldBindJSON 每请求反射 vs 本框架 goccy 直解码；
错误路径得益于错误体缓存（同 code+msg 复用字节）。

## 布局

```
endpoint.go   端点描述：Builder/In/描述器/Bound/Route/Handle/Raw + 注册层糖 + 输入组合
openapi.go    OpenAPI 3.0 生成：schema 模型、描述器元数据、app.Doc()
req.go        web.Req 只读访问器（Path/Query/DecodeBody/Context）
render.go     Renderer[O] 渲染器与错误响应写入
router.go     radix tree（段内前缀合并 + {param}/{path...}）
ctx.go        Ctx 池化、类型键
app.go        App：Mount/Must/Group/ServeHTTP、405/OPTIONS/HEAD
middleware.go Recover、Logger（报告错误映射后的有效状态码）
httperr/      类型化 HTTP 错误
examples/tasks/ 完整参考应用：CRUD + 鉴权 + 分页/过滤 + 状态机 + OpenAPI + 优雅停机 + 端到端测试
demo/         最小 demo（两种注册风格 + 组合 + Group）
examples/     完整示例
benchmarks/   独立模块：vs gin、stdlib ServeMux
```

## 测试

```bash
go test ./...          # 单元 + 分配预算 + 路由语义
go test -race ./...    # Ctx 池契约
cd benchmarks && go test -bench . -benchmem -benchtime=1s
```
