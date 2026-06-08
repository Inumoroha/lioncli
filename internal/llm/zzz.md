## OpenAI

### OpenAI的带工具的第一次请求格式长什么样(携带工具定义)？

[官网](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create) ` POST https://api.openai.com/v1/chat/completions`

在这个请求中，你在 `tools` 字段中定义了 AI 可以使用的工具（比如一个查询天气的函数 `get_weather`）。

```json
{
    "model": "gpt-5.4",
    "messages": [
        {
            "role": "developer",
            "content": "You are a helpful assistant."
        },
        {
            "role": "user",
            "content": [
                {
                    "type": "text",
                    "text": "What is in this image?"
                },
                {
                    "type": "image_url",
                    "image_url": {
                        "url": "https://xxx.jpg"
                    }
                }
            ]
        }
    ],
    "temperature": 0.7,
    "max_tokens": 300,
    "stream": false,
    "tools": [
        {
            "type": "function",
            "function": {
                "name": "get_current_weather",
                "description": "Get the current weather in a given location",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "location": {
                            "type": "string",
                            "description": "The city and state, e.g. San Francisco, CA"
                        },
                        "unit": {
                            "type": "string",
                            "enum": ["celsius", "fahrenheit"],
                            "description": "the unit of temperature."
                        }
                    },
                    "required": ["location"]
                }
            }
        }
    ],
    "tool_choice": "auto",
    "logprobs": true,
    "top_logprobs": 2,
    "response_format": { "type": "text" },
    "reasoning_effort": "medium"
}
```

**`tools`**：工具列表。每个工具的 `type` 目前必须是 `"function"`。在 `function` 内部，你需要定义 `name`（函数名）、`description`（让 AI 明白什么时候该用它）以及 `parameters`（遵循 JSON Schema 规范的参数定义）。

**`tool_choice`**：控制 AI 如何使用工具。`"auto"` 表示让 AI 自己决定用不用；如果是 `"none"` 则强制不用；如果指定 `{"type": "function", "function": {"name": "get_weather"}}` 则强迫 AI 必须调用这个函数。

### OpenAI的带工具的第一次响应格式长什么样(AI 决定调用工具)？

AI 阅读了用户的请求，发现需要查询北京和上海的天气，于是它生成了**两个工具调用**指令，并暂停文本输出。

```json
{
    "id": "chatcmpl-9QMxB8...",
    "object": "chat.completion",
    "created": 1716301234,
    "model": "gpt-4o-2024-08-06",
    "system_fingerprint": "fp_a24b4d720c",
    "choices": [
        {
            "index": 0,
            "message": {
                "role": "assistant",
                "content": null,
                "refusal": null,
                "tool_calls": [
                    {
                        "id": "call_bj001",
                        "type": "function",
                        "function": {
                            "name": "get_weather",
                            "arguments": "{\"city\": \"北京\"}"
                        }
                    },
                    {
                        "id": "call_sh002",
                        "type": "function",
                        "function": {
                            "name": "get_weather",
                            "arguments": "{\"city\": \"上海\"}"
                        }
                    }
                ]
            },
            "logprobs": null,
            "finish_reason": "tool_calls"
        }
    ],
    "usage": {
        "prompt_tokens": 25,
        "completion_tokens": 150,
        "total_tokens": 175,
        "completion_tokens_details": {
            "reasoning_tokens": 0,
            "accepted_prediction_tokens": 0,
            "rejected_prediction_tokens": 0
        }
    }
}
```

**`content` 为 `null`**：此时 AI 没有对用户说话，而是转去调用工具。

**`tool_calls`**：包含 AI 想要调用的函数列表。

- **`id`**：每个工具调用都有一个唯一的 ID（如 `call_bj001`），后续返回结果时必须对齐这个 ID。
- **`arguments`**：AI 生成的参数字符串。**注意：它是一个经过转义的 JSON 字符串**，你在代码中需要用 `JSON.parse()` 解析它。

**`finish_reason` 为 `"tool_calls"`**：明确告诉客户端，生成中断是因为要等待工具执行结果。

### OpenAI的带工具的第二次请求格式长什么样(提交工具执行结果)？

你在自己的代码里运行了真正的 `get_weather` 函数，拿到了结果。现在，你需要把 **之前的对话历史**、**AI 刚刚返回的 tool_calls**、以及 **你的工具运行结果** 一起打包发回给 OpenAI。

