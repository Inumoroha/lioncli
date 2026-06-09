## `serverState`是怎么设计出来的？

```Go
// serverState 保存一个已连接 server 的 client 和它在 Initialize 时声明的 capabilities。
// 我们用 capabilities 来决定要不要去调对应的 list 接口，避免对不支持的 server 发无意义请求。
type serverState struct {
	client       *client.Client
	capabilities mcplib.ServerCapabilities
}
```

把这个设计"逆向演化"一遍给你看

**第 0 版 —— 你 100% 想得到的:**

```go
// 我要连多个 server,那就存一堆 client 呗
servers map[string]*client.Client
```

这一步根本不需要天赋。"多个东西、按名字找" → map。你闭着眼都写得出。

**第 1 版 —— 写到 loadTools 时一头撞墙:**

```go
for name, server := range servers {
    tools, err := server.ListTools(...)   // 等等……
}
```

你这时会"啊"一声:**不是每个 server 都支持 tools 的**。有的只给 resources。对一个不支持 tools 的 server 调 `ListTools`,轻则白跑一趟网络,重则报错。

于是一个**问题**冒出来了(注意:是问题先冒出来,不是设计):

> "我怎么知道这个 server 支不支持 tools?"

**第 2 版 —— 去找这个信息哪来的:**

你不知道有 `ServerCapabilities` 这个东西?**没关系,你马上就会知道。** 因为你回头看握手那行:

```go
res, err := server.Initialize(ctx, initReq)
res.   // ← 你敲个点,自动补全弹出来:res.Capabilities
```

**你不是"事先知道"它,你是"用到的那一刻被类型系统教会"的——这跟 AI 知道它的时机一模一样。** AI 也不是天生记得,它是从无数个"调完 Initialize 就读 Capabilities"的例子里学的。你看一眼返回值类型,就追平了。

**第 3 版 —— struct 自己长出来:**

现在你手上有两个事实:

- `capabilities` 是 Initialize **那一刻**拿到的,但 `ListTools` 是**之后**才调的 → 这中间得**存下来**。
- 它跟 client 一样,是"**每个 server 一份**",而且用**同一个 key**(server 名字)去找。

两个值,**同一个 key、同样的生命周期、总是一起出现** —— 到这儿,struct 不是你"设计"出来的,是它**自己浮出水面**:

```go
type serverState struct {
    client       *client.Client            // 用来发请求
    capabilities mcplib.ServerCapabilities  // 用来决定该不该发
}
servers map[string]*serverState   // 把第 0 版的 map 升级一下
```

看到了吗?**整个过程没有一步需要"想象力"。** 每一步都是被上一步的具体问题逼出来的。

### 那条能复用一辈子的规则

把上面的过程抽出来,就是一条你可以用在**任何**语言、任何项目的规则:

> **当两个(或多个)数据,总是被同一个 key 查找、有同样的生命周期、总是一起出现时 —— 把它们包成一个 struct。**

`client` 和 `capabilities` 完美符合:都按 server 名查、都活到 server 断开、每次用 client 时都可能要看 capabilities。所以它俩进同一个 struct。**这不是审美,是机械判断。**

### 所以"AI 写之前我怎么想得出"的真实答案是

你**不会**在空白页上一次想出 `serverState`。**没人会,AI 也不会**——AI 只是把"边写边撞墙边重构"这个过程在脑子里瞬间跑完了而已。你想得出的方式是:

1. 先写最蠢的第 0 版(`map[string]*client.Client`),**别怕蠢**。
2. 往下写,撞墙(发现要判断 capability)。
3. 顺着类型找到信息源(`Initialize` 的返回值)。
4. 发现"这个值得存、还跟 client 绑在一起" → 重构成 struct。

**设计是写出来的,不是想出来的。** 你缺的不是"事先想到"的天赋,是"**敢写第 0 版、然后允许自己重构**"的习惯。

### 怎么把这个本领真正长出来

下次再用这个包,做个实验:**把 `serverState` 删掉**,强行退回第 0 版 `map[string]*client.Client`,然后试着自己把代码改通。你会在某一行被 capabilities 卡住,然后**亲手把那个 struct 重新逼出来一次**。这一次,它就是你的了——不是"AI 写的我看懂了",是"我自己推导出来过"。这两者在你大脑里留下的东西,天差地别。

## 那到底是不是多对多?——把名词理清就不晕了

你晕,是因为"client"这个词被用在两个层级上了。拆开:

```
        我们的程序(MCPManager)= 1 个
                │
        ┌───────┼───────┐         ← "一对多" 在这一层:1 个程序管 N 个 server
        ▼       ▼       ▼
   serverState serverState serverState
        │           │          │
        ▼           ▼          ▼
     client      client     client    ← 每个 serverState 里:1 个 client
        │           │          │
        ▼           ▼          ▼
   filesystem   everything  chrome     ← 每个 client 连 1 个 server
```

