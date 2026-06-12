MCP 的 stdio 模式下，client 把 MCP server 当成一个子进程拉起来。

## 三大管道

- `stdout`: 协议通道，跑 JSON-RPC,client 和 server 在这上面对话。
- `stdin`: client 往 server 发请求。
- `stderr`: server 的「日志/诊断输出」,不属于协议。

>  `stderr`有什么潜在问题？
>

操作系统的管道(pipe)有固定缓冲区,Linux 上通常 64KB。子进程往 `stderr` 写,如果没人读,缓冲区写满后,子进程的 `write()` 系统调用会阻塞。一个 MCP server 如果在启动时打印了一堆 warning、或者卡在某个 print 上不动了,你的整个 MCP 连接就莫名其妙挂住了,而且你完全看不出原因。这是一类非常隐蔽的 bug。

> `stderr` 能提供什么信息？

MCP server 启动失败时——比如 npx 找不到包、缺依赖——错误信息几乎都是打到 `stderr` 的,而不是走 JSON-RPC。如果你不接 `stderr`,你这边只会看到「连接超时」或「server 没响应」,完全不知道真实原因。接上之后,你能直接在自己的 log 里看到 `ModuleNotFoundError: No module named ...` 这种关键线索。 

> [!TIP]
>
> - 只要你用了管道连子进程,就该条件反射地想:三根管子(`stdin`/`stdout`/`stderr`)我是不是都处理了? 漏掉任何一根都可能出问题。这是 Unix 进程模型的通用知识,不是 MCP 特有的。任何 `exec.Command` + `pipe` 的场景都适用——比如你调 ffmpeg、git、任何外部命令,「不读会阻塞」都成立。
> 2. 「不读的管道会阻塞写端」是经典坑。 同样的道理你在 Go 里也会遇到:`cmd.StdoutPipe()` 的文档明确警告过,如果不读完就 `Wait()` 会死锁。这个知识迁移过来,你就知道 `stderr` 也一样。
> 3. AI 写出这个,正是因为它「见过」大量这类代码和踩坑帖。 你作为新手不知道很正常——这恰恰是 AI 在这里帮你补上经验的地方。你该做的不是记住这一个具体片段,而是把上面那条「<span style="background:#FFB266;">子进程的每根管子都要管</span>」的原则记下来。