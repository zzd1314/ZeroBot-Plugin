// 「音乐状态」播放卡渲染：专辑封面 + 可视化进度条 + 同步歌词，
// 深色毛玻璃面板风格，纯 Go 图像库（gg/imaging）实现。
// 渲染禁忌（与 servicemenu 一致）：曲线描边/圆角 Stroke 有垃圾像素 bug
// 只用直线段与填充；半透明色一律 SetRGBA255；♪✓ 等字形 GlowSansSC 缺失。
package splayer

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"math"

	// 注册 jpg/gif 解码器，封面解码不依赖其他插件是否加载
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/FloatTech/gg"
	"github.com/disintegration/imaging"
	"github.com/sirupsen/logrus"
)

const (
	fontBold = "data/Font/GlowSansSC-Normal-ExtraBold.ttf"
	fontReg  = "data/Font/regular-bold.ttf"
)

// coverCache 曲目封面缓存（key = 曲目ID|URL），避免每次刷新重复拉取
var (
	coverCache   = map[string]image.Image{}
	coverCacheMu sync.Mutex
	coverClient  = &http.Client{Timeout: 10 * time.Second}
)

// coverURL 规范化封面地址（相对路径补 apiBase）
func coverURL(raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return apiBase + raw
	}
	return raw
}