- **一个 `client.Client` 对一个 server**:在 `mcp-go` 里,一个 `Client` 绑定一条 `transport`(一个子进程管道 / 一个 HTTP 会话),它**只通向一个 `server`**。想连 3 个 `server`,就造 3 个 client——这正是 `NewMCPManager` 里那个 `for` 循环在干的事。**不是一个 `client` 连多个 `server`。**
- **"多"在哪**:在 `MCPManager` 这一层。一个程序(host)管多个 server,每个 server 配一个 client。这是**一对多**(1 个 manager → N 个 server),不是多对多。

> 至于"一个 server 能不能被多个 client 连"——理论上一个 server 进程可以接受很多连接,但那是**别的程序也来连它**的事,跟你这个程序无关。**在你这份代码的世界里,就是干净的一对一:一个 server 名字 → 一个 client → 一条连接。** 没有多对多。

## 指针 vs 值:为什么 client 用指针、capabilities 用值

这背后有一条能用一辈子的判断规则:

> **代表"资源/有身份/会变/不能复制"的东西 → 用指针。 代表"事实/快照/不可变的纯数据" → 用值。**

逐个对上:

|              | `client *client.Client`                                     | `capabilities ServerCapabilities`                            |
| ------------ | ----------------------------------------------------------- | ------------------------------------------------------------ |
| 它是什么     | 一条**活的连接**(子进程、管道、内部锁、`goroutine`、缓冲区) | 一份**事实快照**:"这 server 支持 tools/resources/prompts 吗" |
| 有没有"身份" | **有**。这世上只有一条真连接,所有引用必须指向**同一个**它   | **没有**。两份字段相同的 capabilities 完全可互换             |
| 复制它会怎样 | **灾难**。复制等于复制了锁、重复了文件句柄,连接就乱了       | **无所谓**。就是拷几个字段,廉价又安全                        |
| 会不会变     | 它内部状态一直在变(收发数据)                                | 握手后**再也不变**,只读                                      |
| 结论         | **必须指针**——共享同一个,绝不复制                           | **值就好**——人手一份拷贝,互不影响                            |

一句话:**client 是"一个东西"(有身份,要共享),capabilities 是"一些数据"(无身份,可复制)。** 指针是为了"大家指向同一个",值是为了"各拿各的副本互不干扰"。client 属于前者,capabilities 属于后者。

另外补一个你迟早会撞到的细节:`capabilities` 整体是值,但**它内部**的字段是指针——所以代码里判断"支不支持 tools"是写 `s.capabilities.Tools == nil`:

```go
if s.capabilities.Tools == nil {   // nil = 这个 server 没声明 tools 能力
    continue
}
```

外层用值(便宜的快照),内层用指针的 `nil` 当"有没有这项能力"的开关——这是 Go 里表达"可选字段"的常见手法。值和指针在**同一个类型里**按各自的用途混用,不矛盾。

## **为什么 `servers` 是 `map[string]*serverState`(指针)而不是 `map[string]serverState`(值)?**

### 值 map 的问题不是"共享内存",恰恰是"不共享"

Go 的 map value 有一条铁律:**取出来的是副本(copy),不是原件。**

```go
s := m.servers[r.name]   // 值 map 的话:s 是 map 里那个 struct 的一份【拷贝】
s.capabilities = r.caps  // 你改的是【拷贝】,map 里的原件纹丝不动
```

所以**不是**两者指向同一块内存,而是相反:`s` 和 map 里的条目是**两块独立内存**。你把握手结果 `r.caps` 写进了 `s` 这个拷贝,函数一走 `s` 就被丢掉,map 里那条记录的 `capabilities` 还是**零值**。握手白做了。

这就是 [manager.go](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/manager.go) 里这段真实代码必须靠指针 map 才能成立的原因:

```go
for r := range results {
    s := m.servers[r.name]   // 指针 map:s 是 *serverState,指向 map 里那一个
    ...
    s.capabilities = r.caps  // 通过指针改,改的就是 map 里的本体 ✓
}
```

指针 map 时 `s` 是 `*serverState`,`s.capabilities = r.caps` 顺着指针落到**真身**上;值 map 时 `s` 是副本,这行就丢了。

### 还有个更狠的:值 map 你**连这样写都不让**

你可能会想:"那我不取出来,直接改不就行了?"

```go
m.servers[r.name].capabilities = r.caps   // ❌ 值 map:编译都过不了!
```

报错大意是 `cannot assign to struct field ... in map`。**Go 故意禁止它。** 因为 map value 不可寻址(not addressable)——你要改的"那个东西"在 map 里没有稳定地址,编译器干脆不让你写,免得你以为改成功了其实改的是临时副本。

> 而指针 map 没这问题:`m.servers[r.name]` 是个指针(指针本身可以正常取出来用),顺着它改它指向的 struct,完全合法。

### 一句话收口

- **值 map** → 取出即拷贝 → 你的修改进了副本,原件没变(还可能直接编译报错)。
- **指针 map** → 取出的是指针 → 顺着它改的就是本体。

所以 `servers` 用 `map[string]*serverState`,根上的原因是:**这个 struct 在握手后还要被"原地修改"(填 capabilities)。需要原地改的东西放进 map,就得用指针 map。**