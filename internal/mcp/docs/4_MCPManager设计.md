```gO
// MCPManager 管理所有连接到的 MCP server，并把它们提供的 tools/resources/prompts
// 聚合成统一视图。具体的领域操作分散在同包的 tool.go / resource.go / prompt.go，
// refresh / 通知通路在 refresh.go——这里只放骨架（生命周期 + 共享状态）。
type MCPManager struct {
	servers   map[string]*serverState
	tools     map[string]toolEntry     // key 为 keyOf(serverName, originalName)
	resources map[string]resourceEntry // key 为 keyOf(serverName, originalURI)
	prompts   map[string]promptEntry   // key 为 keyOf(serverName, originalName)

	// mu 保护上面三张 map。读路径走 RLock，refresh 写路径走 Lock。
	// servers 不在 mu 范围内：握手阶段独占式构建，之后只读。
	mu sync.RWMutex

	// refreshCh 由 OnNotification handler 写入、worker 读取，
	// 把 transport 读循环的同步回调与实际 RPC 调用解耦。
	refreshCh chan refreshReq
	done      chan struct{}
	wg        sync.WaitGroup

	// cbMu 保护下面三组回调切片。回调由 worker 在写锁释放后调用，
	// 这样回调里反过来调 mgr.AllLLMTools() 不会与 refresh 自身死锁。
	cbMu         sync.Mutex
	toolsCBs     []func()
	resourcesCBs []func()
	promptsCBs   []func()
}
```

## `MCPManager` 结构体: 它不是 "设计" 出来的, 是被问题 "逼" 出来的

> 这份文档回答一个问题: **为什么 `MCPManager` 有这么多看起来想不到的字段?**
>
> 核心结论先放这儿:
>
> **你想不到那些字段, 不是因为你笨, 是因为它们根本不是 "想" 出来的——是一个个具体的并发问题逼出来的。你看到的是最终成型的结构体, 像一口气设计好的; 但没有人是这么写出来的。**

下面把这个结构体 "长出来的过程" 倒放一遍。每个让人头疼的字段, 都是上一版撞墙之后的**唯一解**。

### 第 0 版: 你能想到的(而且它没错!)

```go
type MCPManager struct {
    servers   map[string]*serverState
    tools     map[string]toolEntry
    resources map[string]resourceEntry
    prompts   map[string]promptEntry
}
```

**如果这个程序是单线程的, 到此为止, 完美, 够用。**

你能想到这 4 个, 因为它们回答的是 "这东西要**装**什么"——这是数据建模, 是正常人的思维方式。

剩下所有字段, 只为一件事存在: **不止一个 goroutine 在碰这几张 map。** 一旦并发进来, 问题一个接一个砸过来, 每砸一个, 就被迫加一个字段。

