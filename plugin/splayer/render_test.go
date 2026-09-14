package splayer

// 渲染测试：不走 bot，直接用真实渲染管线输出 PNG 供视觉验证
// 运行: go test -run TestRenderCardPNG ./plugin/splayer
// chdir 到项目根目录保证 data/Font 相对路径可用

import (
	"image/color"
	"os"
	"testing"

	"github.com/FloatTech/gg"
)

func TestMain(m *testing.M) {
	_ = os.Chdir("../..") // 项目根目录
	os.Exit(m.Run())
}

// fakeTrackData 构造伪造播放状态与歌词（Cover 留空走占位图路径，不依赖外网）
func fakeTrackData() (*nowPlaying, *lyricData) {
	np := &nowPlaying{
		Track: &track{
			ID:       "test-1",
			Title:    "Example Song 这是一段相当长的中文歌曲标题用来验证省略号截断效果",
			Artists:  []artist{{Name: "Alice"}, {Name: "Bob"}, {Name: "Carol"}},
			Album:    album{Name: "Night Drive 夜行专辑"},
			Duration: 213000,
		},
		Position:       74000,
		Playing:        true,
		LyricAvailable: true,
	}
	ld := &lyricData{TrackID: "test-1", Lyric: []lyricLine{
		{StartTime: 0, EndTime: 15000, Words: []lyricWord{{Word: "前奏响起的第一个音符"}}},
		{StartTime: 15000, EndTime: 30000, Words: []lyricWord{{Word: "夜色在城市上空缓缓降落"}}},
		{StartTime: 30000, EndTime: 60000, Words: []lyricWord{{Word: "街灯把影子拉得很长很长"}}},
		// 当前行：带词级时间戳（卡拉OK精确点亮），position 74000 时唱到"歌词"中段
		{StartTime: 60000, EndTime: 88000, Words: []lyricWord{
			{Word: "当前", StartTime: 60000, EndTime: 63000},
			{Word: "正在", StartTime: 63000, EndTime: 66000},
			{Word: "播放", StartTime: 66000, EndTime: 69000},
			{Word: "的这一句", StartTime: 69000, EndTime: 73000},
			{Word: "歌词", StartTime: 73000, EndTime: 76000},
			{Word: " Current Line", StartTime: 76000, EndTime: 88000},
		},
			TranslatedLyric: "This is the translated subtitle line"},
		{StartTime: 88000, EndTime: 104000, Words: []lyricWord{{Word: "接下来的一句歌词，看看渐隐的层次感是否自然"}}},
		{StartTime: 104000, EndTime: 120000, Words: []lyricWord{{Word: "第三行之后透明度应该越来越低"}},
			TranslatedLyric: "third line with translation"},
		{StartTime: 120000, EndTime: 136000, Words: []lyricWord{{Word: "And the fourth line is quite long to verify the ellipsis truncation behavior"}}},
		{StartTime: 136000, EndTime: 152000, Words: []lyricWord{{Word: "最后一行在窗口之外不应显示"}}},
	}}
	return np, ld
}

func writeCardPNG(t *testing.T, np *nowPlaying, ld *lyricData, out string) {
	t.Helper()
	png, err := renderCard(np, ld)
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if len(png) < 1000 {
		t.Fatalf("PNG 过小: %d bytes", len(png))
	}
	if err := os.WriteFile(out, png, 0o644); err != nil {
		t.Fatalf("写出失败: %v", err)
	}
	t.Logf("已输出 %s（%d bytes）", out, len(png))
}

// TestRenderCardPNG 播放中 / 暂停两种状态
func TestRenderCardPNG(t *testing.T) {
	np, ld := fakeTrackData()
	writeCardPNG(t, np, ld, "nowplaying.png")

	// 暂停态：进度接近末尾，旋钮靠右、橙色暂停徽章
	np2, ld2 := fakeTrackData()
	np2.Playing = false
	np2.Position = 201000
	writeCardPNG(t, np2, ld2, "nowplaying_paused.png")
}

// TestRenderCardNoLyric 无歌词回退
func TestRenderCardNoLyric(t *testing.T) {
	np, _ := fakeTrackData()
	np.LyricAvailable = false
	writeCardPNG(t, np, nil, "nowplaying_nolyric.png")
}

