// Package splayer 控制 SPlayer-Next 播放器（外部 HTTP API）。
// 播放控制、进度/歌词同步、会话播放列表与数字切歌。
package splayer

import (
	"fmt"
	"strconv"
	"strings"

	ctrl "github.com/FloatTech/zbpctrl"
	"github.com/FloatTech/zbputils/control"
	"github.com/sirupsen/logrus"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

const progressWidth = 20

func init() {
	engine := control.AutoRegister(&ctrl.Options[*zero.Ctx]{
		DisableOnDefault: false,
		Brief:            "SPlayer-Next 播放器控制",
		Help: "- 音乐播放 / 音乐暂停 / 音乐停止\n" +
			"- 音乐下一曲 / 音乐上一曲\n" +
			"- 音乐状态（封面+进度条+歌词卡片）\n" +
			"- 音乐歌词（当前曲完整歌词）\n" +
			"- 音乐列表（合并转发本次会话播过的歌）\n" +
			"- 切歌 <序号>（跳到列表第 N 首）\n" +
			"- 音量 <0-100>\n" +
			"- 跳转 <分:秒> 或 <百分比>%\n" +
			"提示: 需在 SPlayer-Next「设置 → 外部 API」开启并允许局域网访问；\n" +
			"「音乐列表/切歌」基于机器人在线期间记录的播放轨迹（顺序播放模式最准确）",
	})

	// 基础控制：音乐播放 / 音乐暂停 / 音乐停止 / 音乐下一曲 / 音乐上一曲
	controls := map[string]string{
		"音乐播放":  "play",
		"音乐暂停":  "pause",
		"音乐停止":  "stop",
		"音乐下一曲": "next",
		"音乐上一曲": "prev",
	}
	for cmd, op := range controls {
		engine.OnFullMatch(cmd).SetBlock(true).
			Handle(func(ctx *zero.Ctx) {
				if err := playerControl(op); err != nil {
					ctx.SendChain(message.Text("操作失败: ", err.Error()))
					return
				}
				ctx.SendChain(message.Text(cmd, " ✓"))
			})
	}

	engine.OnFullMatch("音乐状态").SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			np, err := getNowPlaying()
			if err != nil {
				ctx.SendChain(message.Text("操作失败: ", err.Error()))
				return
			}
			if np.Track == nil {
				ctx.SendChain(message.Text("当前没有播放中的曲目"))
				return
			}
			syncCurrent(np)
			img, err := renderNowPlayingCard(np)
			if err != nil {
				logrus.Warnf("[splayer] 渲染播放卡失败: %v", err)
				if text, err2 := statusText(); err2 == nil {
					ctx.SendChain(message.Text(text))
				}
				return
			}
			ctx.SendChain(message.ImageBytes(img))
		})

	engine.OnFullMatch("音乐歌词").SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			text, err := lyricText()
			if err != nil {
				ctx.SendChain(message.Text("操作失败: ", err.Error()))
				return
			}
			ctx.SendChain(message.Text(text))
		})

	engine.OnFullMatch("音乐列表").SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			np, err := getNowPlaying()
			if err != nil {
				ctx.SendChain(message.Text("操作失败: ", err.Error()))
				return
			}
			syncCurrent(np)
			snapshot, cur := queueSnapshot()
			if len(snapshot) == 0 {
				ctx.SendChain(message.Text("列表为空：机器人在线期间还没有记录到切歌"))
				return
			}
			nickname := ctx.CardOrNickName(ctx.Event.SelfID)
			curLabel := "未知"
			if cur >= 0 {
				curLabel = strconv.Itoa(cur + 1)
			}
			nodes := make(message.Message, 0, len(snapshot)+2)
			nodes = append(nodes, message.CustomNode(nickname, ctx.Event.SelfID,
				fmt.Sprintf("♪ SPlayer-Next 会话播放列表（共 %d 首，当前第 %s 首）", len(snapshot), curLabel)))
			for i, t := range snapshot {
				marker := ""
				if i == cur {
					marker = " ▶"
				}
				nodes = append(nodes, message.CustomNode(nickname, ctx.Event.SelfID,
					fmt.Sprintf("%d. %s%s", i+1, trackText(&t), marker)))
			}
			if queueFull {
				nodes = append(nodes, message.CustomNode(nickname, ctx.Event.SelfID,
					"（列表已达上限，较早的记录不再追加）"))
			}
			ctx.SendGroupForwardMessage(ctx.Event.GroupID, nodes)
		})

	engine.OnRegex(`^切歌\s*(\d+)$`).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			n, _ := strconv.Atoi(ctx.State["regex_matched"].([]string)[1])
			t, err := jumpTo(n)
			if err != nil {
				ctx.SendChain(message.Text("操作失败: ", err.Error()))
				return
			}
			ctx.SendChain(message.Text(fmt.Sprintf("已切到第 %d 首：%s", n, trackText(t))))
		})

	engine.OnRegex(`^音量\s*(\d{1,3})$`).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			n, _ := strconv.Atoi(ctx.State["regex_matched"].([]string)[1])
			if n > 100 {
				ctx.SendChain(message.Text("音量范围 0-100"))
				return
			}
			if err := setVolume(float64(n) / 100); err != nil {
				ctx.SendChain(message.Text("操作失败: ", err.Error()))
				return
			}
			ctx.SendChain(message.Text(fmt.Sprintf("音量已设为 %d%%", n)))
		})

	engine.OnRegex(`^跳转\s*(\d{1,3}):([0-5]?\d)$`).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			m := ctx.State["regex_matched"].([]string)
			min, _ := strconv.Atoi(m[1])
			sec, _ := strconv.Atoi(m[2])
			if err := seekTo(int64(min*60+sec) * 1000); err != nil {
				ctx.SendChain(message.Text("操作失败: ", err.Error()))
				return
			}
			ctx.SendChain(message.Text(fmt.Sprintf("已跳转到 %02d:%02d", min, sec)))
		})

	engine.OnRegex(`^跳转\s*(\d{1,3})\s*%$`).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			n, _ := strconv.Atoi(ctx.State["regex_matched"].([]string)[1])
			if n > 100 {
				ctx.SendChain(message.Text("百分比范围 0-100"))
				return
			}
			dur := int64(0)
			if np, err := getNowPlaying(); err == nil && np.Track != nil {
				dur = np.Track.Duration
			}
			if dur <= 0 {
				if st, err := getStatus(); err == nil {
					dur = st.Duration
				}
			}
			if dur <= 0 {
				ctx.SendChain(message.Text("无法获取歌曲时长，请稍后重试"))
				return
			}
			pos := dur * int64(n) / 100
			if err := seekTo(pos); err != nil {
				ctx.SendChain(message.Text("操作失败: ", err.Error()))
				return
			}
			ctx.SendChain(message.Text(fmt.Sprintf("已跳转到 %d%%（%s）", n, fmtMs(pos))))
		})

	engine.OnRegex(`^跳转\s*(\d+)$`).SetBlock(true).
		Handle(func(ctx *zero.Ctx) {
			sec, _ := strconv.Atoi(ctx.State["regex_matched"].([]string)[1])
			if err := seekTo(int64(sec) * 1000); err != nil {
				ctx.SendChain(message.Text("操作失败: ", err.Error()))
				return
			}
			ctx.SendChain(message.Text(fmt.Sprintf("已跳转到 %s", fmtMs(int64(sec)*1000))))
		})
}

