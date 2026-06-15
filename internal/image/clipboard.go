package image

import (
	"bytes"
	"context"
	"fmt"
	img "image"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// grabTimeout 是单次外部命令(powershell/osascript/xclip)的超时。
const grabTimeout = 8 * time.Second

// GrabResult 是一次剪贴板抓图的结果:成功带落地的 PNG 路径,失败带原因。
type GrabResult struct {
	OK    bool
	Path  string
	Error string
}

func grabOK(path string) GrabResult    { return GrabResult{OK: true, Path: path} }
func grabErr(reason string) GrabResult { return GrabResult{Error: reason} }

// defaultCacheDir 是抓图落地目录 ~/.teacli/cache(取不到 home 退回临时目录)。
func defaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".teacli", "cache")
}

// Grab 从系统剪贴板抓取图片,落地成 PNG 后返回路径。按平台分派。
func Grab() GrabResult {
	return grabInto(defaultCacheDir())
}

func grabInto(cacheDir string) GrabResult {
	switch runtime.GOOS {
	case "windows":
		return grabWindows(cacheDir)
	case "darwin":
		return grabMac(cacheDir)
	case "linux":
		return grabLinux(cacheDir)
	default:
		return grabErr("当前平台暂不支持剪贴板抓图: " + runtime.GOOS)
	}
}

// stampedPath 生成一个带纳秒时间戳的缓存文件路径。
func stampedPath(cacheDir, ext string) string {
	return filepath.Join(cacheDir, fmt.Sprintf("clip-%d%s", time.Now().UnixNano(), ext))
}

// --- Windows ---

// windowsClipScript 在 STA 线程上用 .NET 取剪贴板图片存成 PNG。
// 输出路径经环境变量 TEACLI_CLIP_OUT 传入(避免命令行/编码转义),脚本保持纯 ASCII。
const windowsClipScript = `
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$img = [System.Windows.Forms.Clipboard]::GetImage()
if ($img -eq $null) { [Console]::Error.WriteLine('no image on clipboard'); exit 2 }
$img.Save($env:TEACLI_CLIP_OUT, [System.Drawing.Imaging.ImageFormat]::Png)
$img.Dispose()
`

func grabWindows(cacheDir string) GrabResult {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return grabErr("创建缓存目录失败: " + err.Error())
	}
	out := stampedPath(cacheDir, ".png")

	ctx, cancel := context.WithTimeout(context.Background(), grabTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", "-")
	cmd.Env = append(os.Environ(), "TEACLI_CLIP_OUT="+out)
	cmd.Stdin = strings.NewReader(windowsClipScript)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		os.Remove(out)
		return grabErr("读取剪贴板图片超时")
	}
	if err == nil {
		if fi, e := os.Stat(out); e == nil && fi.Size() > 0 {
			return grabOK(out)
		}
	}
	os.Remove(out)
	msg := strings.TrimSpace(stderr.String())
	if msg == "" || strings.Contains(msg, "no image on clipboard") {
		msg = "剪贴板里没有图片，请先截图后再触发（Windows: Win+Shift+S）"
	}
	return grabErr(msg)
}

// --- macOS ---

// macClipboardPNGScript / TIFFScript 直接移植自 Java 版:优先取 PNGf,失败退 TIFF。
const macClipboardPNGScript = `on run argv
    set outputPath to item 1 of argv
    try
        set pngData to (the clipboard as «class PNGf»)
    on error errMsg
        error "剪贴板里没有 PNG 数据"
    end try
    set fh to open for access (POSIX file outputPath as string) with write permission
    try
        set eof of fh to 0
        write pngData to fh
        close access fh
    on error errMsg
        try
            close access fh
        end try
        error errMsg
    end try
end run`

const macClipboardTIFFScript = `on run argv
    set outputPath to item 1 of argv
    try
        set tiffData to (the clipboard as «class TIFF»)
    on error errMsg
        error "剪贴板里没有 TIFF 数据"
    end try
    set fh to open for access (POSIX file outputPath as string) with write permission
    try
        set eof of fh to 0
        write tiffData to fh
        close access fh
    on error errMsg
        try
            close access fh
        end try
        error errMsg
    end try
end run`