// fetchCover 拉取当前曲目封面并预裁为 200x200；无地址或失败返回 nil（占位图回退）
func fetchCover(t *track) image.Image {
	u := coverURL(t.Cover)
	if u == "" {
		return nil
	}
	key := t.ID + "|" + u
	coverCacheMu.Lock()
	if img, ok := coverCache[key]; ok {
		coverCacheMu.Unlock()
		return img
	}
	coverCacheMu.Unlock()

	resp, err := coverClient.Get(u)
	if err != nil {
		logrus.Warnf("[splayer] 拉取封面失败: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logrus.Warnf("[splayer] 拉取封面返回 %d", resp.StatusCode)
		return nil
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		logrus.Warnf("[splayer] 解码封面失败: %v", err)
		return nil
	}
	small := imaging.Fill(img, 200, 200, imaging.Center, imaging.Lanczos)
	coverCacheMu.Lock()
	coverCache[key] = small
	coverCacheMu.Unlock()
	return small
}

// renderNowPlayingCard 渲染播放卡：内部补拉歌词与封面
func renderNowPlayingCard(np *nowPlaying) ([]byte, error) {
	var ld *lyricData
	if np.Track != nil && np.LyricAvailable {
		if l, err := getLyric(); err == nil && l.TrackID == np.Track.ID {
			ld = l
		}
	}
	return renderCard(np, ld)
}

const (
	cardW = 900
	cardH = 640
)

// renderCard 纯渲染（ld 可为 nil），测试可直接注入伪造数据
func renderCard(np *nowPlaying, ld *lyricData) ([]byte, error) {
	c := gg.NewContext(cardW, cardH)

	// === 背景：封面放大模糊 + 压暗，无封面时深色渐变 ===
	cover := fetchCover(np.Track)
	if cover != nil {
		bg := imaging.Blur(imaging.Fill(cover, cardW, cardH, imaging.Center, imaging.Lanczos), 28)
		c.DrawImage(bg, 0, 0)
		c.SetRGBA255(8, 10, 18, 130)
		c.DrawRectangle(0, 0, cardW, cardH)
		c.Fill()
	} else {
		g := gg.NewLinearGradient(0, 0, cardW, cardH)
		g.AddColorStop(0, color.RGBA{R: 44, G: 50, B: 78, A: 255})
		g.AddColorStop(1, color.RGBA{R: 18, G: 20, B: 34, A: 255})
		c.SetFillStyle(g)
		c.DrawRectangle(0, 0, cardW, cardH)
		c.Fill()
	}

	// === 深色圆角面板（填充安全，禁 Stroke）===
	c.SetRGBA255(14, 16, 26, 185)
	c.DrawRoundedRectangle(28, 28, cardW-56, cardH-56, 24)
	c.Fill()

	// === 专辑封面（左上 200px 圆角）===
	const (
		coverX = 80
		coverY = 90
		coverS = 200
	)
	if cover != nil {
		drawImageRounded(c.Image().(*image.RGBA), cover, coverX, coverY, coverS, 16)
	} else {
		drawCoverPlaceholder(c, coverX, coverY, coverS, 16)
	}

	// === 播放状态徽章（右上角）===
	drawPlayBadge(c, 790, 130, 26, np.Playing)

	// === 标题 / 歌手 / 专辑 ===
	t := np.Track
	title := ellipsizeByWidth(c, t.Title, fontBold, 30, 430)
	drawTextOutlined(c, title, fontBold, 30, 320, 142, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	names := make([]string, 0, len(t.Artists))
	for _, a := range t.Artists {
		names = append(names, a.Name)
	}
	drawTextOutlined(c, ellipsizeByWidth(c, strings.Join(names, "/"), fontReg, 18, 470),
		fontReg, 18, 320, 182, color.RGBA{R: 255, G: 255, B: 255, A: 200})
	if t.Album.Name != "" {
		drawTextOutlined(c, ellipsizeByWidth(c, "专辑 · "+t.Album.Name, fontReg, 14, 470),
			fontReg, 14, 320, 212, color.RGBA{R: 255, G: 255, B: 255, A: 150})
	}

	// === 可视化进度条 ===
	drawProgressBar(c, np)

	// === 同步歌词 ===
	drawLyrics(c, np, ld)

	// === 底部提示 ===
	drawTextOutlined(c, "发送「音乐歌词」查看完整歌词", fontReg, 13, 80, 590,
		color.RGBA{R: 255, G: 255, B: 255, A: 110})

	var buf bytes.Buffer
	if err := png.Encode(&buf, c.Image()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// drawProgressBar 可视化进度条：胶囊轨道 + 已播进度 + 旋钮 + 两端时间
func drawProgressBar(c *gg.Context, np *nowPlaying) {
	const (
		bx = 320.0
		by = 232.0
		bw = 500.0
		bh = 10.0
	)
	// 轨道
	c.SetRGBA255(255, 255, 255, 55)
	c.DrawRoundedRectangle(bx, by, bw, bh, bh/2)
	c.Fill()
	// 已播进度 + 旋钮
	dur := int64(0)
	if np.Track != nil {
		dur = np.Track.Duration
	}
	if dur > 0 {
		p := float64(np.Position) / float64(dur)
		if p < 0 {
			p = 0
		}
		if p > 1 {
			p = 1
		}
		fw := bw * p
		if fw < bh { // 宽度小于直径时圆角矩形畸形，取直径保底
			fw = bh
		}
		c.SetRGBA255(255, 255, 255, 230)
		c.DrawRoundedRectangle(bx, by, fw, bh, bh/2)
		c.Fill()
		c.SetRGBA255(255, 255, 255, 255)
		c.DrawCircle(bx+fw, by+bh/2, 9)
		c.Fill()
	}
	// 两端时间
	cur := fmtMs(np.Position)
	drawTextOutlined(c, cur, fontReg, 14, bx, by+40, color.RGBA{R: 255, G: 255, B: 255, A: 190})
	total := fmtMs(dur)
	if tw, _ := c.MeasureString(total); tw > 0 {
		drawTextOutlined(c, total, fontReg, 14, bx+bw-tw, by+40, color.RGBA{R: 255, G: 255, B: 255, A: 190})
	}
}

// drawLyrics 同步歌词：当前行高亮（主歌词+翻译），后续 4 行渐隐
func drawLyrics(c *gg.Context, np *nowPlaying, ld *lyricData) {
	const (
		lx    = 80.0
		mainW = 740.0
		nextH = 34.0
	)
	nextAlpha := [4]uint8{150, 115, 85, 60}
	if ld == nil || len(ld.Lyric) == 0 {
		drawTextOutlined(c, "暂无歌词", fontReg, 18, lx, 380, color.RGBA{R: 255, G: 255, B: 255, A: 140})
		return
	}
	cur := ld.currentLine(np.Position)
	base := 406.0
	if cur >= 0 {
		parts := strings.SplitN(ld.Lyric[cur].lineText(), "\n", 2)
		drawKaraokeLine(c, &ld.Lyric[cur], np.Position, fontBold, 24, lx, 366, mainW)
		if len(parts) > 1 {
			drawTextOutlined(c, ellipsizeByWidth(c, parts[1], fontReg, 16, mainW),
				fontReg, 16, lx, 396, color.RGBA{R: 255, G: 255, B: 255, A: 190})
			base = 436
		}
	}
	start := cur + 1
	if start < 0 {
		start = 0
	}
	for i := 0; i < len(nextAlpha); i++ {
		idx := start + i
		if idx >= len(ld.Lyric) {
			break
		}
		parts := strings.SplitN(ld.Lyric[idx].lineText(), "\n", 2)
		a := nextAlpha[i]
		drawTextOutlined(c, ellipsizeByWidth(c, parts[0], fontReg, 18, mainW),
			fontReg, 18, lx, base+nextH*float64(i), color.RGBA{R: 255, G: 255, B: 255, A: a})
	}
}

// lineMain 行主歌词（不含翻译）
func lineMain(l *lyricLine) string {
	var b strings.Builder
	for _, w := range l.Words {
		b.WriteString(w.Word)
	}
	return b.String()
}

// spanAlpha [start,end] 时间区间内 position 的点亮程度 0(未唱)..1(已唱)
func spanAlpha(start, end, position int64) float64 {
	if end <= start {
		if position >= start {
			return 1
		}
		return 0
	}
	if position >= end {
		return 1
	}
	if position <= start {
		return 0
	}
	return float64(position-start) / float64(end-start)
}

// karaokeAlphas 计算行内每个 rune 的点亮程度 0..1。词级时间戳可用时按词
// 精确定位（唱中的词内部线性插值）；缺失或与主歌词 rune 数错位时回退为
// 行内字符匀速近似（行时长按字符数均分，逐字跳变点亮）。
func karaokeAlphas(l *lyricLine, runes []rune, positionMs int64) []float64 {
	total := len(runes)
	alphas := make([]float64, total)
	// 仅多词且带时间戳的行才走精确模式（QRC 逐字源）；单词行即行级源
	// （实测 /api/lyrics 对 LRC 源输出 words:[整行]），匀速逐字观感更佳
	wordTimed := len(l.Words) >= 2
	if wordTimed {
		hasTime := false
		for _, w := range l.Words {
			if w.StartTime > 0 || w.EndTime > 0 {
				hasTime = true
				break
			}
		}
		wordTimed = hasTime
	}
	if wordTimed {
		idx := 0
		ok := true
		for _, w := range l.Words {
			a := spanAlpha(w.StartTime, w.EndTime, positionMs)
			for range w.Word {
				if idx >= total {
					ok = false
					break
				}
				alphas[idx] = a
				idx++
			}
			if !ok {
				break
			}
		}
		if ok && idx == total {
			return alphas
		}
		for i := range alphas { // 数据错位，清零回退匀速
			alphas[i] = 0
		}
	}
	prog := 1.0
	if l.EndTime > l.StartTime {
		prog = clampF64(float64(positionMs-l.StartTime)/float64(l.EndTime-l.StartTime), 0, 1)
	}
	for i := 0; i < total; i++ {
		lo := float64(i) / float64(total)
		hi := float64(i+1) / float64(total)
		switch {
		case prog >= hi:
			alphas[i] = 1
		case prog > lo:
			alphas[i] = (prog - lo) / (hi - lo)
		}
	}
	return alphas
}

func clampF64(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// drawKaraokeLine 逐字点亮绘制当前歌词行（卡拉OK效果）：整行画暗色描边底，
// 主体逐 rune 按已唱进度着色（未唱 α150 → 已唱 α255，唱中插值渐变）。
// 超宽时截断 rune 前缀加省略号（与 ellipsizeByWidth 同款二分）。
func drawKaraokeLine(c *gg.Context, l *lyricLine, positionMs int64, fontPath string, size, x, y, maxW float64) {
	loadFont(c, fontPath, size)
	runes := []rune(lineMain(l))
	if len(runes) == 0 {
		return
	}
	alphas := karaokeAlphas(l, runes, positionMs)
	main := string(runes)
	if tw, _ := c.MeasureString(main); tw > maxW {
		lo, hi := 0, len(runes)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if w, _ := c.MeasureString(string(runes[:mid]) + "..."); w <= maxW {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		runes = runes[:lo]
		alphas = alphas[:lo]
		main = string(runes) + "..."
	}
	c.SetRGBA255(25, 28, 40, 170)
	for _, d := range [...][2]float64{
		{-1, 0}, {1, 0}, {0, -1}, {0, 1},
		{-0.71, -0.71}, {0.71, -0.71}, {-0.71, 0.71}, {0.71, 0.71},
	} {
		c.DrawString(main, x+d[0], y+d[1])
	}
	c.SetRGBA255(25, 28, 40, 70)
	for _, d := range [...][2]float64{
		{-1.8, 0}, {1.8, 0}, {0, -1.8}, {0, 1.8},
		{-1.3, -1.3}, {1.3, -1.3}, {-1.3, 1.3}, {1.3, 1.3},
	} {
		c.DrawString(main, x+d[0], y+d[1])
	}
	cx := x
	for i, r := range runes {
		s := string(r)
		a := 150 + int(math.Round(alphas[i]*105))
		c.SetRGBA255(255, 255, 255, a)
		c.DrawString(s, cx, y)
		if w, _ := c.MeasureString(s); w > 0 {
			cx += w
		}
	}
}

// drawPlayBadge 播放状态徽章：绿色圆+播放三角 / 橙色圆+暂停双条（纯填充）
func drawPlayBadge(c *gg.Context, cx, cy, r float64, playing bool) {
	c.SetRGBA255(0, 0, 0, 90)
	c.DrawCircle(cx, cy, r+3)
	c.Fill()
	if playing {
		c.SetRGBA255(104, 166, 0, 235)
	} else {
		c.SetRGBA255(242, 153, 74, 235)
	}
	c.DrawCircle(cx, cy, r)
	c.Fill()
	c.SetRGBA255(255, 255, 255, 255)
	if playing {
		c.MoveTo(cx-7, cy-12)
		c.LineTo(cx-7, cy+12)
		c.LineTo(cx+14, cy)
		c.Fill()
	} else {
		c.DrawRectangle(cx-10, cy-12, 7, 24)
		c.Fill()
		c.DrawRectangle(cx+3, cy-12, 7, 24)
		c.Fill()
	}
}

// drawCoverPlaceholder 无封面占位：深色圆角底 + 矢量音符（纯填充）
func drawCoverPlaceholder(c *gg.Context, x, y, size int, radius float64) {
	c.SetRGBA255(52, 58, 82, 255)
	c.DrawRoundedRectangle(float64(x), float64(y), float64(size), float64(size), radius)
	c.Fill()
	cx, cy := float64(x)+float64(size)/2, float64(y)+float64(size)/2
	c.SetRGBA255(255, 255, 255, 220)
	// 两个音符头
	c.DrawCircle(cx-17, cy+16, 9)
	c.Fill()
	c.DrawCircle(cx+15, cy+22, 9)
	c.Fill()
	// 两根符干
	c.DrawRectangle(cx-10, cy-20, 4.5, 36)
	c.Fill()
	c.DrawRectangle(cx+22, cy-14, 4.5, 36)
	c.Fill()
	// 连接符梁（斜四边形）
	c.MoveTo(cx-10, cy-24)
	c.LineTo(cx+26.5, cy-18)
	c.LineTo(cx+26.5, cy-10)
	c.LineTo(cx-10, cy-16)
	c.Fill()
}

// roundedAlphaMask 生成圆角方形 alpha 蒙版（圆角图片用 DrawMask，勿用 gg Clip）
func roundedAlphaMask(size int, radius float64) *image.Alpha {
	mc := gg.NewContext(size, size)
	mc.SetRGBA255(255, 255, 255, 255)
	mc.DrawRoundedRectangle(0, 0, float64(size), float64(size), radius)
	mc.Fill()
	m := image.NewAlpha(image.Rect(0, 0, size, size))
	draw.Draw(m, m.Rect, mc.Image(), image.Point{}, draw.Src)
	return m
}

// drawImageRounded 以圆角蒙版把 src 绘制到 dst 的 (x,y) 处
func drawImageRounded(dst *image.RGBA, src image.Image, x, y, size int, radius float64) {
	rect := image.Rect(x, y, x+size, y+size)
	draw.DrawMask(dst, rect, src, image.Point{}, roundedAlphaMask(size, radius), image.Point{}, draw.Over)
}

// loadFont 加载字体到上下文，失败时告警（失败会退回默认字体导致排版错乱）
func loadFont(c *gg.Context, path string, size float64) {
	if err := c.LoadFontFace(path, size); err != nil {
		logrus.Warnf("[splayer] 加载字体失败 %s: %v", path, err)
	}
}

// ellipsizeByWidth 按像素宽度截断文本（rune 安全），超宽以 "..." 结尾
func ellipsizeByWidth(c *gg.Context, text, fontPath string, size, maxW float64) string {
	loadFont(c, fontPath, size)
	if tw, _ := c.MeasureString(text); tw <= maxW {
		return text
	}
	const ell = "..."
	runes := []rune(text)
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if w, _ := c.MeasureString(string(runes[:mid]) + ell); w <= maxW {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return string(runes[:lo]) + ell
}

// drawTextOutlined 带暗色描边绘制文字：FloatTech/gg 的 Stroke 在曲线字形上
// 会写入垃圾像素，因此用 8 向偏移暗色底模拟描边再绘主体
func drawTextOutlined(c *gg.Context, text, fontPath string, size float64, x, y float64, fill color.RGBA) {
	loadFont(c, fontPath, size)
	c.SetRGBA255(25, 28, 40, 170)
	for _, d := range [...][2]float64{
		{-1, 0}, {1, 0}, {0, -1}, {0, 1},
		{-0.71, -0.71}, {0.71, -0.71}, {-0.71, 0.71}, {0.71, 0.71},
	} {
		c.DrawString(text, x+d[0], y+d[1])
	}
	c.SetRGBA255(25, 28, 40, 70)
	for _, d := range [...][2]float64{
		{-1.8, 0}, {1.8, 0}, {0, -1.8}, {0, 1.8},
		{-1.3, -1.3}, {1.3, -1.3}, {-1.3, 1.3}, {1.3, 1.3},
	} {
		c.DrawString(text, x+d[0], y+d[1])
	}
	c.SetColor(fill)
	c.DrawString(text, x, y)
}
