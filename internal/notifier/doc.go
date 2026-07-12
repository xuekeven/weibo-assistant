// Package notifier 封装企业微信中继服务的 HTTP 调用。
// 自动重试不在这里实现，而由 app 层的 outbox worker 统一调度。
package notifier