// trackText 曲目单行文本：标题 - 歌手 [时长]
func trackText(t *track) string {
	var b strings.Builder
	b.WriteString(t.Title)
	if len(t.Artists) > 0 {
		names := make([]string, 0, len(t.Artists))
		for _, a := range t.Artists {
			names = append(names, a.Name)
		}
		b.WriteString(" - ")
		b.WriteString(strings.Join(names, "/"))
	}
	if t.Duration > 0 {
		b.WriteString(" [")
		b.WriteString(fmtMs(t.Duration))
		b.WriteString("]")
	}
	return b.String()
}

// progressBar 文本进度条：━━━━●────
func progressBar(positionMs, durationMs int64, width int) string {
	if durationMs <= 0 {
		return strings.Repeat("─", width)
	}
	filled := positionMs * int64(width) / durationMs
	if filled < 0 {
		filled = 0
	}
	if filled >= int64(width) {
		filled = int64(width) - 1
	}
	return strings.Repeat("━", int(filled)) + "●" + strings.Repeat("─", width-1-int(filled))
}

// statusText 音乐状态：曲目信息 + 进度条 + 当前行歌词
func statusText() (string, error) {
	np, err := getNowPlaying()
	if err != nil {
		return "", err
	}
	if np.Track == nil {
		return "当前没有播放中的曲目", nil
	}
	var b strings.Builder
	stateIcon := "⏸"
	if np.Playing {
		stateIcon = "▶"
	}
	b.WriteString(stateIcon + " " + trackText(np.Track) + "\n")
	if np.Track.Album.Name != "" {
		b.WriteString("专辑: " + np.Track.Album.Name)
		if np.Track.Codec != "" {
			b.WriteString(" | " + strings.ToUpper(np.Track.Codec))
			if np.Track.BitRate > 0 {
				b.WriteString(" " + strconv.FormatInt(np.Track.BitRate/1000, 10) + "kbps")
			}
		}
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("%s %s %s / %s\n",
		fmtMs(np.Position), progressBar(np.Position, np.Track.Duration, progressWidth), stateIcon, fmtMs(np.Track.Duration)))
	// 当前行歌词
	if np.LyricAvailable {
		if ld, err := getLyric(); err == nil && ld.TrackID == np.Track.ID {
			if idx := ld.currentLine(np.Position); idx >= 0 {
				b.WriteString("♪ " + strings.ReplaceAll(ld.Lyric[idx].lineText(), "\n", " / "))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// lyricText 当前曲完整歌词，当前行前加 ▶；超长时截取当前行附近 ±30 行
func lyricText() (string, error) {
	np, err := getNowPlaying()
	if err != nil {
		return "", err
	}
	if np.Track == nil {
		return "当前没有播放中的曲目", nil
	}
	if !np.LyricAvailable {
		return "当前曲目没有可用歌词", nil
	}
	ld, err := getLyric()
	if err != nil {
		return "", err
	}
	if ld.TrackID != np.Track.ID {
		return "歌词与当前曲目不匹配，请稍后重试", nil
	}
	cur := ld.currentLine(np.Position)
	var b strings.Builder
	b.WriteString("♪ " + trackText(np.Track) + "\n\n")
	// 歌词很长时只保留当前行附近窗口，避免刷屏
	start, end := 0, len(ld.Lyric)
	const window = 30
	if cur >= 0 && len(ld.Lyric) > window*2+1 {
		start = cur - window
		if start < 0 {
			start = 0
		}
		end = cur + window + 1
		if end > len(ld.Lyric) {
			end = len(ld.Lyric)
		}
		if start > 0 {
			b.WriteString("……\n")
		}
	}
	for i := start; i < end; i++ {
		if i == cur {
			b.WriteString("▶ ")
		} else {
			b.WriteString("　 ")
		}
		b.WriteString(strings.ReplaceAll(ld.Lyric[i].lineText(), "\n", " / "))
		b.WriteString("\n")
	}
	if end < len(ld.Lyric) {
		b.WriteString("……\n")
	}
	if cur >= 0 {
		b.WriteString(fmt.Sprintf("\n当前进度 %s / %s", fmtMs(np.Position), fmtMs(np.Track.Duration)))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