```json
{
    "model": "gpt-4o",
    "messages": [
        {
            "role": "developer",
            "content": "你是一个有用的助手。"
        },
        {
            "role": "user",
            "content": "帮我看看北京和上海现在的天气怎么样？"
        },
        // 必须把步骤2中 AI 返回的 message 原封不动传回去
        {
            "role": "assistant",
            "tool_calls": [
                {
                    "id": "call_bj001",
                    "type": "function",
                    "function": {
                        "name": "get_weather",
                        "arguments": "{\"city\": \"北京\"}"
                    }
                },
                {
                    "id": "call_sh002",
                    "type": "function",
                    "function": {
                        "name": "get_weather",
                        "arguments": "{\"city\": \"上海\"}"
                    }
                }
            ]
        },
        // 提交北京工具的运行结果
        {
            "role": "tool",
            "tool_call_id": "call_bj001",
            "content": "{\"status\": \"晴天\", \"temperature\": \"25°C\"}"
        },
        // 提交上海工具的运行结果
        {
            "role": "tool",
            "tool_call_id": "call_sh002",
            "content": "{\"status\": \"小雨\", \"temperature\": \"20°C\"}"
        }
    ]
}
```

- **`role` 为 `"tool"`**：这是一个专门用于提交工具结果的角色。
- **`tool_call_id`**：必须与步骤 2 中 AI 分配的 ID 严格一致，否则 AI 不知道这个结果对应哪次调用。
- **`content`**：工具返回的真实数据，通常建议传 JSON 字符串。

### OpenAI的带工具的第二次响应格式长什么样(AI 组织语言回答用户)？

OpenAI 拿到了工具返回的天气数据，终于可以合并这些信息，给用户一个最终的文本回答。

```json
{
    "id": "chatcmpl-FINAL789",
    "object": "chat.completion",
    "created": 1716301240,
    "model": "gpt-4o-2024-08-06",
    "choices": [
        {
            "index": 0,
            "message": {
                "role": "assistant",
                "content": "现在北京和上海的天气情况如下：\n- **北京**：目前是晴天，气温约为 25°C，非常舒适。\n- **上海**：目前有小雨，气温约为 20°C，出门记得带伞。"
            },
            "logprobs": null,
            "finish_reason": "stop"
        }
    ],
    "usage": {
        "prompt_tokens": 210,
        "completion_tokens": 48,
        "total_tokens": 258
    }
}
```

**`finish_reason` 为 `"stop"`**：本次复杂的工具调用交互完美结束。

## Anthropic

### Anthropic的带工具的第一次请求格式长什么样(携带工具定义)？

注意这里 `system` 的位置，以及 `tools` 里面的 `input_schema`（OpenAI 叫 `parameters`）。

```json
{
    "model": "claude-3-5-sonnet-20241022",
    "system": "你是一个资深的前端 UI 设计师助手。",
    "max_tokens": 1024,
    "messages": [
        {
            "role": "user",
            "content": [
                {
                    "type": "image",
                    "source": {
                        "type": "base64",
                        "media_type": "image/jpeg", // 必须明确指定格式，如 image/png, image/webp
                        "data": "/9j/4AAQSkZJRgABAQEASABIAAD... (这里是超级长的Base64字符串)"
                    }
                },
                {
                    "type": "text",
                    "text": "这是我手画的登录页面草图，请你分析里面的元素，并帮我调用工具生成一张高保真的 UI 效果图。"
                }
            ]
        }
    ],
    "tools": [
        {
            "name": "generate_ui_design",
            "description": "根据传入的画面描述，生成一张高质量的 UI 设计图，并返回图片的 URL",
            "input_schema": {
                "type": "object",
                "properties": {
                    "image_prompt": {
                        "type": "string",
                        "description": "用于生成图片的英文提示词（Prompt）"
                    }
                },
                "required": ["image_prompt"]
            }
        }
    ],
    "tool_choice": { "type": "auto" }
}
```

**`model`** (必填)：模型名称，如 `"claude-3-5-sonnet-20241022"`、`"claude-3-opus-20240229"`。

**`system`** (可选，**注意位置！**)：在 Anthropic 中，系统提示词**绝对不能**放在 `messages` 数组里！它是一个**与 `messages` 平级的顶级参数**。

**`messages`** (必填)：对话历史数组。

