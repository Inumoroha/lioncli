// Package image 是从旧 Java 版(com.paicli.image)移植的图片子系统:
//   - processor.go:解码 → 缩放/铺白底去 alpha/压缩 → base64,围绕 5MB API 上限。
//   - clipboard.go:从系统剪贴板抓图(三平台)。
//   - reference.go:解析 @image:<path> / @clipboard,构造多模态 user 消息。
package image

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	img "image"
	"image/color"
	"image/draw"
	_ "image/gif" // 注册 GIF 解码器
	"image/jpeg"
	"image/png"
	"os"
	"strings"

	xdraw "golang.org/x/image/draw"

	"lioncli/internal/llm"
)

const (
	// APIImageMaxBase64Size 是单张图 base64 后的硬上限(API 侧 5MB)。
	APIImageMaxBase64Size = 5 * 1024 * 1024
	// ImageTargetRawSize 是对应的原始字节目标(base64 膨胀 4/3)。
	ImageTargetRawSize = APIImageMaxBase64Size * 3 / 4
	// MaxSourceImageBytes 是处理前对源文件大小的保护上限。
	MaxSourceImageBytes = 50 * 1024 * 1024
	// IMAGE_MAX_WIDTH / HEIGHT:缩放兜底时的目标外框(token 成本软建议)。
	imageMaxWidth  = 2000
	imageMaxHeight = 2000
)

// jpegQualities 是 PNG 仍过大时的 JPEG 质量阶梯(由高到低)。
var jpegQualities = []int{85, 70, 55, 40, 25}

// Dimensions 记录原始尺寸与交付尺寸,用于生成"坐标换算"提示。
type Dimensions struct {
	OriginalWidth  int
	OriginalHeight int
	DisplayWidth   int
	DisplayHeight  int
}

// ProcessedImage 是处理后的图片:base64 载荷 + MIME + 尺寸/来源等元信息。
type ProcessedImage struct {
	Base64        string
	MimeType      string
	OriginalBytes int64
	OutputBytes   int64
	Dimensions    *Dimensions // 无法解码时为 nil
	SourcePath    string      // 来自剪贴板/字节流时为空
	Reencoded     bool
}

// FromPath 读取磁盘文件并处理。
func FromPath(path, mimeType string) (*ProcessedImage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, errors.New("图片文件为空")
	}
	if size > MaxSourceImageBytes {
		return nil, fmt.Errorf("图片超过 %dMB 处理上限", MaxSourceImageBytes/1024/1024)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Process(data, mimeType, path)
}

// FromBytes 处理内存中的图片字节。
func FromBytes(data []byte, mimeType string) (*ProcessedImage, error) {
	return Process(data, mimeType, "")
}

// Process 是核心:对照 Java 版逐分支移植。
//  1. 字节已在 API 上限内 + 无 alpha → 直通原始 bytes,不动尺寸。
//  2. 有 alpha 但在上限内 → 铺白底 flatten,PNG 输出,保持尺寸。
//  3. 仍 > 5MB → 等比缩放进 2000×2000,优先 PNG,过大退 JPEG 多档,再不行降到 1200。
func Process(data []byte, mimeType, sourcePath string) (*ProcessedImage, error) {
	if len(data) == 0 {
		return nil, errors.New("图片数据为空")
	}
	normalizedMime := normalizeMimeType(mimeType)

	image := tryDecode(data)
	if image == nil {
		// 运行时无法解码该格式:在上限内则直通,否则报错(与 Java 一致)。
		if estimateBase64Size(int64(len(data))) <= APIImageMaxBase64Size {
			return &ProcessedImage{
				Base64:        base64.StdEncoding.EncodeToString(data),
				MimeType:      normalizedMime,
				OriginalBytes: int64(len(data)),
				OutputBytes:   int64(len(data)),
				SourcePath:    sourcePath,
			}, nil
		}
		return nil, errors.New("图片超过 5MB API 上限，且当前运行时无法解码压缩该格式")
	}

	b := image.Bounds()
	originalWidth, originalHeight := b.Dx(), b.Dy()
	overSize := estimateBase64Size(int64(len(data))) > APIImageMaxBase64Size
	hasAlpha := imageHasAlpha(image)

	// 1) 直通
	if !overSize && !hasAlpha {
		return &ProcessedImage{
			Base64:        base64.StdEncoding.EncodeToString(data),
			MimeType:      normalizedMime,
			OriginalBytes: int64(len(data)),
			OutputBytes:   int64(len(data)),
			Dimensions:    &Dimensions{originalWidth, originalHeight, originalWidth, originalHeight},
			SourcePath:    sourcePath,
		}, nil
	}

	// 2) 在上限内但有 alpha → 铺白底 PNG
	if !overSize && hasAlpha {
		flattened, err := writePng(flattenAlpha(image))
		if err != nil {
			return nil, err
		}
		if estimateBase64Size(int64(len(flattened))) <= APIImageMaxBase64Size {
			return &ProcessedImage{
				Base64:        base64.StdEncoding.EncodeToString(flattened),
				MimeType:      "image/png",
				OriginalBytes: int64(len(data)),
				OutputBytes:   int64(len(flattened)),
				Dimensions:    &Dimensions{originalWidth, originalHeight, originalWidth, originalHeight},
				SourcePath:    sourcePath,
				Reencoded:     true,
			}, nil
		}
		// flatten 后仍过大 → 落到 3)
	}

	// 3) 缩放兜底
	tw, th := fitWithin(originalWidth, originalHeight, imageMaxWidth, imageMaxHeight)
	resized := resize(image, tw, th)

	resizedPng, err := writePng(resized)
	if err != nil {
		return nil, err
	}
	if estimateBase64Size(int64(len(resizedPng))) <= APIImageMaxBase64Size {
		return &ProcessedImage{
			Base64:        base64.StdEncoding.EncodeToString(resizedPng),
			MimeType:      "image/png",
			OriginalBytes: int64(len(data)),
			OutputBytes:   int64(len(resizedPng)),
			Dimensions:    &Dimensions{originalWidth, originalHeight, tw, th},
			SourcePath:    sourcePath,
			Reencoded:     true,
		}, nil
	}

	var encoded []byte
	for _, q := range jpegQualities {
		candidate, err := writeJpeg(resized, q)
		if err != nil {
			return nil, err
		}
		if estimateBase64Size(int64(len(candidate))) <= APIImageMaxBase64Size {
			encoded = candidate
			break
		}
	}
	if encoded == nil && (tw > 512 || th > 512) {
		sw, sh := fitWithin(originalWidth, originalHeight, 1200, 1200)
		resized = resize(image, sw, sh)
		tw, th = sw, sh
		for _, q := range jpegQualities {
			candidate, err := writeJpeg(resized, q)
			if err != nil {
				return nil, err
			}
			if estimateBase64Size(int64(len(candidate))) <= APIImageMaxBase64Size {
				encoded = candidate
				break
			}
		}
	}
	if encoded == nil {
		return nil, errors.New("图片压缩后仍超过 5MB API 上限")
	}

	return &ProcessedImage{
		Base64:        base64.StdEncoding.EncodeToString(encoded),
		MimeType:      "image/jpeg",
		OriginalBytes: int64(len(data)),
		OutputBytes:   int64(len(encoded)),
		Dimensions:    &Dimensions{originalWidth, originalHeight, tw, th},
		SourcePath:    sourcePath,
		Reencoded:     true,
	}, nil
}

