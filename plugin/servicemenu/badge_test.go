package servicemenu

// 单元测试：红点徽章生命周期 / 「/用法」命令解析 / 红点渲染像素验证
// 运行: go test ./plugin/servicemenu -run 'TestBadge|TestUsage'

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/FloatTech/gg"
)

// withTempBadgePath 把徽章存储指到临时目录，测试互不污染
func withTempBadgePath(t *testing.T) {
	t.Helper()
	old := badgePath
	badgePath = filepath.Join(t.TempDir(), "badges.json")
	t.Cleanup(func() { badgePath = old })
}

// TestBadgeLifecycle 首次运行播种 → 新插件自动打标 → 消失清理 → 手动清除 → 不复活
func TestBadgeLifecycle(t *testing.T) {
	withTempBadgePath(t)

	// 1. 首次运行：launchBadges（hyperv/splayer）打标，存量插件不打标
	refreshBadges([]string{"job", "chat", "hyperv", "splayer"})
	s := badgedSet()
	if !s["hyperv"] || !s["splayer"] {
		t.Fatalf("首发新插件应有红点: %v", s)
	}
	if s["job"] || s["chat"] {
		t.Fatalf("存量插件不应打标: %v", s)
	}

	// 2. 之后出现新插件 → 自动打标
	refreshBadges([]string{"job", "chat", "hyperv", "splayer", "newplug"})
	s = badgedSet()
	if !s["newplug"] {
		t.Fatalf("新出现的插件 newplug 应自动打标: %v", s)
	}

	// 3. 插件消失 → 红点与 seen 一并清理
	refreshBadges([]string{"job", "hyperv", "splayer"})
	s = badgedSet()
	if s["newplug"] {
		t.Fatalf("已消失插件的红点应被清理: %v", s)
	}

	// 4. 手动清除指定
	if n := clearBadges("HYPERV"); n != 1 { // 大小写不敏感
		t.Fatalf("清除 hyperv 应返回 1, got %d", n)
	}
	if badgedSet()["hyperv"] {
		t.Fatal("清除后 hyperv 不应再有红点")
	}

	// 5. 清除不存在的红点
	if n := clearBadges("hyperv"); n != 0 {
		t.Fatalf("重复清除应返回 0, got %d", n)
	}

	// 6. 清除全部
	if n := clearBadges(""); n != 1 { // 只剩 splayer
		t.Fatalf("清除全部应返回 1, got %d", n)
	}
	if len(badgedSet()) != 0 {
		t.Fatalf("清除全部后不应有红点: %v", badgedSet())
	}

	// 7. 持久化验证：清除后重新检测，已清除的红点不复活
	refreshBadges([]string{"job", "hyperv", "splayer"})
	s = badgedSet()
	if s["hyperv"] || s["splayer"] {
		t.Fatalf("已清除的红点不应复活: %v", s)
	}
}

// TestBadgeCorruptStore 存储损坏时视为首次运行，按 launchBadges 重建
func TestBadgeCorruptStore(t *testing.T) {
	withTempBadgePath(t)
	refreshBadges([]string{"a", "hyperv"}) // 生成正常文件
	// 写入坏内容
	if err := os.WriteFile(badgePath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	clearBadges("") // 应安全返回
	refreshBadges([]string{"a", "hyperv", "splayer"})
	s := badgedSet()
	if !s["hyperv"] || !s["splayer"] {
		t.Fatalf("损坏存储应按首次运行重建: %v", s)
	}
}

// TestUsageDetailsForNewPlugins 新插件必须有四分区详细用法
func TestUsageDetailsForNewPlugins(t *testing.T) {
	for _, name := range []string{"hyperv", "splayer"} {
		ss, ok := usageDetails[name]
		if !ok {
			t.Fatalf("usageDetails 缺少 %s", name)
		}
		if len(ss) != 4 {
			t.Fatalf("%s 详细用法应有 4 个分区, got %d", name, len(ss))
		}
		for i, want := range []string{"功能介绍", "操作步骤", "参数说明", "使用示例"} {
			if ss[i].Title != want {
				t.Fatalf("%s 分区 %d 标题应为 %s, got %s", name, i, want, ss[i].Title)
			}
			if len([]rune(ss[i].Body)) < 8 {
				t.Fatalf("%s 分区 %s 正文过短", name, want)
			}
		}
	}
}

// TestUsageCommandRegex 「/用法」命令解析：中英文空格 / 斜杠 / 无空格 / 旧命令名
func TestUsageCommandRegex(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/用法 splayer", "splayer"},
		{"用法 splayer", "splayer"},
		{"/用法hyperv", "hyperv"},    // 无空格直连
		{"用法　hyperv", "hyperv"},    // 全角空格
		{"用法  splayer", "splayer"}, // 多个半角空格
		{"！用法 SPlayer", "SPlayer"}, // 全角感叹号 + 大写（处理时统一 ToLower）
		{"!用法 splayer", "splayer"},
		{"菜单用法 aiimage", "aiimage"},
		{"menuusage job", "job"},
		{"menuusage  chat", "chat"},
	}
	for _, tc := range cases {
		m := usageNameRe.FindStringSubmatch(tc.in)
		if m == nil {
			t.Fatalf("%q 应匹配", tc.in)
		}
		if m[1] != tc.want {
			t.Fatalf("%q 捕获应为 %q, got %q", tc.in, tc.want, m[1])
		}
	}
	for _, in := range []string{"用法", "菜单用法", "服务列表", "主题 glass-dark", "清除红点"} {
		if usageNameRe.MatchString(in) {
			t.Fatalf("%q 不应匹配", in)
		}
	}
}

// TestBadgeDotRendering 红点像素验证：hasBadge=true 时卡片右上角出现 iOS 红，
// false 时同区域无红色
func TestBadgeDotRendering(t *testing.T) {
	tt := themes[0]
	currentTheme = tt
	const w, h = 514, 120

	renderCard := func(hasBadge bool) int {
		c := gg.NewContext(w+60, h+60)
		c.SetRGBA255(90, 90, 110, 255)
		c.DrawRectangle(0, 0, float64(w+60), float64(h+60))
		c.Fill()
		drawPluginCardContent(c, 30, 30, w, h, "splayer", "SPlayer-Next 播放器控制", true, hasBadge, tt)
		red := 0
		// 扫描卡片右上角区域（红点中心在 x+w-20, y+40 局部坐标）
		for py := 30; py < 30+34; py++ {
			for px := 30 + w - 44; px < 30+w+6; px++ {
				r, g, b, a := c.Image().At(px, py).RGBA()
				if a>>8 > 200 && r>>8 > 200 && g>>8 < 110 && b>>8 < 110 {
					red++
				}
			}
		}
		return red
	}

	if red := renderCard(true); red < 50 {
		t.Fatalf("红点未渲染: 红像素 %d", red)
	}
	if red := renderCard(false); red != 0 {
		t.Fatalf("无徽章时不应有红像素: %d", red)
	}
}
