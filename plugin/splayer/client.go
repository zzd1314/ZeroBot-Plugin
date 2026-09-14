// Package splayer 控制 SPlayer-Next 播放器：通过其外部 HTTP API 实现播放控制、
// 播放状态/歌词查询与会话播放列表管理。
// 需在 SPlayer-Next「设置 → 外部 API」中开启并勾选「允许局域网访问」。
// API 文档: https://github.com/SPlayer-Dev/SPlayer-Next/blob/dev/docs/api.md
package splayer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/pkg/errors"
)

// apiBase SPlayer-Next 外部 API 地址，可用环境变量 SPLAYER_API 覆盖
var apiBase = "http://192.168.1.242:14558"

var httpClient = &http.Client{Timeout: 5 * time.Second}

func init() {
	if v := os.Getenv("SPLAYER_API"); v != "" {
		apiBase = v
	}
}

// artist 歌手
type artist struct {
	Name string `json:"name"`
}

// album 专辑
type album struct {
	Name string `json:"name"`
}

// track 曲目信息（外部 API Track 的子集）
type track struct {
	ID       string   `json:"id"`
	Source   string   `json:"source"`
	Title    string   `json:"title"`
	Artists  []artist `json:"artists"`
	Album    album    `json:"album"`
	Duration int64    `json:"duration"` // 毫秒
	Cover    string   `json:"cover"`
	Codec    string   `json:"-"`
	BitRate  int64    `json:"-"`
}

// UnmarshalJSON 从原始 Track 提取常用字段（quality 嵌套在 track 内）
func (t *track) UnmarshalJSON(data []byte) error {
	type alias track
	var a struct {
		alias
		Quality *struct {
			Codec   string `json:"codec"`
			BitRate int64  `json:"bitRate"`
		} `json:"quality"`
	}
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*t = track(a.alias)
	if a.Quality != nil {
		t.Codec = a.Quality.Codec
		t.BitRate = a.Quality.BitRate
	}
	return nil
}

// nowPlaying GET /api/now-playing 响应
type nowPlaying struct {
	Track          *track `json:"track"`
	Position       int64  `json:"position"` // 毫秒
	Playing        bool   `json:"playing"`
	State          string `json:"state"`
	LyricAvailable bool   `json:"lyricAvailable"`
	LyricLineCount int    `json:"lyricLineCount"`
}

// lyricWord 逐字歌词单词（词级时间戳部分歌词源才有，缺失时为 0，
// 渲染端自动回退行内匀速近似）
type lyricWord struct {
	Word      string `json:"word"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
}

// lyricLine 歌词行（时间为毫秒）
type lyricLine struct {
	Words           []lyricWord `json:"words"`
	TranslatedLyric string      `json:"translatedLyric"`
	StartTime       int64       `json:"startTime"`
	EndTime         int64       `json:"endTime"`
}

// lyricData GET /api/lyrics 响应
type lyricData struct {
	TrackID       string      `json:"trackId"`
	Lyric         []lyricLine `json:"lyric"`
	LyricOffsetMs int64       `json:"lyricOffsetMs"`
}

// playerStatus GET /api/status 响应
type playerStatus struct {
	State      string  `json:"state"`
	Position   int64   `json:"position"`
	Duration   int64   `json:"duration"`
	Volume     float64 `json:"volume"`
	IsFinished bool    `json:"isFinished"`
}

// apiGet GET /api/<path> 并解析 JSON 响应
func apiGet(path string, out any) error {
	resp, err := httpClient.Get(apiBase + "/api/" + path)
	if err != nil {
		return errors.Wrapf(err, "无法连接 SPlayer-Next（%s）", apiBase)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("SPlayer API %s 返回 %d: %s", path, resp.StatusCode, truncateBytes(body, 120))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return errors.Wrapf(err, "解析 SPlayer API %s 响应失败", path)
	}
	return nil
}

// apiPost POST /api/<path>，body 可为 nil（控制类接口无请求体）
func apiPost(path string, body any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	resp, err := httpClient.Post(apiBase+"/api/"+path, "application/json", rd)
	if err != nil {
		return errors.Wrapf(err, "无法连接 SPlayer-Next（%s）", apiBase)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("SPlayer API %s 返回 %d: %s", path, resp.StatusCode, truncateBytes(respBody, 120))
	}
	return nil
}

// getNowPlaying 获取当前播放快照（曲目+进度）
func getNowPlaying() (*nowPlaying, error) {
	np := &nowPlaying{}
	if err := apiGet("now-playing", np); err != nil {
		return nil, err
	}
	return np, nil
}

// getLyric 获取当前曲目完整歌词
func getLyric() (*lyricData, error) {
	ld := &lyricData{}
	if err := apiGet("lyrics", ld); err != nil {
		return nil, err
	}
	return ld, nil
}

// getStatus 获取播放状态
func getStatus() (*playerStatus, error) {
	st := &playerStatus{}
	if err := apiGet("status", st); err != nil {
		return nil, err
	}
	return st, nil
}

// playerControl 执行无参控制命令（play/pause/stop/next/prev）
func playerControl(name string) error {
	return apiPost(name, nil)
}

// seekTo 跳转到指定毫秒位置
func seekTo(positionMs int64) error {
	return apiPost("seek", map[string]int64{"positionMs": positionMs})
}

// setVolume 设置音量（0~1）
func setVolume(volume float64) error {
	return apiPost("volume", map[string]float64{"volume": volume})
}

// truncateBytes 截断字节串用于错误信息
func truncateBytes(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "..."
	}
	return string(b)
}

// lineText 歌词行文本：主歌词 + 可选翻译
func (l *lyricLine) lineText() string {
	text := ""
	for _, w := range l.Words {
		text += w.Word
	}
	if l.TranslatedLyric != "" {
		text += "\n" + l.TranslatedLyric
	}
	return text
}

// currentLine 定位播放位置对应的歌词行下标（-1 表示前奏/间奏无歌词）
func (ld *lyricData) currentLine(positionMs int64) int {
	idx := -1
	for i := range ld.Lyric {
		if ld.Lyric[i].StartTime <= positionMs {
			idx = i
		} else {
			break
		}
	}
	return idx
}

// fmtMs 毫秒 → mm:ss
func fmtMs(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	s := ms / 1000
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}
