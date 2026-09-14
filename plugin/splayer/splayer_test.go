package splayer

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestAPIConnectivity 实测外部 API 连通性与各端点（播放器未开时 Skip）
func TestAPIConnectivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = ctx

	np, err := getNowPlaying()
	if err != nil {
		t.Skipf("SPlayer-Next API 不可达: %v", err)
	}
	t.Logf("state=%s playing=%v", np.State, np.Playing)
	if np.Track != nil {
		t.Logf("now: %s", trackText(np.Track))
		t.Logf("codec=%s bitrate=%d cover=%s", np.Track.Codec, np.Track.BitRate, np.Track.Cover)
	}

	ld, err := getLyric()
	if err != nil {
		t.Skipf("歌词接口不可用: %v", err)
	}
	t.Logf("lyric lines=%d trackId=%s", len(ld.Lyric), ld.TrackID)
	if len(ld.Lyric) > 0 {
		t.Logf("line0: %s", strings.ReplaceAll(ld.Lyric[0].lineText(), "\n", " / "))
	}

	statusText, err := statusText()
	if err != nil {
		t.Skipf("statusText 失败: %v", err)
	}
	t.Logf("statusText:\n%s", statusText)
}

// TestLyricCurrentLine 验证歌词行定位
func TestLyricCurrentLine(t *testing.T) {
	ld := &lyricData{Lyric: []lyricLine{
		{StartTime: 0, EndTime: 5000, Words: []lyricWord{{Word: "a"}}},
		{StartTime: 5000, EndTime: 10000, Words: []lyricWord{{Word: "b"}}},
		{StartTime: 10000, EndTime: 15000, Words: []lyricWord{{Word: "c"}}},
	}}
	cases := []struct {
		pos  int64
		want int
	}{
		{0, 0},
		{4999, 0},
		{5000, 1},
		{9999, 1},
		{12000, 2},
		{99999, 2}, // 超出末尾落在最后一行
	}
	for _, c := range cases {
		if got := ld.currentLine(c.pos); got != c.want {
			t.Errorf("currentLine(%d) = %d, want %d", c.pos, got, c.want)
		}
	}
}

// TestProgressBar 验证进度条渲染
func TestProgressBar(t *testing.T) {
	if got := progressBar(0, 100, 10); got != "●─────────" {
		t.Errorf("progressBar(0) = %q", got)
	}
	if got := progressBar(100, 100, 10); got != "━━━━━━━━━●" {
		t.Errorf("progressBar(100) = %q", got)
	}
	if got := progressBar(50, 100, 10); got != "━━━━━●────" {
		t.Errorf("progressBar(50) = %q", got)
	}
	if got := progressBar(1, 0, 5); got != "─────" {
		t.Errorf("progressBar(total=0) = %q", got)
	}
}
