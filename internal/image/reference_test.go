package image

import (
	"runtime"
	"testing"

	"lioncli/internal/llm"
)

func TestAcceptedMatches(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		values []string
	}{
		{"no refs", "你好世界", nil},
		{"bare path", "@image:./shot.png 这是什么", []string{"./shot.png"}},
		{"angle path with space", "看 @image:<./my shot.png> 这张", []string{"./my shot.png"}},
		{"clipboard", "@clipboard 描述一下", []string{clipboardToken}},
		{"clipboard at end", "描述 @clipboard", []string{clipboardToken}},
		{"clipboard not a word prefix", "@clipboardfoo 不该命中", nil},
		{"clipboard followed by cjk", "@clipboard你好", nil},
		{"clipboard followed by punct", "@clipboard。看图", []string{clipboardToken}},
		{"fullwidth period not in path", "@image:./a.png。后面", []string{"./a.png"}},
		{"two refs", "@clipboard 和 @image:/tmp/x.png", []string{clipboardToken, "/tmp/x.png"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := acceptedMatches(c.input)
			if len(got) != len(c.values) {
				t.Fatalf("匹配数 = %d, 期望 %d (%+v)", len(got), len(c.values), got)
			}
			for i, want := range c.values {
				if got[i].value != want {
					t.Errorf("匹配[%d] = %q, 期望 %q", i, got[i].value, want)
				}
			}
		})
	}
}

func TestStripRefs(t *testing.T) {
	input := "@image:./a.png 这是 @clipboard 什么"
	matches := acceptedMatches(input)
	got := stripRefs(input, matches)
	// 引用被剥掉,保留的文字两端 trim;不强校验中间空白数量。
	if indexOf(got, "@image") >= 0 || indexOf(got, "@clipboard") >= 0 {
		t.Errorf("stripRefs 未剥净引用: %q", got)
	}
	if indexOf(got, "这是") < 0 || indexOf(got, "什么") < 0 {
		t.Errorf("stripRefs 误删正文: %q", got)
	}
}

func TestStripRefsKeepsNonMatch(t *testing.T) {
	// @clipboardfoo 不该被当成引用,因此应原样保留。
	input := "看 @clipboardfoo 这个"
	matches := acceptedMatches(input)
	got := stripRefs(input, matches)
	if got != input {
		t.Errorf("stripRefs 误删非引用: %q", got)
	}
}

func TestUserMessageNoRefsIsPlainText(t *testing.T) {
	msg := UserMessage("普通消息", "")
	if msg.Role != llm.RoleUser {
		t.Fatalf("role = %q", msg.Role)
	}
	if len(msg.Content) != 1 || msg.Content[0].Type != llm.ContentTypeText {
		t.Fatalf("期望单个文本块, 得到 %+v", msg.Content)
	}
	if msg.Content[0].Text != "普通消息" {
		t.Errorf("text = %q", msg.Content[0].Text)
	}
}

func TestUserMessageInvalidRefDegradesToText(t *testing.T) {
	// 指向不存在的文件:应退化为纯文本消息并带"引用无效"说明,不含图片块。
	msg := UserMessage("@image:/no/such/file.png 看图", "")
	if len(msg.Content) != 1 || msg.Content[0].Type != llm.ContentTypeText {
		t.Fatalf("期望退化为单文本块, 得到 %+v", msg.Content)
	}
	if !contains(msg.Content[0].Text, "图片引用无效") {
		t.Errorf("缺少无效说明: %q", msg.Content[0].Text)
	}
}

func TestEstimateBase64Size(t *testing.T) {
	cases := map[int64]int64{0: 0, 1: 4, 2: 4, 3: 4, 4: 8, 6: 8}
	for raw, want := range cases {
		if got := estimateBase64Size(raw); got != want {
			t.Errorf("estimateBase64Size(%d) = %d, 期望 %d", raw, got, want)
		}
	}
}

func TestFitWithin(t *testing.T) {
	// 不超框 → 不缩放。
	if w, h := fitWithin(800, 600, 2000, 2000); w != 800 || h != 600 {
		t.Errorf("fitWithin 不该缩放: %dx%d", w, h)
	}
	// 超框 → 等比缩放,长边落在框上。
	if w, h := fitWithin(4000, 2000, 2000, 2000); w != 2000 || h != 1000 {
		t.Errorf("fitWithin = %dx%d, 期望 2000x1000", w, h)
	}
}

func TestCreateMetadataText(t *testing.T) {
	// 缩放过 → 带坐标换算提示。
	resized := &ProcessedImage{
		SourcePath: "/tmp/a.png",
		Dimensions: &Dimensions{OriginalWidth: 4000, OriginalHeight: 2000, DisplayWidth: 2000, DisplayHeight: 1000},
	}
	md := CreateMetadataText(resized)
	if !contains(md, "Multiply coordinates by 2.00") {
		t.Errorf("缺少坐标换算: %q", md)
	}

	// 无来源、未缩放、未重编码 → 无可说明信息,返回空。
	plain := &ProcessedImage{
		Dimensions: &Dimensions{OriginalWidth: 100, OriginalHeight: 100, DisplayWidth: 100, DisplayHeight: 100},
	}
	if md := CreateMetadataText(plain); md != "" {
		t.Errorf("期望空串, 得到 %q", md)
	}

	// 重编码但尺寸不变 → 说明重编码。
	reenc := &ProcessedImage{
		Dimensions: &Dimensions{OriginalWidth: 100, OriginalHeight: 100, DisplayWidth: 100, DisplayHeight: 100},
		Reencoded:  true,
	}
	if md := CreateMetadataText(reenc); !contains(md, "re-encoded") {
		t.Errorf("缺少重编码说明: %q", md)
	}
}

func TestFileURIToLocalPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		if got := fileURIToLocalPath("file:///C:/Users/a%20b/shot.png"); got != "C:/Users/a b/shot.png" {
			t.Errorf("windows file:// = %q", got)
		}
		return
	}
	// POSIX: 含 percent-encoded 空格。
	if got := fileURIToLocalPath("file:///tmp/a%20b/shot.png"); got != "/tmp/a b/shot.png" {
		t.Errorf("posix file:// = %q", got)
	}
	// 未编码的中文原样保留。
	if got := fileURIToLocalPath("file:///tmp/截图.png"); got != "/tmp/截图.png" {
		t.Errorf("posix file:// cjk = %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