- **`role`**：**只能是 `user` 或 `assistant`。** Anthropic 非常严格，它要求对话必须是 `user` 和 `assistant` **严格交替出现**（也就是不能有两个连续的 `user`，如果有，你必须把内容合并成一个）。
- **`content`**：可以是纯字符串，也可以是数组（用于图文多模态，例如 `[{"type": "text", "text": "..."}, {"type": "image", "source": {...}}]`）。

**`max_tokens`** (**必填！**)：OpenAI 里这个是选填的，但 **Anthropic 强制要求你必须填**，用来限制模型输出的最大长度。Claude 3.5 Sonnet 的最大支持输出是 8192。

**`temperature`** (可选)：控制随机性，范围是 `0.0` 到 `1.0`（注意 OpenAI 是到 2.0）。

### Anthropic的带工具的第一次响应格式长什么样(决定调用工具)？

Claude 收到了请求，决定调用 `generate_ui_design` 工具。Anthropic 的响应结构设计得更加“块状化”，它把回复内容当作一个包含不同类型区块（Text Block / Tool Use Block）的数组。

```json
{
    "id": "msg_01XFDxyz...",
    "type": "message",
    "role": "assistant",
    "model": "claude-3-5-sonnet-20241022",
    "content": [
        {
            "type": "text",
            "text": "好的，我已经看过了你手绘的登录页草图，里面包含了一个居中的登录框。我现在来为你生成高保真效果图。"
        },
        {
            "type": "tool_use",
            "id": "toolu_01A09q90qw90llq1324",
            "name": "generate_ui_design",
            "input": {
                "image_prompt": "A modern login page UI design, minimal style..."
            }
        }
    ],
    "stop_reason": "tool_use",
    "stop_sequence": null,
    "usage": {
        "input_tokens": 1250, // 包含了图片的 Token
        "output_tokens": 85
    }
}
```

**注意 `input` 字段**：这里直接是一个标准的 JSON Object `{ "image_prompt": "..." }`，不是字符串，你在代码里直接 `input.image_prompt` 就能拿到值，比 OpenAI 舒服。

**`id`**：本次消息的唯一 ID。

**`role`**：永远是 `"assistant"`。

**`content`**：这是一个**数组**，而不是像 OpenAI 那样的纯字符串。这是因为如果触发了工具调用，这里会同时包含纯文本解释 `{"type": "text"}` 和工具调用指令 `{"type": "tool_use"}`。你在代码中提取文本时，通常需要读取 `content[0].text`。

**`stop_reason`**：停止原因。常见的有：

- `"end_turn"`：自然回答完毕（相当于 OpenAI 的 `stop`）。
- `"max_tokens"`：达到了你设置的字数上限被截断。
- `"tool_use"`：模型需要调用工具。

**`usage`**：Token 消耗统计。

- Anthropic 近期推出了**提示词缓存 (Prompt Caching)** 功能，如果你使用了该功能，`usage` 里还会多出 `cache_creation_input_tokens` 和 `cache_read_input_tokens` 这两个计费字段。

### Anthropic的带工具的第二次请求格式长什么样(提交工具执行结果)？

开发者在后台画图并把 URL 传给 Claude这是与 OpenAI 差别最大的一步。你需要用 `role: "user"` 发送一个 `type: "tool_result"` 的区块给 Claude。

```json
{
    "model": "claude-3-5-sonnet-20241022",
    "system": "你是一个资深的前端 UI 设计师助手。",
    "max_tokens": 1024,
    "messages": [
        // 1. 初始的 User 消息 (图片Base64+文本)
        {
            "role": "user",
            "content": [
                {
                    "type": "image",
                    "source": {
                        "type": "base64",
                        "media_type": "image/jpeg",
                        "data": "..."
                    }
                },
                { "type": "text", "text": "这是我手画的..." }
            ]
        },
        // 2. 必须把步骤2中 Claude 的完整回复原封不动拼进来 (包含 text 和 tool_use)
        {
            "role": "assistant",
            "content": [
                { "type": "text", "text": "好的，我已经看过了..." },
                {
                    "type": "tool_use",
                    "id": "toolu_01A09q90qw90llq1324",
                    "name": "generate_ui_design",
                    "input": {
                        "image_prompt": "A modern login page UI design, minimal style..."
                    }
                }
            ]
        },
        // 3. 以 User 身份，提交工具的执行结果
        {
            "role": "user",
            "content": [
                {
                    "type": "tool_result",
                    "tool_use_id": "toolu_01A09q90qw90llq1324", // 必须对应上面的 ID
                    "content": "{\"status\": \"success\", \"result_image_url\": \"https://example.com/final-design.png\"}"
                }
            ]
        }
    ]
}
```