// CreateMetadataText 生成随消息附带的图片元信息说明(来源/缩放坐标换算/重编码)。
// 无可说明信息时返回空串。
func CreateMetadataText(image *ProcessedImage) string {
	if image == nil {
		return ""
	}
	dims := image.Dimensions
	if dims == nil {
		if image.SourcePath == "" {
			return ""
		}
		return "[Image source: " + image.SourcePath + "]"
	}
	wasResized := dims.OriginalWidth != dims.DisplayWidth || dims.OriginalHeight != dims.DisplayHeight
	hasSource := image.SourcePath != ""
	if !hasSource && !wasResized && !image.Reencoded {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("[Image: ")
	needsComma := false
	if hasSource {
		sb.WriteString("source: ")
		sb.WriteString(image.SourcePath)
		needsComma = true
	}
	if wasResized {
		if needsComma {
			sb.WriteString(", ")
		}
		scale := float64(dims.OriginalWidth) / float64(max(1, dims.DisplayWidth))
		fmt.Fprintf(&sb, "original %dx%d, displayed at %dx%d. Multiply coordinates by %.2f to map to original image.",
			dims.OriginalWidth, dims.OriginalHeight, dims.DisplayWidth, dims.DisplayHeight, scale)
	} else if image.Reencoded {
		if needsComma {
			sb.WriteString(", ")
		}
		sb.WriteString("re-encoded for API size limit without changing dimensions.")
	}
	sb.WriteString("]")
	return sb.String()
}

// ToContentPart 把处理后的图片转成统一的图片内容块。
func ToContentPart(image *ProcessedImage) llm.ContentBlock {
	return llm.ImageContent(image.Base64, image.MimeType)
}

// tryDecode 尝试解码;失败返回 nil(交由上层走直通/报错分支),不当作硬错误。
func tryDecode(data []byte) img.Image {
	image, _, err := img.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return image
}

// imageHasAlpha 判断图片是否含 alpha 通道。优先用 Opaque()(标准类型都实现),
// 否则回退看 color model。
func imageHasAlpha(image img.Image) bool {
	if o, ok := image.(interface{ Opaque() bool }); ok {
		return !o.Opaque()
	}
	switch image.ColorModel() {
	case color.NRGBAModel, color.RGBAModel, color.NRGBA64Model, color.RGBA64Model, color.AlphaModel, color.Alpha16Model:
		return true
	}
	return false
}

func normalizeMimeType(mimeType string) string {
	if strings.TrimSpace(mimeType) == "" {
		return "image/png"
	}
	normalized := strings.ToLower(mimeType)
	if normalized == "image/jpg" {
		return "image/jpeg"
	}
	return normalized
}

// estimateBase64Size 估算 rawBytes 经 base64 后的长度。
func estimateBase64Size(rawBytes int64) int64 {
	return ((rawBytes + 2) / 3) * 4
}

// fitWithin 等比缩到不超过 maxWidth×maxHeight(只缩不放)。
func fitWithin(width, height, maxWidth, maxHeight int) (int, int) {
	scale := min(1.0, min(float64(maxWidth)/float64(width), float64(maxHeight)/float64(height)))
	return max(1, int(float64(width)*scale+0.5)), max(1, int(float64(height)*scale+0.5))
}

// resize 用 CatmullRom 缩放,并铺白底把可能的 alpha 合成掉。
func resize(src img.Image, width, height int) img.Image {
	dst := img.NewRGBA(img.Rect(0, 0, width, height))
	draw.Draw(dst, dst.Bounds(), img.NewUniform(color.White), img.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}

// flattenAlpha 把图片合成到白底上(尺寸不变)。
func flattenAlpha(src img.Image) img.Image {
	b := src.Bounds()
	dst := img.NewRGBA(img.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img.NewUniform(color.White), img.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

func writePng(image img.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeJpeg(image img.Image, quality int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
