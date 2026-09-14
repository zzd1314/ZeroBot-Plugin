// badge.go 服务列表红点徽章：插件首次出现时自动打标（类手机 app 角标），
// 持久化到 data/servicemenu/badges.json，可通过「清除红点」命令手动清除。
//
// 自动打标规则：
//   - 存储文件不存在（首次运行）：当前全部插件视为"已见"不打标，
//     仅对 launchBadges 里本版本新增的功能播种红点；
//   - 之后每次进程启动对比当前插件集合与 seen 记录，新出现的插件自动打标；
//   - 已消失的插件从 seen/badges 中清理，红点不残留。
package servicemenu

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

// launchBadges 本版本新增功能的红点首发标记。仅在存储文件首次初始化时生效，
// 之后的版本无需维护：新插件出现时会相对 seen 记录自动打标。
var launchBadges = []string{"hyperv", "splayer"}

// badgePath 徽章持久化文件（相对机器人工作目录）。测试中可替换到临时目录。
var badgePath = filepath.Join("data", "servicemenu", "badges.json")

// badgeStore 徽章存储结构：
//   - seen：出现过的插件名 → 最近一次出现时间（unix 秒），防止重复打标
//   - badges：当前带红点的插件名 → 打标时间（unix 秒）
type badgeStore struct {
	Seen   map[string]int64 `json:"seen"`
	Badges map[string]int64 `json:"badges"`
}

var badgeMu sync.Mutex

// badgeDetectOnce 每个进程只做一次新插件检测（插件集合在编译期固定）
var badgeDetectOnce sync.Once

// readStore 读取存储文件。返回 (存储, 是否首次运行)。
// 文件不存在或解析失败均视为首次运行（返回空存储），不视为错误。
func readStore() (*badgeStore, bool) {
	b, err := os.ReadFile(badgePath)
	if err != nil {
		return &badgeStore{Seen: map[string]int64{}, Badges: map[string]int64{}}, true
	}
	var s badgeStore
	if err := json.Unmarshal(b, &s); err != nil {
		return &badgeStore{Seen: map[string]int64{}, Badges: map[string]int64{}}, true
	}
	if s.Seen == nil {
		s.Seen = map[string]int64{}
	}
	if s.Badges == nil {
		s.Badges = map[string]int64{}
	}
	return &s, false
}

// saveStore 落盘（调用方须持有 badgeMu）
func saveStore(s *badgeStore) error {
	if err := os.MkdirAll(filepath.Dir(badgePath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(badgePath, b, 0o644)
}

// refreshBadges 对比当前插件集合与存储：新出现的插件自动打红点，
// 已消失的插件清理出 seen/badges，最后落盘。names 为全部插件英文名（小写）。
func refreshBadges(names []string) {
	badgeMu.Lock()
	defer badgeMu.Unlock()

	s, firstRun := readStore()
	now := time.Now().Unix()

	if firstRun {
		// 首次运行：当前插件全部记入 seen（不打标），
		// 仅对本版本新增功能播种红点
		s.Seen = map[string]int64{}
		s.Badges = map[string]int64{}
		for _, n := range names {
			s.Seen[n] = now
		}
		for _, n := range launchBadges {
			s.Badges[n] = now
		}
	} else {
		for _, n := range names {
			if _, seen := s.Seen[n]; !seen {
				// 首次出现的新插件 → 自动打标
				s.Badges[n] = now
			}
			s.Seen[n] = now
		}
		// 清理已消失的插件
		current := make(map[string]bool, len(names))
		for _, n := range names {
			current[n] = true
		}
		for n := range s.Seen {
			if !current[n] {
				delete(s.Seen, n)
			}
		}
		for n := range s.Badges {
			if !current[n] {
				delete(s.Badges, n)
			}
		}
	}

	if err := saveStore(s); err != nil {
		// 落盘失败只影响下次启动的"已见"记录，不阻断渲染
		logrus.Warnf("[servicemenu] 保存红点徽章失败: %v", err)
	}
}

// badgedSet 返回当前带红点的插件名集合（渲染用，副本）
func badgedSet() map[string]bool {
	badgeMu.Lock()
	defer badgeMu.Unlock()
	s, _ := readStore()
	out := make(map[string]bool, len(s.Badges))
	for n := range s.Badges {
		out[strings.ToLower(n)] = true
	}
	return out
}

// clearBadges 清除红点：name 为空清除全部，否则只清指定插件（大小写不敏感）。
// 返回实际清除的数量。
func clearBadges(name string) int {
	badgeMu.Lock()
	defer badgeMu.Unlock()

	s, firstRun := readStore()
	if firstRun {
		return 0
	}
	cleared := 0
	if name == "" {
		cleared = len(s.Badges)
		s.Badges = map[string]int64{}
	} else {
		n := strings.ToLower(name)
		if _, ok := s.Badges[n]; ok {
			delete(s.Badges, n)
			cleared = 1
		}
	}
	if cleared > 0 {
		if err := saveStore(s); err != nil {
			logrus.Warnf("[servicemenu] 保存红点徽章失败: %v", err)
			return 0
		}
	}
	return cleared
}

func init() {
	// 清除红点：「清除红点」清全部，「清除红点 <插件名>」清单个
	// （\s 不含全角空格，故显式加入　）
	zero.OnRegex(`^(?:清除红点|clearbadge)(?:[\s　]+(\S+))?$`).SetBlock(true).FirstPriority().
		Handle(func(ctx *zero.Ctx) {
			var name string
			if m := ctx.State["regex_matched"].([]string); len(m) > 1 {
				name = strings.TrimSpace(m[1])
			}
			n := clearBadges(name)
			switch {
			case name != "" && n == 1:
				ctx.SendChain(message.Text("已清除 ", name, " 的红点"))
			case name != "":
				ctx.SendChain(message.Text(name, " 没有红点"))
			case n == 0:
				ctx.SendChain(message.Text("当前没有红点"))
			default:
				ctx.SendChain(message.Text("已清除 ", itoa(n), " 个红点"))
			}
		})
}
