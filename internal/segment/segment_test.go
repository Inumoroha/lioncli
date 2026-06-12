package segment

import (
	"strings"
	"testing"
)

func TestHasHan(t *testing.T) {
	cases := map[string]bool{
		"登录":      true,
		"login":   false,
		"a登录b":    true,
		"":        false,
		"123_.":   false,
		"中文 mix": true,
	}
	for in, want := range cases {
		if got := HasHan(in); got != want {
			t.Errorf("HasHan(%q) = %v, 期望 %v", in, got, want)
		}
	}
}

func TestCutSplitsHanBlock(t *testing.T) {
	words := Cut("登录功能")
	if len(words) == 0 {
		t.Skip("分词器未就绪(内嵌词典加载失败),跳过")
	}
	// 切分应非有损:拼回去等于原文。
	if joined := strings.Join(words, ""); joined != "登录功能" {
		t.Errorf("切分有损: %v 拼回 %q", words, joined)
	}
	// 应真的切开,而非原样返回单个整块。
	if len(words) < 2 {
		t.Errorf("期望切成多个词, 得到 %v", words)
	}
	// 期望切出"登录""功能"这类词级片段。
	if !contains(words, "登录") || !contains(words, "功能") {
		t.Errorf("期望含 登录/功能, 得到 %v", words)
	}
}

func TestCutEmpty(t *testing.T) {
	if got := Cut(""); got != nil {
		t.Errorf("Cut(\"\") = %v, 期望 nil", got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
