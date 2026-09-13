package splayer

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/RomiChan/websocket"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// 会话轨迹：外部 API 不暴露真实播放队列，机器人通过 WS 订阅切歌事件，
// 把本会话内播过的曲目按播放顺序记录为列表。「切歌 N」= 连发 next/prev 跳转。
// 顺序播放模式下精确；随机模式下实际队列是重排的，序号可能错位。

const (
	queueMaxLen   = 200 // 轨迹上限，满了停止追加（保持已有序号稳定）
	jumpMaxSteps  = 50  // 单次切歌最大连跳次数
	jumpStepGapMs = 150 // 连跳间隔，给渲染进程记账留时间
	wsReconnect   = 5 * time.Second
)

var (
	queueMu   sync.Mutex
	queue     []track // 会话播放轨迹（按播放顺序）
	queueFull bool    // 轨迹是否已满
)

func init() {
	go wsLoop()
}

// wsEvent WS 下行事件：{kind:"event", type:"track", data:{track:{...}}}
type wsEvent struct {
	Kind string `json:"kind"`
	Type string `json:"type"`
	Data struct {
		Track *track `json:"track"`
	} `json:"data"`
}

// wsLoop 常驻连接 SPlayer WebSocket，断线自动重连
func wsLoop() {
	for {
		wsURL := strings.Replace(apiBase, "http", "ws", 1) + "/ws"
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			time.Sleep(wsReconnect)
			continue
		}
		logrus.Infoln("[splayer] 已连接 SPlayer-Next WebSocket:", wsURL)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				logrus.Infoln("[splayer] SPlayer WebSocket 断开:", err)
				break
			}
			var evt wsEvent
			if json.Unmarshal(raw, &evt) != nil || evt.Kind != "event" || evt.Type != "track" {
				continue
			}
			if evt.Data.Track != nil && evt.Data.Track.ID != "" {
				appendTrack(*evt.Data.Track)
			}
		}
		_ = conn.Close()
		time.Sleep(wsReconnect)
	}
}

// appendTrack 追加曲目到会话轨迹（相邻去重，满员停止追加）
func appendTrack(t track) {
	queueMu.Lock()
	defer queueMu.Unlock()
	if len(queue) > 0 && queue[len(queue)-1].ID == t.ID {
		queue[len(queue)-1] = t
		return
	}
	if len(queue) >= queueMaxLen {
		queueFull = true
		return
	}
	queue = append(queue, t)
}

// syncCurrent 用 now-playing 校准轨迹（补偿 WS 断开期间漏掉的切歌）
func syncCurrent(np *nowPlaying) {
	if np == nil || np.Track == nil || np.Track.ID == "" {
		return
	}
	appendTrack(*np.Track)
}

// queueSnapshot 返回轨迹快照与当前曲目下标（-1 表示未知）
func queueSnapshot() ([]track, int) {
	queueMu.Lock()
	defer queueMu.Unlock()
	snapshot := make([]track, len(queue))
	copy(snapshot, queue)
	return snapshot, len(snapshot) - 1
}

// jumpTo 跳转到轨迹第 n 首（1-based）。先校准当前位置，再连发 next/prev。
func jumpTo(n int) (*track, error) {
	np, err := getNowPlaying()
	if err != nil {
		return nil, err
	}
	syncCurrent(np)
	if np.Track == nil {
		return nil, errors.Errorf("当前没有播放中的曲目")
	}

	queueMu.Lock()
	cur := -1
	for i := range queue {
		if queue[i].ID == np.Track.ID {
			cur = i
			break
		}
	}
	if cur < 0 {
		queueMu.Unlock()
		return nil, errors.Errorf("目标曲目不在会话列表中，请先发送「音乐列表」刷新")
	}
	steps := (n - 1) - cur
	queueMu.Unlock()

	if steps == 0 {
		return np.Track, nil
	}
	if steps > jumpMaxSteps || -steps > jumpMaxSteps {
		return nil, errors.Errorf("距离目标曲目需连跳 %d 次，超过上限 %d", steps, jumpMaxSteps)
	}
	op := "next"
	if steps < 0 {
		op = "prev"
		steps = -steps
	}
	for i := 0; i < steps; i++ {
		if err := playerControl(op); err != nil {
			return nil, errors.Wrapf(err, "第 %d 次跳转失败", i+1)
		}
		time.Sleep(jumpStepGapMs * time.Millisecond)
	}
	final, err := getNowPlaying()
	if err != nil {
		return nil, err
	}
	syncCurrent(final)
	if final.Track == nil {
		return nil, errors.Errorf("跳转后未获取到曲目信息")
	}
	return final.Track, nil
}
