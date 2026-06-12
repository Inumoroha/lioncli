---
name: commit-message
description: 指导 agent 如何把一段代码改动总结成规范的 Git 提交信息(Conventional Commits 风格)。当用户要求"写 commit"、"生成提交信息"、"帮我 commit"或需要把 diff 概括成提交说明时调用本技能获取写法规范。
---

# Git 提交信息撰写指引（commit-message）

把一段改动总结成一条清晰、可检索的提交信息。优先看实际 diff，不要只凭文件名臆测改了什么。

## 格式

采用 Conventional Commits：

```
<type>(<scope>): <subject>

<body>
```

- **type**(必填,小写):`feat` 新功能 / `fix` 修缺陷 / `refactor` 重构 / `docs` 文档 /
  `test` 测试 / `chore` 杂务(依赖、构建) / `perf` 性能。
- **scope**(可选):受影响的模块,如 `config`、`mcp`、`tui`。拿不准就省略。
- **subject**:祈使句、不超过 50 字、句末不加句号。写"做了什么",不写"做了一些修改"。
- **body**(可选):空一行后写。解释**为什么**这么改,而不是复述代码本身。

## 标准流程

1. **先看改动**:有 diff 就读 diff,判断这是一次「增/修/重构/杂务」中的哪一类,定 type。
2. **一次只说一件事**:若改动横跨多个不相关主题,提醒用户拆成多个 commit,而不是硬塞进一条。
3. **subject 抓主干**:用一句话概括最核心的变化,细节留给 body。
4. **body 讲动机**:涉及取舍、修了什么 bug、为什么换方案时才写;琐碎改动可不写 body。

## 示例

```
feat(config): 用环境变量替换硬编码的 API key

key/baseURL/model 改为从 .env 读取,避免密钥写死在源码里被提交。
```

```
fix(mcp): server 连不上时降级而非阻断启动
```

## 注意

- 不要把"修改了若干文件""更新代码"这类零信息内容当 subject。
- 中英文都可,但同一条信息内保持一致风格。
- 不确定 type 时,改了行为用 `fix`/`feat`,只动结构不改行为用 `refactor`。
