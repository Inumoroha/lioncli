package image

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"

	"lioncli/internal/llm"
)

// imageRefRe 同时识别 @image:<path> / @image:path 和 @clipboard。
//
// @image 命中时 group(1) 为路径(可被 <> 包裹);裸路径用负字符类排除 CJK 符号/全角标点
// (U+3000–U+303F、U+FF00–U+FFEF)和通用标点(U+2010–U+206F),这样
// "@image:./shot.png。这是什么" 不会把全角句号吞进路径。
//
// Go 的 RE2 不支持 lookahead,所以 @clipboard 的 word boundary 不写进正则,改在
// acceptedMatches 里手动 peek 下一个 rune(等价 Java 的 (?![\p{L}\p{N}_]))。
var imageRefRe = regexp.MustCompile(
	`@image:(<[^>]+>|[^\s<>\x{2010}-\x{206F}\x{3000}-\x{303F}\x{FF00}-\x{FFEF}]+)|@clipboard`)

// trailingSpaceRe 用于 stripRefs 后清理行尾空白(对应 Java 的 "[ \\t]+\\n" → "\n")。
var trailingSpaceRe = regexp.MustCompile(`[ \t]+\n`)

const clipboardToken = "@clipboard"

// attachmentInstruction 是随图片附件附带的行为约束(直接移植 Java 版文案)。
const attachmentInstruction = "[图片已作为图片附件附加。请直接观察本轮图片内容；除非用户明确要求结合历史，历史对话、历史工具结果、网页/仓库信息都只能作为背景，不能替代当前图片内容；如果当前图片与历史上下文冲突，以当前图片为准。不要调用 MCP、文件系统或浏览器工具重新读取 Image source；如果你无法直接观察附件，请明确说明无法看图，不要根据路径或历史上下文猜测。]"

// refMatch 是一次被接受的引用:文本里的跨度 [start,end) 与解析出的值(路径或 @clipboard)。
type refMatch struct {
	start int
	end   int
	value string
}

// UserMessage 解析输入里的图片引用,构造(可能多模态的)user 消息。
// 无引用时返回纯文本消息,行为与未接入图片时完全一致。
func UserMessage(input, baseDir string) llm.Message {
	matches := acceptedMatches(input)
	if len(matches) == 0 {
		return textUserMessage(input)
	}

	text := stripRefs(input, matches)
	if strings.TrimSpace(text) == "" {
		text = "请分析以下图片。"
	}

	var notes []string
	var imageParts []llm.ContentBlock
	for _, mt := range matches {
		payload := loadImage(mt.value, baseDir)
		label := displayLabel(mt.value, payload)
		if !payload.ok {
			notes = append(notes, fmt.Sprintf("[图片引用无效: %s，原因: %s]", label, payload.err))
			continue
		}
		imageParts = append(imageParts, ToContentPart(payload.image))
		if md := CreateMetadataText(payload.image); md != "" {
			notes = append(notes, md)
		}
	}

	// 全部加载失败 → 退化为纯文本 + notes(不带附件指令),与 Java 的无图分支一致。
	if len(imageParts) == 0 {
		body := text
		if len(notes) > 0 {
			body += "\n\n" + strings.Join(notes, "\n")
		}
		return textUserMessage(body)
	}

	var sb strings.Builder
	sb.WriteString(text)
	sb.WriteString("\n\n")
	sb.WriteString(attachmentInstruction)
	if len(notes) > 0 {
		sb.WriteString("\n")
		sb.WriteString(strings.Join(notes, "\n"))
	}

	parts := make([]llm.ContentBlock, 0, 1+len(imageParts))
	parts = append(parts, llm.ContentBlock{Type: llm.ContentTypeText, Text: sb.String()})
	parts = append(parts, imageParts...)
	return llm.Message{Role: llm.RoleUser, Content: parts}
}

func textUserMessage(text string) llm.Message {
	return llm.Message{
		Role:    llm.RoleUser,
		Content: []llm.ContentBlock{{Type: llm.ContentTypeText, Text: text}},
	}
}

// acceptedMatches 跑正则并对 @clipboard 做手动 word-boundary 过滤。
func acceptedMatches(input string) []refMatch {
	locs := imageRefRe.FindAllStringSubmatchIndex(input, -1)
	var out []refMatch
	for _, m := range locs {
		if m[2] >= 0 {
			// @image 分支:group(1) 命中
			raw := input[m[2]:m[3]]
			if strings.HasPrefix(raw, "<") && strings.HasSuffix(raw, ">") {
				raw = raw[1 : len(raw)-1]
			}
			out = append(out, refMatch{start: m[0], end: m[1], value: raw})
			continue
		}
		// @clipboard 分支:后面紧跟字母/数字/下划线则不算命中(如 @clipboardfoo)。
		if m[1] < len(input) {
			r, _ := utf8.DecodeRuneInString(input[m[1]:])
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				continue
			}
		}
		out = append(out, refMatch{start: m[0], end: m[1], value: clipboardToken})
	}
	return out
}