> [!NOTE]
>
> ## 三类 goroutine,各自为什么碰 map
>
> |       | 谁(goroutine)                   | 什么时候                    | 干嘛(读/写)                             | 代码位置                                                     |
> | ----- | ------------------------------- | --------------------------- | --------------------------------------- | ------------------------------------------------------------ |
> | **1** | **主 goroutine**(启动期)        | `Initialize()` 里           | **写**:首次把清单灌进三张 map           | `loadTools` [tool.go:75](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/tool.go#L75) / `loadResources` [resource.go:89](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/resource.go#L89) / `loadPrompts` [prompt.go:104](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/prompt.go#L104) |
> | **2** | **调用方 goroutine**(运行期·读) | 任何时候,LLM 要用东西时     | **读**:取工具列表、查某个工具去调用     | `AllLLMTools`/`CallTool` [tool.go:112](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/tool.go#L112),[124](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/tool.go#L124);资源、prompt、`StatusSnapshot` [status.go:24](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/status.go#L24) 同理 |
> | **3** | **worker goroutine**(运行期·写) | server 推来"列表变了"通知时 | **写**:删掉该 server 旧条目、灌入新条目 | `refreshToolsFor` 等 [refresh.go:113](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/refresh.go#L113),[150](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/refresh.go#L150),[187](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/refresh.go#L187) |
>
> ## 把"为什么"讲透——危险的是哪两对碰撞
>
> 光列出来还不够,关键是**谁和谁会撞**。`mu` 这把锁防的就是下面这两种同时发生:
>
> **碰撞 A:2读3写(最主要的理由)**
>
> ```
> LLM 正在 CallTool         → 读 m.tools(找 "mcp__fs__read_file")
>                               ↕  同一瞬间
> 某 server 推送通知,worker  → 改 m.tools(delete 旧的 + 写新的)
> ```
>
> 一个在遍历/查 map,另一个在 `delete`+写。Go 的 map 撞上这个直接 `fatal error: concurrent map read and map write`,**整个进程崩**。这是 `mu` 存在的头号原因。
>
> **碰撞 B:1写2读**
>
> 启动期 `loadTools` 还在往 map 灌数据,但如果此时上层已经能发起调用(比如 UI 先起来了),`AllLLMTools` 就来读了——写一半的 map 被读,同样是并发读写。所以连启动期的首次灌入也得持写锁。
>
> > 而3和3自己之间**不会**撞:worker 只有一个(单 goroutine,见 `startWorker`)。所以"写×写"这种竞争被设计上规避了——这也是为什么作者特意只开**一个** worker,而不是来一个通知开一个 goroutine。
>
> ## 一句话回答你
>
> > **三类 goroutine 在碰这三张 map:① 启动期的主 goroutine 写一次(预加载);② 运行期调用方 goroutine 不停地读(LLM 用工具);③ 运行期单个 worker 在收到通知时写(刷新)。**
> >
> > 真正逼出 `mu` 的,是 **②的读和 ③的写 会在运行期任意时刻同时发生**——map 撑不住并发读写会让整个程序崩,所以必须加锁。

### 第 1 版: 撞墙——map 并发崩溃 → 加 `mu`

LLM 在调工具时读 `tools`, 后台刷新同时在改 `tools`。

Go 的 map **不允许一边读一边写**, 撞上直接:

```
fatal error: concurrent map read and map write
```

程序当场死。

> 解法不是 "想出来" 的, 是 Go 的铁律逼的: **共享 map 必须加锁。**
> 为什么用 `RWMutex` 不用普通 `Mutex`?因为 "读"(LLM 调工具)又多又频繁, "写"(刷新)很少。读写锁(RWMutex)允许多个读同时进, 只在写时才独占。

```go
    mu sync.RWMutex   // ← 被"map 会崩"逼出来的
```

> [!NOTE]
>
> 工具确实是在 MCP server 里定义好的,Manager 一个都没"创造"。
>
> 那 `m.tools` 这张 map 到底在存什么、为什么要写它?一句话先说结论:
>
> > **`m.tools` 不是"定义工具",是 Manager 自己留的一张"通讯录/缓存"。工具的真身在 server 那边,这张 map 存的是"我这边记下来的:哪个工具在哪个 server、原名叫啥"。**
>
> ## 一、东西在 server,但每次都去问 server 太蠢
>
> 工具定义确实在 server。那 Manager 想用的时候,可以每次都现去问 server "你有哪些工具啊"——这叫 `ListTools`,一次网络/进程往返(RPC)。
>
> 问题是:LLM 每跑一步几乎都要看"我现在有哪些工具可用"。如果**每次都现问 server**:
>
> - 慢:每次都要一个 RPC 往返,N 个 server 就 N 次。
> - 浪费:工具列表大部分时间根本没变,问了也是同样的答案。
>
> 所以 Manager **问一次,记在本地**(写进 `m.tools`),之后 LLM 来要,直接从内存这张 map 拿。**这就是"写 tools"的第一个理由:缓存。** 这就是 [tool.go:64](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/tool.go#L64) `loadTools` 干的事——启动时问一遍,记下来。
>
> ## 二、更关键的理由:它存的不只是"工具",是"路由信息"
>
> 这才是 `m.tools` 真正不可省的原因。看 [tool.go:29](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/tool.go#L29) 那个 `toolEntry`:
>
> ```go
> type toolEntry struct {
>     serverName   string   // 这工具属于哪个 server
>     originalName string   // 它在那个 server 里的原名
>     llmTool      llm.Tool // 给 LLM 看的、带前缀的版本
> }
> ```
>
> 为什么需要这个?因为**两个 server 可能有同名工具**。比如:
>
> - `filesystem` server 有个工具叫 `read`
> - `chrome-devtools` server 也有个工具叫 `read`
>
> 如果直接把原名 `read` 给 LLM,LLM 说"调 read",Manager 傻了:**调哪个 server 的 read?**
>
> 所以 Manager 做了一件事:给每个工具**改名加前缀**(见 [tool.go:23](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/tool.go#L23) `keyOf`):
>
> ```
> filesystem 的 read       →  对 LLM 显示为  mcp__filesystem__read
> chrome-devtools 的 read  →  对 LLM 显示为  mcp__chrome-devtools__read
> ```
>
> 现在 LLM 说"调 `mcp__filesystem__read`",Manager 一查 `m.tools` 这张 map,就知道:
>
> > 哦,这个对应 `filesystem` server 的原名 `read` → 去那个 server,用原名 `read` 调。
>
> 这一步"翻译回去"就是 [tool.go:123](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/tool.go#L123) `CallTool` 干的:
>
> ```go
> entry, ok := m.tools[name]        // 用带前缀的名字查到 entry
> ...
> req.Params.Name = entry.originalName   // 翻译回原名,再发给对应 server
> ```
>
> **没有 `m.tools` 这张表,Manager 就不知道 `mcp__filesystem__read` 该路由回谁、原名是啥。** 这是它第二个、也是更硬的存在理由:**路由 / 改名映射。**
>
> ## 三、用一个比喻彻底锁死
>
> 把 MCP server 想成**几家不同的餐馆**,每家有自己的菜单(工具)。
>
> - 菜(工具)是餐馆做的,Manager 不做菜 —— ✅ 你说得对。
> - 但 Manager 是个**外卖平台**。它要干两件事:
>   1. **把各家菜单抄到自己 App 上**(写 `m.tools`),用户一刷就看到,不用每次打电话问餐馆有没有这道菜 —— **缓存**。
>   2. **两家都有"番茄炒蛋",平台就显示成"A 店·番茄炒蛋""B 店·番茄炒蛋"**(加前缀),你点了,平台知道该把单子转给哪家、那家管这道菜叫啥 —— **路由**。
>
> > 菜在餐馆,没错。但"平台上那份带店名的菜单",必须平台自己存一份——这份就是 `m.tools`。
>
> ## 一句话收尾
>
> `m.tools` 不和 server 抢"定义工具"的活。它写的是 Manager 自己必须持有的两样东西:
>
> 1. **一份本地缓存**(免得每次问 server,慢);
> 2. **一张路由表**(带前缀名 → 哪个 server + 原名,否则同名工具没法区分、调用没法转发)。
>
> server 定义"有什么工具",Manager 记录"这些工具我怎么找回去、怎么不重名"——两边干的根本不是一件事。

> [!NOTE]
>
> > "工具不是 server 一开始就定死的吗?既然定死了,那张表存一次不就完了,为什么还要 delete 旧的、写新的?"
>
> 关键就在这句**"定死了"**——它其实**没定死**。这是 MCP 协议里一个很容易被忽略的设定:
>
> ## 一、server 的工具列表是会**变**的,不是刻在石头上的
>
> "工具在 server 里定义好"这句话,容易让人以为它像常量一样一辈子不变。但实际上,很多 server 的工具会**在运行中动态增减**。举几个真实场景:
>
> - **filesystem server**:你授权它访问的目录变了,它能操作的工具集可能跟着变。
> - **chrome-devtools server**:浏览器**没连上**时,只有"连接浏览器"这一个工具;一旦连上,突然多出"截图""点击""读 DOM"等一大堆工具。
> - 一个 server 加载了新插件 / 切换了模式 → 工具集变了。
>
> MCP 协议为此专门规定了一条通知:`tools/list_changed`(还有 resources、prompts 的对应版本)。意思是 server 主动喊一嗓子:
>
> > **"喂,我的工具列表变了,你之前记的那份过时了,重新来拿。"**
>
> 这就是 [refresh.go:39](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/refresh.go#L39) 在监听的东西。
>
> ## 二、收到这一嗓子,Manager 那张缓存就"脏"了
>
> 回到上一轮的比喻:`m.tools` 是**外卖平台抄下来的菜单缓存**。
>
> 餐馆(server)现在换菜单了,还特地打电话通知平台:"我菜单变了!"
>
> 平台手里那份**旧菜单就作废了**。它必须:
>
> 1. 重新跟餐馆要一份最新菜单(`listAllTools`,又一次 RPC);
> 2. 把自己 App 上**那家餐馆的旧菜删掉**(delete 旧的);
> 3. **换上新菜单**(写新的)。
>
> 看代码 [refresh.go:113-122](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/refresh.go#L113-L122),干的就是这三步:
>
> ```go
> tools, err := listAllTools(ctx, s.client)   // 1. 重新问 server 要最新的
> ...
> m.mu.Lock()
> for k, e := range m.tools {
>     if e.serverName == serverName {
>         delete(m.tools, k)        // 2. 删掉【这个 server】的所有旧条目
>     }
> }
> for k, e := range newEntries {
>     m.tools[k] = e                // 3. 灌入最新的
> }
> m.mu.Unlock()
> ```
>
> ## 三、为什么是"先删后写",不是直接覆盖?
>
> 这里有个细节值得你注意——它**只删 `e.serverName == serverName` 的条目**,不是清空整张表。原因:
>
> `m.tools` 里混着**所有 server** 的工具。现在只是 `filesystem` 喊"我变了",那 `chrome-devtools` 的工具不该动。所以要精准地"**只清掉这一家的旧菜,别家不碰**"。
>
> 而且必须"先删再写"而不能只"写覆盖",是因为:**工具可能被删掉了**。比如 server 原来有 `read`、`write` 两个,现在 `write` 被移除了,新列表只剩 `read`。如果只做"写覆盖",那张表里 `write` 这条**永远删不掉**,变成幽灵工具——LLM 还以为能调,一调就报错。先把这家的全删干净、再灌新的,才能保证"删除"也被正确反映。
>
> ## 一句话回答你
>
> > 工具不是"一次定死",而是 server 在运行中可以**增、删、改**的。server 通过 `tools/list_changed` 通知你"我变了",Manager 手里那份缓存就过期了——于是 worker 重新去拿最新列表,**把这个 server 的旧条目全删掉、换上新的**,让本地缓存重新和 server 对齐。
>
> > delete + 写,不是因为工具变了,而是因为**Manager 的缓存需要追上 server 的变化**。变的是 server,Manager 只是在"对账"。

### 第 2 版: 撞墙——后台刷新该在哪跑? → 加 `refreshCh` / `done` / `wg`

server 推来 "工具变了" 的通知。这个通知回调跑在 transport 读循环里, **不能在那儿发 RPC(会死锁)**。所以这活儿必须 **交给别人干**——一个后台 worker goroutine。

一旦你决定 "开个后台 worker", 这三个字段就是 **一整套打包来的、跑不掉**:

```go
    refreshCh chan refreshReq   // ← 把活儿递给 worker 的队列
    done      chan struct{}     // ← 告诉 worker "该下班了,退出"
    wg        sync.WaitGroup    // ← 等 worker 真的退干净,再去关 client
```

注意: 你不是 "想出了 3 个字段", 你是 **reach 了一个标准模式**——"可启动、可干净关闭的后台 worker"。这个模式天生需要这 3 件套。资深的人看到 "我需要个后台 worker", 手就自动去拿这三样, 跟你想到要装东西就拿 map 一样自然。

#### 死锁的产生

> [!NOTE]
>
> > server 推来 "工具变了" 的通知。这个通知回调跑在 transport 读循环里, **不能在那儿发 RPC(会死锁)**。所以这活儿必须 **交给别人干**——一个后台 worker goroutine。没明白为什么会发生死锁？
>
> 这是整个包**最烧脑、也最值钱**的一个点。你卡在这儿很正常,这个死锁不画图基本想不通。我一步步来。
>
> ## 一、先建立一个关键事实:RPC 的"发"和"收"靠同一条线
>
> 你跟 server 之间只有**一条管道**(stdio 的标准输入输出,或一条 HTTP 长连接)。所有消息都从这条管道里进出。
>
> `mcp-go` 库为了管这条管道,内部开了**一个专门的 goroutine,死循环地读这条管道**。我们叫它**读循环(read loop)**。它干的事极其单纯:
>
> ```
> 读循环:  for { 从管道读一条消息 → 看是什么 → 分发出去 }  // 永远转圈
> ```
>
> 管道里回来的**所有**东西,都得经过它的手:
>
> - 你调 `ListTools` 后,server 返回的**响应**——读循环收。
> - server 主动推的**通知**(`tools/list_changed`)——也是读循环收。
>
> **记住这句话:管道里任何回来的消息,都必须由读循环读到、才能被处理。这是唯一的入口。**
>
> ## 二、一次正常的 RPC 长什么样
>
> 你(某个 goroutine)调 `ListTools`,内部其实是这样:
>
> ```
> 你的 goroutine:                  读循环:
>   ① 把请求写进管道                   (在转圈读)
>   ② 卡住等响应  ⟵⟵⟵⟵⟵⟵⟵⟵⟵   ③ 从管道读到响应 → 唤醒你
>   ④ 拿到响应,继续
> ```
>
> **重点在 ②**:你调 `ListTools` 后会**阻塞,卡在那里等**。谁来唤醒你?**③ 读循环**——它从管道读到 server 的响应,然后通知你"响应到了"。
>
> > 你能继续,**前提是读循环还在转、能去读那条响应**。
>
> ## 三、现在制造死锁:在读循环里发 RPC
>
> 通知回调(`OnNotification` 那个函数)是**谁在调它**?——**读循环**。
>
> 读循环读到一条通知,它的处理方式是"调用你注册的回调函数"。也就是说,你的回调代码**是在读循环这个 goroutine 上跑的**。此刻读循环**没在转圈**,它**停在你的回调里**,等你的回调返回,它才能继续读下一条。
>
> 假设你在回调里**直接调了 `ListTools`**,死锁就这么发生:
>
> ```
> 读循环 goroutine:
>   ① 读到一条通知
>   ② 调用你的回调  ← 读循环现在"人在这里",出不去了
>   ③ 你的回调里调 ListTools → 把请求写进管道 → 卡住等响应...
>                                               ↑
>                                     谁去管道里读这个响应?
>                                     本该是读循环。
>                                     但读循环正卡在 ②→③ 里等你的回调返回!
> ```
>
> **僵局成型:**
>
> - 你的回调:在等 `ListTools` 的响应,**不返回**。
> - 读循环:在等你的回调返回,才能去读那个响应,**不去读**。
>
> > 你等响应 → 响应要读循环去取 → 读循环在等你返回 → 你在等响应…… **两边互相等对方先动,谁都不动。永久卡死。** 这就是死锁。
>
> 一句话:**你让读循环去等一个"只有读循环自己才能取到"的东西。它把自己锁死了。**
>
> ## 四、解法:回调里别干活,把活儿"扔出去"
>
> 既然问题是"读循环被你的回调卡住了",那解法就是**让回调瞬间返回**,把真正要发 RPC 的活儿交给**另一个 goroutine**(worker)去干。
>
> 看 [refresh.go:50](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/refresh.go#L50) 回调里实际做的事:
>
> ```go
> select {
> case m.refreshCh <- refreshReq{...}:   // 往 channel 扔个纸条:"该刷新了"
> default:                                 // 扔不进去就算了
> }
> ```
>
> 回调**只是往 channel 里塞一张纸条,然后立刻返回**。不发任何 RPC。读循环马上就自由了,继续转圈读管道——**这样它就能去读后面那个 `ListTools` 响应了**。
>
> 而真正发 `ListTools` 的活儿,由**另一个 goroutine**(worker,见 [refresh.go:60](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/refresh.go#L60))捡起那张纸条去干:
>
> ```
> 读循环 → 读到通知 → 回调塞纸条 → 立刻返回 → 继续转圈 ✅
>                           ↓
>                      refreshCh
>                           ↓
> worker(另一个 goroutine) → 捡到纸条 → 调 ListTools → 卡住等响应
>                                                         ↑
>                                           读循环此刻是空闲的,
>                                           能正常读到响应、唤醒 worker ✅
> ```
>
> 关键差别:现在等响应的是 **worker**,而读循环是**另一个独立的 goroutine,自由地在转圈**。worker 等的响应,读循环能去取。**两个 goroutine,不再自己等自己。死锁解除。**
>
> ## 五、一句话锁死这个理解
>
> > 通知回调是**读循环亲自在跑**的。RPC 的响应**也只能靠读循环去管道里取**。 所以如果你在回调里发 RPC,就等于**让读循环一边卡着等响应、一边又是唯一能取这个响应的人**——它把自己锁死了。 解法:回调里**只扔个信号(塞 channel),秒返回**,让读循环立刻恢复自由;真正的 RPC 交给**另一个 goroutine(worker)**去发——这样"等响应的人"和"取响应的人"就不是同一个了。
>
> 这就是 `refreshCh` + worker 这套结构存在的**唯一理由**:它不是为了好看,是为了**把"发 RPC"从读循环的身上挪走**,否则必死锁。

### 第 3 版: 撞墙——通知外层 + 第二次死锁 → 加 `cbMu` + 三个回调切片

外层(main)想知道 "工具变了我好重新同步", 于是存一串回调: `toolsCBs []func()`。

新问题: 这串回调谁来保护?**能不能也用 `mu`?——不能, 而且这正是最阴的一个坑:**

回调里可能反过来调 `AllLLMTools()`, 那函数要 `mu.RLock()`。如果 worker 正攥着 `mu.Lock()` 去触发回调, 回调又来要 `mu`……**自己锁死自己。**

所以: 回调必须在 **释放 `mu` 之后** 才触发, 而且它需要一把 **自己的、独立的** 锁。

```go
    cbMu         sync.Mutex   // ← 第二把锁,故意和 mu 分开,这样两把锁永不嵌套
    toolsCBs     []func()
    resourcesCBs []func()
    promptsCBs   []func()
```

#### 回调

> [!NOTE]
>
> > 怎么理解提到的“回调”，在代码中具体表现为一个函数吗？
>
> 对,你的理解完全正确:**回调在代码里就是一个函数**。但"回调"这个词强调的不是"它是函数",而是**它被调用的方式**——所以我把这层意思讲透,你以后看到 callback / handler / hook 这些词就都通了。
>
> ## 一、普通调用 vs 回调:区别在"谁来调"
>
> **普通函数**:你自己写、自己调,你清楚它什么时候跑。
>
> ```go
> result := add(1, 2)   // 我亲自调 add,我知道它此刻执行
> ```
>
> **回调函数**:你写好一个函数,**交给别人(通常是库)**,然后**别人在未来某个时刻替你调它**。你不知道具体哪一刻,你只是说"等那件事发生时,请帮我调这个"。
>
> > 回 = 反过来。正常是"你调库";回调是"你把函数给库,**库反过来调你**"。所以叫"回"调。
>
> 一个生活类比:你去餐馆取号,留个手机号说"做好了打这个号"。这个**手机号**就是回调——你不站在后厨盯着,你把"联系方式"交出去,**厨房在菜好的那一刻反过来打给你**。
>
> ## 二、在这个包里,回调具体是哪个函数
>
> 就是 [refresh.go:35](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/refresh.go#L35) 的 `makeNotificationHandler` **返回的那个函数**:
>
> ```go
> func (m *MCPManager) makeNotificationHandler(serverName string) func(mcplib.JSONRPCNotification) {
>     return func(n mcplib.JSONRPCNotification) {   // ← 这一坨,就是"回调"
>         ...
>         select {
>         case m.refreshCh <- refreshReq{...}:
>         default:
>         }
>     }
> }
> ```
>
> 注意它的返回类型 `func(mcplib.JSONRPCNotification)`——**它返回的是一个函数**。这个被返回的函数,就是要交给库的那个回调。
>
> ## 三、关键动作:把这个函数"交出去"
>
> 光写一个函数,它不会自己跑。必须**注册**给库。看 [manager.go:229](vscode-webview://0392vca9q0fquf0bqu0dq22l6n0cvmqklndqgh30kegeejroqdef/internal/mcp/manager.go#L229):
>
> ```go
> s.client.OnNotification(m.makeNotificationHandler(r.name))
> ```
>
> 把这行拆开看,就是"留手机号"那一刻:
>
> ```
> m.makeNotificationHandler(r.name)   ← 先造出那个回调函数(还没跑)
> s.client.OnNotification( ... )      ← 把它交给库,说"以后有通知就调它"
> ```
>
> `OnNotification` 的意思就是 "On Notification(当有通知时)→ 调用我给你的这个函数"。**你只是登记,并没有调用它。** 调用的时机由库决定。
>
> ## 四、谁、在什么时候,真正调了它
>
> 这就接上了我们前面聊的死锁:**库的读循环**,在从管道读到一条通知的那一刻,**替你调用**这个函数:
>
> ```
> 你(启动时):     OnNotification(回调)   → 把函数交给库,登记好,返回
>                         ⋮
>                   (时间过去,你在干别的)
>                         ⋮
> 库的读循环:      从管道读到一条通知       → 找到你登记的回调 → 调用它(把通知作为参数 n 传进去)
> 你的回调:        被库唤醒,执行,塞个 channel,返回
> ```
>
> 所以你写的那个函数,**执行时机和执行所在的 goroutine 都不由你控制**——是库在读循环里替你调的。这正是为什么前面强调"回调里不能发 RPC":因为你不是在自己的 goroutine 里跑,你是**借着读循环的身体在跑**,卡住它就卡住了整条管道。
>
> ## 一句话总结
>
> > "回调"在代码里确实就是一个函数。但它特殊在:**你写好它、用 `OnNotification` 把它交给库登记,自己不调用;将来某个事件发生时,由库反过来替你调用它(还顺便把事件数据当参数传给你)。**
> >
> > 这个包里,回调 = `makeNotificationHandler` 返回的那个 `func(n ...)`;登记动作 = `s.client.OnNotification(...)`;真正触发 = 库的读循环收到通知时。
>
> > 你以后看到 `OnXxx`、`HandleXxx`、`SetXxxCallback`、`AddListener` 这类名字,几乎都是同一个套路:**"把你的函数交给我,事到临头我替你调"。**

### 现在再回头看这个结构体, 它 "会说话" 了

```Go
servers/tools/resources/prompts  →  装什么(你能想到的,数据)
mu                               →  "这堆数据被多个 goroutine 碰"
refreshCh/done/wg                →  "改它的是个后台 worker,要能启动 + 干净停"
cbMu/三个切片                     →  "我要通知外人,且通知不能和数据锁打架"
```

每一组字段都是一句话的答案。**它不是 11 个零散字段, 是 4 个回答 + 你能想到的那部分数据。**

### 真正要带走的那句话

你 "想不到" 这些字段, 是因为你在拿 **写代码的眼光** 看一段 **已经凝固的疤痕**。这些字段是并发问题一刀刀划出来的伤疤, 作者几乎肯定是这么长出来的:

> 先写 4 个 map → 跑起来崩(并发 map)→ 加 `mu`
> → 通知刷新撞死锁 → 加 channel + worker(`refreshCh`/`done`/`wg`)
> → 加回调时又撞第二个死锁 → 加 `cbMu`。

**成品把这段血泪史抹平了, 看起来像天才一笔画成——这正是 AI 生成代码(以及一切 "完成态" 代码)对你撒的谎, 也正是 "我永远想不到" 的根源。**

所以 "长本领" 的方向 **不是** 把想象力练强去凭空变出这些字段。是反过来:

> **去认识那几个反复出现的并发套路**——
> ① 共享状态要加锁
> ② 后台活儿用 channel + worker + 优雅关闭
> ③ 对外回调要在锁外触发
>
> 一共就这么三五个。认识了它们, 下次 **你自己** 撞上 "map 崩了" "回调死锁了", 手就会自动伸向对应的工具。

到那天你不会觉得 "我想到了这个字段", 你只会觉得 "这不废话吗, 这儿当然得加把锁"——**那个'废话'的感觉, 就是本领长出来了的样子。**

## 附: `done chan struct{}` 到底什么意思

这个字段拆开看是两层意思叠在一起。        

### 一、`chan struct{}` 字面意思

- `chan X` = 一个 **通道**, goroutine 之间用它传 `X` 类型的值。
- `struct{}` = **空结构体**, 一个 **不占内存(0 字节)、不携带任何数据** 的类型。

所以 `chan struct{}` = **一个不传任何数据的通道**。

它不传数据要干嘛?——它传的不是 "值", 是 "**事件本身**"。通道被关这个 **动作** 就是信号。这里它就是一个纯粹的 "**关门信号灯**"。

> 为什么不用 `chan bool`?因为 `bool` 会让人误以为 "还要看传过来的是 true 还是 false"。这里根本不关心值, 只关心 "有没有这回事"。Go 社区惯例: **只传信号、不传数据的通道, 用 `chan struct{}`**, 一看就知道 "这是个纯信号"。

### 二、它在这个包里怎么用——配合 `close()` 看

真正的玄机在于它 **从不发送值, 而是被 `close`**。

**worker 那头在 "听" 它**(refresh.go, `startWorker`):

```go
for {
    select {
    case <-m.done:          // ← 如果 done 有动静,就 return,worker 退出
        return
    case req := <-m.refreshCh:
        m.handleRefresh(req)
    }
}
```

**Close 那头 "按下" 它**(manager.go, `Close`):

```go
func (m *MCPManager) Close() error {
    close(m.done)   // ← 关闭通道
    m.wg.Wait()     // ← 等 worker 真的退出
    ...
}
```

### 三、关键机制: 为什么 `close` 能当 "广播信号"

记住 Go 这个特性:

> **一个被 `close` 掉的通道, 所有对它的接收(`<-m.done`)会立刻返回, 不再阻塞。**

平时 worker 卡在 `select` 上等活儿。一旦 `Close()` 执行 `close(m.done)`, 那个 `case <-m.done:` 瞬间就能 "收到"(收到的是零值, 但没人 care), 于是 worker `return`, goroutine 干净退出。

而且 `close` 是 **广播**——哪怕有 10 个 worker 同时 `<-m.done`, 一次 `close` 就能让它们 **全部** 收到、全部退出。这就是为什么关门信号用 "close 通道" 而不是 "发一个值": 发值只能叫醒一个, close 能叫醒所有。

### 四、一句话总结

```go
done chan struct{}
```

**一个一次性的、不带数据的 "停工广播开关"**。

平时没人动它, worker 安静地在 `select` 里等活; `Close()` 时 `close(它)`, 所有在听它的 goroutine 立刻收到 "下班" 信号、退出。

`struct{}` 表明 "我只发信号、零数据"; `close`(而非发值)表明 "这是一次性的、对所有人广播的终止"。

> 三件套闭环: `done` 负责 **喊** "下班" → worker 收到后退出 → `wg.Wait()` 负责 **确认** "人真的走光了" → 然后才敢去 `client.Close()`。