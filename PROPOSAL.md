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
  `HeaderString` 描述器、`InFuncMeta` 自定义元数据。
- **M4**：可选代码生成加速器（已无反射，非性能需求，降级为可选）。
