// Package browser 负责启动 Chromium，并通过 CDP（Chrome DevTools Protocol）
// 观察微博聊天页的网络请求和页面状态。
//
// 这个包只负责“采集”，采集到消息后通过 MonitorOptions 中的回调交给 app 包处理。
package browser