func grabMac(cacheDir string) GrabResult {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return grabErr("创建缓存目录失败: " + err.Error())
	}
	out := stampedPath(cacheDir, ".png")

	pngStderr, pngTimeout, pngOK := runOsascript(macClipboardPNGScript, out)
	if pngTimeout {
		os.Remove(out)
		return grabErr("读取剪贴板图片超时")
	}
	if pngOK {
		if fi, e := os.Stat(out); e == nil && fi.Size() > 0 {
			return grabOK(out)
		}
	}
	os.Remove(out)

	tiff := stampedPath(cacheDir, ".tiff")
	defer os.Remove(tiff)
	tiffStderr, tiffTimeout, tiffOK := runOsascript(macClipboardTIFFScript, tiff)
	if tiffTimeout {
		return grabErr("读取剪贴板图片超时")
	}
	if tiffOK {
		if fi, e := os.Stat(tiff); e == nil && fi.Size() > 0 {
			if convertTiffToPng(tiff, out) {
				return grabOK(out)
			}
		}
	}

	msg := strings.TrimSpace(pngStderr)
	if msg == "" {
		msg = strings.TrimSpace(tiffStderr)
	}
	if msg == "" {
		msg = "剪贴板里没有图片，请先截图后再触发"
	}
	return grabErr(msg)
}

// runOsascript 经 stdin 喂脚本、把输出路径作为 argv 传入,返回 (stderr, 是否超时, 是否成功)。
func runOsascript(script, outputPath string) (string, bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), grabTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-", outputPath)
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", true, false
	}
	return stderr.String(), false, err == nil
}

// convertTiffToPng 用系统自带 sips 把 TIFF 转 PNG。
func convertTiffToPng(tiff, png string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), grabTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sips", "-s", "format", "png", tiff, "--out", png)
	if err := cmd.Run(); err != nil {
		return false
	}
	if fi, e := os.Stat(png); e == nil && fi.Size() > 0 {
		return true
	}
	return false
}

// --- Linux ---

func grabLinux(cacheDir string) GrabResult {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return grabErr("创建缓存目录失败: " + err.Error())
	}
	out := stampedPath(cacheDir, ".png")

	// 先 Wayland(wl-paste),再 X11(xclip);任一拿到 PNG 字节即成功。
	for _, attempt := range [][]string{
		{"wl-paste", "-t", "image/png"},
		{"xclip", "-selection", "clipboard", "-t", "image/png", "-o"},
	} {
		data, ok := runCapture(attempt[0], attempt[1:]...)
		if ok && len(data) > 0 {
			if err := os.WriteFile(out, data, 0o644); err == nil {
				return grabOK(out)
			}
		}
	}
	return grabErr("未能从剪贴板读取图片（需要 wl-paste 或 xclip，且剪贴板中有图片）")
}

// runCapture 跑命令并返回 stdout;命令不存在/失败/超时都返回 ok=false。
func runCapture(name string, args ...string) ([]byte, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), grabTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, false
	}
	return stdout.Bytes(), true
}

// Describe 返回图片的简短描述:文件名 (宽x高, 人类可读大小)。失败时退回文件名。
func Describe(imagePath string) string {
	if imagePath == "" {
		return ""
	}
	name := filepath.Base(imagePath)
	info, err := os.Stat(imagePath)
	if err != nil {
		return name
	}
	size := humanBytes(info.Size())
	f, err := os.Open(imagePath)
	if err != nil {
		return fmt.Sprintf("%s (%s)", name, size)
	}
	defer f.Close()
	cfg, _, err := img.DecodeConfig(f)
	if err != nil {
		return fmt.Sprintf("%s (%s)", name, size)
	}
	return fmt.Sprintf("%s (%dx%d, %s)", name, cfg.Width, cfg.Height, size)
}

func humanBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%dB", b)
	}
	kb := float64(b) / 1024.0
	if kb < 1024 {
		return fmt.Sprintf("%.0fKB", kb)
	}
	return fmt.Sprintf("%.1fMB", kb/1024.0)
}
