// Package servicemenu 插件用法卡片：「/用法 <英文名>」查看插件详细使用说明。
//
// 命令解析兼容：/用法 xxx、！用法 xxx、用法 xxx、全角空格、无空格、
// 大小写混写，以及旧命令名「菜单用法 / menuusage」。
// 详细用法按 功能介绍 / 操作步骤 / 参数说明 / 使用示例 四个分区渲染；
// 未收录详细用法的插件回退展示 Brief + Help。
package servicemenu

import (
	"strings"

	"github.com/FloatTech/zbputils/control"
	zero "github.com/wdvxdr1123/ZeroBot"
	"github.com/wdvxdr1123/ZeroBot/message"
)

// usageCmdPattern 「/用法 <英文名>」命令正则：
//   - 可选前缀 / ！ !（ZeroBot OnRegex 直接匹配原始消息文本）
//   - 命令名：用法 / 菜单用法 / menuusage
//   - 分隔：\s 不含全角空格，故显式加入　；允许无空格直连（用法splayer）
//   - 参数：(\S+) 插件英文名（处理时统一 ToLower）
const usageCmdPattern = `^(?:[/！!]?用法|菜单用法|menuusage)[\s　]*(\S+)$`

// usageSection 用法卡片分区：标题 + 正文（\n 分行，行宽超限时自动折行）
type usageSection struct {
	Title string
	Body  string
}

// usageDetails 新插件的详细用法（功能介绍/操作步骤/参数说明/使用示例）。
// key 为插件英文名（小写）。未收录的插件回退 Brief + Help。
var usageDetails = map[string][]usageSection{
	"hyperv": {
		{Title: "功能介绍", Body: "通过宿主机 PowerShell 管理 Hyper-V 虚拟机：开关机、保存/暂停/恢复、" +
			"强制断电、屏幕截图与检查点管理，相当于命令版 Hyper-V 管理器。仅超级用户可用。"},
		{Title: "操作步骤", Body: "1. 确认机器人以管理员权限运行，宿主机已启用 Hyper-V\n" +
			"2. 发送「虚拟机列表」获取虚拟机名称\n" +
			"3. 用下列命令对指定虚拟机执行操作"},
		{Title: "参数说明", Body: "<名称> = 虚拟机名（以「虚拟机列表」显示为准）\n" +
			"<检查点名> = 检查点的自定义名称"},
		{Title: "使用示例", Body: "虚拟机列表\n" +
			"虚拟机状态 win-test\n" +
			"虚拟机开机 win-test\n" +
			"虚拟机屏幕 win-test\n" +
			"创建检查点 win-test 干净系统\n" +
			"还原检查点 win-test 干净系统"},
	},
	"splayer": {
		{Title: "功能介绍", Body: "控制 SPlayer-Next 音乐播放器（外部 HTTP API）：播放/暂停/切曲、" +
			"音量与进度调节、歌词同步显示、会话播放列表合并转发。"},
		{Title: "操作步骤", Body: "1. 在 SPlayer-Next「设置 → 外部 API」开启并允许局域网访问\n" +
			"2. 直接发送播放控制命令（无需 @机器人）\n" +
			"3. 「音乐列表/切歌」基于机器人在线期间记录的播放轨迹"},
		{Title: "参数说明", Body: "切歌 <序号> = 会话播放列表中的歌曲序号\n" +
			"音量 <0-100>，跳转 <分:秒> 或 <秒数>"},
		{Title: "使用示例", Body: "音乐播放\n" +
			"音乐状态\n" +
			"音乐歌词\n" +
			"音乐列表\n" +
			"切歌 3\n" +
			"音量 60\n" +
			"跳转 1:30"},
	},
}

func init() {
	zero.OnRegex(usageCmdPattern).SetBlock(true).FirstPriority().
		Handle(func(ctx *zero.Ctx) {
			name := strings.ToLower(strings.TrimSpace(ctx.State["regex_matched"].([]string)[1]))
			m, ok := control.Lookup(name)
			if !ok {
				ctx.SendChain(message.Text("没有找到插件: ", name, "，可先发送「服务列表」查看全部插件英文名"))
				return
			}
			img, err := renderUsageCard(m)
			if err != nil {
				ctx.SendChain(message.Text("渲染失败: ", err))
				return
			}
			ctx.SendChain(message.ImageBytes(img))
		})
}