// TestRenderCardWithCover 带封面路径：预注入封面缓存避免外网依赖，
// 验证封面圆角展示 + 封面模糊背景管线
func TestRenderCardWithCover(t *testing.T) {
	// 程序化生成一张彩色"专辑封面"
	cc := gg.NewContext(600, 600)
	g := gg.NewLinearGradient(0, 0, 600, 600)
	g.AddColorStop(0, color.RGBA{R: 255, G: 138, B: 76, A: 255})
	g.AddColorStop(1, color.RGBA{R: 126, G: 87, B: 194, A: 255})
	cc.SetFillStyle(g)
	cc.DrawRectangle(0, 0, 600, 600)
	cc.Fill()
	cc.SetRGBA255(255, 255, 255, 90)
	cc.DrawCircle(200, 220, 130)
	cc.Fill()
	cc.DrawCircle(400, 380, 170)
	cc.Fill()
	coverCacheMu.Lock()
	coverCache["test-cover|"+apiBase+"/api/cover/x.jpg"] = cc.Image()
	coverCacheMu.Unlock()

	np, ld := fakeTrackData()
	np.Track.ID = "test-cover"
	np.Track.Cover = "/api/cover/x.jpg"
	writeCardPNG(t, np, ld, "nowplaying_cover.png")
}

// TestKaraokeAlphas 逐字点亮程度计算：精确词级 / 匀速回退 / 数据错位回退
func TestKaraokeAlphas(t *testing.T) {
	// 词级时间：position 在第 1 个词中段
	l := &lyricLine{StartTime: 0, EndTime: 10000, Words: []lyricWord{
		{Word: "ab", StartTime: 0, EndTime: 5000},
		{Word: "cd", StartTime: 5000, EndTime: 10000},
	}}
	a := karaokeAlphas(l, []rune("abcd"), 2500)
	if a[0] != 0.5 || a[1] != 0.5 || a[2] != 0 || a[3] != 0 {
		t.Errorf("词级 25%%: got %v", a)
	}
	a = karaokeAlphas(l, []rune("abcd"), 7500)
	if a[0] != 1 || a[1] != 1 || a[2] != 0.5 || a[3] != 0.5 {
		t.Errorf("词级 75%%: got %v", a)
	}
	a = karaokeAlphas(l, []rune("abcd"), 99999)
	if a[0] != 1 || a[3] != 1 {
		t.Errorf("行结束后全亮: got %v", a)
	}
	// 无词级时间 → 行内匀速：prog=0.5 时前半亮
	l2 := &lyricLine{StartTime: 0, EndTime: 10000, Words: []lyricWord{{Word: "abcd"}}}
	a2 := karaokeAlphas(l2, []rune("abcd"), 5000)
	if a2[0] != 1 || a2[1] != 1 || a2[2] != 0 || a2[3] != 0 {
		t.Errorf("匀速 50%%: got %v", a2)
	}
	// 多词带时间但与主歌词 rune 数错位 → 回退匀速
	l3 := &lyricLine{StartTime: 0, EndTime: 10000, Words: []lyricWord{
		{Word: "ab", StartTime: 1000, EndTime: 5000},
		{Word: "c", StartTime: 5000, EndTime: 8000},
	}}
	a3 := karaokeAlphas(l3, []rune("abcd"), 2500)
	if a3[0] != 1 || a3[3] != 0 {
		t.Errorf("错位回退匀速: got %v", a3)
	}
	// 行时间异常（end<=start 且 position>=start）→ 全亮
	l4 := &lyricLine{StartTime: 5000, EndTime: 5000, Words: []lyricWord{{Word: "abcd"}}}
	a4 := karaokeAlphas(l4, []rune("abcd"), 6000)
	for i, v := range a4 {
		if v != 1 {
			t.Errorf("异常行时间 rune%d = %v, want 1", i, v)
		}
	}
}

// TestRenderCardProportional 无词级时间戳的匀速逐字回退渲染
func TestRenderCardProportional(t *testing.T) {
	np, ld := fakeTrackData()
	ld.Lyric[3].Words = []lyricWord{{Word: "当前正在播放的这一句歌词 Current Line"}}
	writeCardPNG(t, np, ld, "nowplaying_proportional.png")
}

// TestCoverURL 封面地址规范化
func TestCoverURL(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"", ""},
		{"http://a/b.jpg", "http://a/b.jpg"},
		{"https://a/b.jpg", "https://a/b.jpg"},
		{"/api/cover/1", apiBase + "/api/cover/1"},
		{"cover/1.jpg", "cover/1.jpg"},
	}
	for _, c := range cases {
		if got := coverURL(c.raw); got != c.want {
			t.Errorf("coverURL(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}