### Anthropic的带工具的第二次请求格式长什么样(最终响应)？

Claude 组织 Markdown 图片发给你，Claude 收到 `tool_result` 后，顺着往下生成最终的文案。

```json
{
    "id": "msg_02YGFabc...",
    "type": "message",
    "role": "assistant",
    "model": "claude-3-5-sonnet-20241022",
    "content": [
        {
            "type": "text",
            "text": "设计图生成完毕！请看最终的 UI 效果：\n\n![高保真登录页UI设计](https://example.com/final-design.png)\n\n这版设计采用了极简风格，将输入框和按钮做了圆角处理以提升现代感。你觉得效果如何？"
        }
    ],
    "stop_reason": "end_turn",
    "usage": {
        "input_tokens": 1400,
        "output_tokens": 60
    }
}
```

### 💡 核心差异速记口诀

| 特性                      | OpenAI                                             | Anthropic                                                |
| ------------------------- | -------------------------------------------------- | -------------------------------------------------------- |
| 系统设定人设              | 放在 `messages` 里，角色叫 `developer` 或 `system` | 作为顶级参数 `system`，与 `messages` 平级                |
| 角色限制                  | 相对宽松，可以连发多个 `user`                      | **极度严格**，`messages` 只能 `user` 和 `assistant` 交替 |
| 最大输出长度              | `max_completion_tokens` (选填)                     | `max_tokens` (**必填**)                                  |
| 响应内容读取              | `choices[0].message.content` (直接是字符串)        | `content[0].text` (`content` 本身是个数组)               |
| 工具定义 (Function/Tools) | **Tools)**使用标准的 JSON Schema (`parameters`)    | 使用稍作修改的 JSON Schema (`input_schema`)              |

如果你要写一套代码兼容 OpenAI 和 Anthropic 两种 API，记住这句口诀： **“O 家传链接，A 家传编码；O 家参数是字符，A 家参数是对象；O 家交差用 Tool 角色，A 家交差用 User 伪装。”**

## 如何抽象两家？

这其实不是“天赋”问题，而是信息差问题。这层抽象不是凭空想出来的，是“两边 API都摸熟之后，被倒逼出来的”。我拆给你看它是怎么长出来的，你会发现每一步都不神秘。

1.抽象的前提是“你得先见过两边”

这层抽象的设计者（不管是 AI还是人）脑子里同时压着两份格式，于是会自然冒出一个问题：

**这两份消息结构，能不能用一种内部表示同时装下？**

这个问题你想不到，不是因为你不够聪明，而是因为你当时只看了一份文档。只要你把 Anthropic 的 messages文档也读一遍，对比着看，这个念头几乎是必然会冒出来的。抽象能力 ≈ 见过的差异数量。

2.真正的“设计决策”只有一个，而且有客观答案

核心决策只有一个：

**内部统一表示，到底学 OpenAI 还是学 Anthropic？**

- OpenAI：content 是一个字符串（多模态时才退化成数组）。
- Anthropic：content 永远是块的数组（[]ContentBlock）。

选谁？判断标准不是“哪个好看”，而是一个工程上的硬道理：

**选表达力更强的那个当内部标准（superset 原则）**。

块数组能无损表示字符串（一个文本块而已），反过来字符串装不下“文本+图片+工具调用”混在一条消息里。所以内部必须选 Anthropic 的块模型。

```type
  type Message struct {
         Role    Role
         Content []ContentBlock  // ← 选了数组，不是 string
   }
```

这一步不需要灵感，只需要知道“superset 原则”这条规则。 这种规则是可以学的、可以记住的，下次遇到任何“统一两个格式”的问题，你都先问：谁是谁的超集？

3.剩下的全是“体力活”，不是“创造力”

定了内部用块数组之后，后面每一块代码都是机械推导出来的，没有任何灵光：

内部有 image 块 → OpenAI 那边怎么发？翻文档 → 它要 image_url 的 data URI → 写一段转换。

内部有 tool_result 块 → Anthropic 没有 tool role → 塞进 user 消息里 → 写一段转换。

