import { onUnmounted, ref } from 'vue'
import type { RealtimeEvent } from '../api'

export type RealtimeStatus = 'connecting' | 'connected' | 'disconnected' | 'error'

export function useRealtimeConnection(
  createSocket: () => WebSocket,
  onEvent: (event: RealtimeEvent) => void,
  onDisconnected: () => void
) {
  const status = ref<RealtimeStatus>('connecting')
  let socket: WebSocket | undefined
  let reconnectTimer: number | undefined
  let stopped = false

  function connect() {
    if (stopped) return
    if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) return

    status.value = 'connecting'
    socket = createSocket()
    socket.addEventListener('open', () => {
      status.value = 'connected'
    })
    socket.addEventListener('message', (event) => {
      try {
        onEvent(JSON.parse(event.data) as RealtimeEvent)
      } catch (err) {
        console.warn('parse realtime event failed', err)
      }
    })
    socket.addEventListener('close', () => {
      status.value = 'disconnected'
      socket = undefined
      if (stopped) return
      onDisconnected()
      reconnectTimer = window.setTimeout(connect, 3000)
    })
    socket.addEventListener('error', () => {
      status.value = 'error'
    })
  }

  function stop() {
    stopped = true
    if (reconnectTimer) window.clearTimeout(reconnectTimer)
    socket?.close()
    socket = undefined
  }

  onUnmounted(stop)

  return { status, connect, stop }
}