// stripRefs 删除被接受的引用跨度,再清理行尾空白并 trim。
func stripRefs(input string, matches []refMatch) string {
	var b strings.Builder
	prev := 0
	for _, mt := range matches {
		b.WriteString(input[prev:mt.start])
		prev = mt.end
	}
	b.WriteString(input[prev:])
	return strings.TrimSpace(trailingSpaceRe.ReplaceAllString(b.String(), "\n"))
}

// imagePayload 是单个引用的加载结果。
type imagePayload struct {
	ok    bool
	image *ProcessedImage
	err   string
}

func loadImage(rawPath, baseDir string) imagePayload {
	var resolved string
	if rawPath == clipboardToken {
		grab := Grab()
		if !grab.OK {
			return imagePayload{err: grab.Error}
		}
		resolved = grab.Path
	}

	path := resolved
	if path == "" {
		path = resolvePath(rawPath, baseDir)
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return imagePayload{err: "文件不存在"}
		}
		return imagePayload{err: err.Error()}
	}
	if !info.Mode().IsRegular() {
		return imagePayload{err: "不是普通文件"}
	}
	mimeType := detectMimeType(path)
	if !strings.HasPrefix(mimeType, "image/") {
		return imagePayload{err: "不是受支持的图片 MIME 类型: " + mimeType}
	}
	processed, err := FromPath(path, mimeType)
	if err != nil {
		return imagePayload{err: err.Error()}
	}
	return imagePayload{ok: true, image: processed}
}

func displayLabel(value string, payload imagePayload) string {
	if value == clipboardToken {
		if payload.ok && payload.image != nil && payload.image.SourcePath != "" {
			return "剪贴板 (" + filepath.Base(payload.image.SourcePath) + ")"
		}
		return "剪贴板"
	}
	return value
}

func resolvePath(rawPath, baseDir string) string {
	value := strings.TrimSpace(rawPath)
	if strings.HasPrefix(value, "file://") {
		return filepath.Clean(fileURIToLocalPath(value))
	}
	if !filepath.IsAbs(value) {
		root := baseDir
		if root == "" {
			root, _ = os.Getwd()
		}
		value = filepath.Join(root, value)
	}
	return filepath.Clean(value)
}

// fileURIToLocalPath 宽容地把 file:// URI 转成本地路径:合法 %XX 解码,其余原样保留。
// URI.parse 那种对未编码空格/中文抛错的严格行为不适合这里(Finder/IDE 常给裸 file://)。
func fileURIToLocalPath(value string) string {
	afterScheme := value[len("file://"):]
	var pathPart string
	if strings.HasPrefix(afterScheme, "/") {
		pathPart = afterScheme
	} else {
		// file://host/path → 去掉 host,从第一个 / 起算
		if idx := strings.IndexByte(afterScheme, '/'); idx >= 0 {
			pathPart = afterScheme[idx:]
		} else {
			pathPart = "/" + afterScheme
		}
	}
	decoded := percentDecodeUTF8(pathPart)
	// Windows: file:///C:/path → "/C:/path",去掉盘符前的前导斜杠。
	if runtime.GOOS == "windows" && len(decoded) >= 3 && decoded[0] == '/' && decoded[2] == ':' {
		decoded = decoded[1:]
	}
	return decoded
}

func percentDecodeUTF8(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c == '%' && i+2 < len(s) {
			hi := hexVal(s[i+1])
			lo := hexVal(s[i+2])
			if hi >= 0 && lo >= 0 {
				b.WriteByte(byte(hi<<4 | lo))
				i += 3
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// detectMimeType 优先用内容嗅探(近似 Java 的 Files.probeContentType),再退扩展名。
func detectMimeType(path string) string {
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		buf := make([]byte, 512)
		if n, _ := f.Read(buf); n > 0 {
			ct := http.DetectContentType(buf[:n])
			if i := strings.IndexByte(ct, ';'); i >= 0 {
				ct = ct[:i]
			}
			ct = strings.TrimSpace(ct)
			if ct != "" && ct != "application/octet-stream" {
				return ct
			}
		}
	}
	name := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	case strings.HasSuffix(name, ".gif"):
		return "image/gif"
	case strings.HasSuffix(name, ".webp"):
		return "image/webp"
	case strings.HasSuffix(name, ".bmp"):
		return "image/bmp"
	}
	return "application/octet-stream"
}