看出规律没？抽象层（types.go）+ 每个 provider 一个 convert.go，这是一个固定套路，不是每次都要重新发明。这个套路有个名字，叫 Adapter /
防腐层（Anti-Corruption Layer）。你只要认识这个模式一次，以后所有“对接多个外部 API”的场景，你都会条件反射地搭出这个结构。

4.那个让你最惊艳的“多模态”，其实是最不需要创造力的部分

你提到“多模态能力我根本想不到”。但你回头看 ContentBlock）：

```go
 type ContentBlock struct {
         Type       ContentType
         Text       string
         ToolUse    *ToolUseBlock
         ToolResult *ToolResultBlock
         Image      *ImageBlock
  }
```

它“支持多模态”，仅仅是因为它照抄了 Anthropic 的块模型。Anthropic 的 content block 本来就能放
text/image/tool_use，设计者没有“发明”多模态，他只是选对了内部标准，多模态是白送的。这又回到第 2 点：选超集，能力自动到手。

5.给你的可操作结论

你缺的不是脑子，是这三样可以直接补的东西：

1. 见识：同一类东西，逼自己看 ≥2 份实现再动手。差异会自己教你抽象。
2. 几条死规则：比如“统一格式时选超集”“对接外部 API 用防腐层”。这些是套路，记住就能用，不靠天赋。
3. 拆解习惯：把“一个吓人的大设计”拆成“1 个真决策 + N 段体力翻译”。这份代码真正的决策只有一个（选块模型），其余几百行都是体力活。

下次你想验证自己有没有学到——别看 AI 的成品，找 OpenAI + 一个别的 provider（比如 Gemini）的文档，自己试着画一遍内部 Message该长啥样。你大概率能画出和这个八九不离十的东西。能想到，只是因为这次你会看两份文档了。

## **架构/思想(必须吃透,而且很小)**

整个 provider 层就一个模式——翻译层 (adapter):

统一内部类型 (internal/llm) ←→ convert.go 双向翻译 ←→ provider 的线上格式 (types.go)
↑
client.go 只管 HTTP 收发

每个 provider 都是这三个文件,职责切得很干净:

- client.go — 纯管道:拼 HTTP 请求、设 header、读 body、处理错误码
- types.go — provider 的 JSON 线上结构(字段名长啥样)
- convert.go — 核心:toAPI 出去、fromAPIResponse 回来

    这个层次你应该能默写出来,因为它就是整个设计的灵魂。而且 OpenAI 和 Anthropic
    放一起对比,差异点全暴露了,正好是最好的学习材料:

| 差异         | Anthropic        | OpenAI                            |
| ------------ | ---------------- | --------------------------------- |
| system 消息  | 顶层独立字段     | 塞进 messages 数组                |
| tool 结果    | 当成 user 消息发 | 独立的 tool role,每个 result 一条 |
| 图片         | source.base64    | image_url 的 data URI             |
| 工具调用参数 | 直接是对象       | 是 JSON 字符串,要再 parse 一次    |

这张表就是这层代码 80% 的价值。看懂它,你就懂了"为什么需要统一抽象"——因为底下两家长得完全不一样。

## 细节(不用记,要用再查)

anthropic-version: 2023-06-01、finish_reason 有哪几个枚举值、data URI的拼法……这些是参考资料,不是知识。记不住天经地义,官方文档随时能查。你"迷失在细节"的恐惧,本质是想把 B 当 A 来背——没必要。

## 给你一个具体的读法

不要从头往下读。按这个顺序,30 分钟够了:

1. 先读 ` internal/llm/types.go`(你已经熟了)——这是"中间语言"。
2. 读 一个 `convert.go` 的 `toAPI` 函数,边读边问:"这个内部字段为什么要变成那个样子?" 看不懂的地方对照上面那张表。
3. 读对应的 `client.go`——你会发现它无聊得很(就是 HTTP 八股),这是好事,说明复杂度都被关在 convert 里了。
4. 然后读另一个 `provider` 的 `convert.go`,只看它和第一个哪里不一样。差异点才是信息。
5. `types.go` 永远最后看,而且是当字典查,不通读。

读完你不需要能"从零手写",你需要的是:给你一段 `convert` 代码,你能判断它对不对。这种判断力,比"能默写"实用得多,也是你真正缺的底气。
