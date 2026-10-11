export function shouldAdvanceGroupPreview(currentTime?: string, incomingTime?: string): boolean {
  if (!incomingTime) return false
  const incoming = Date.parse(incomingTime)
  if (!Number.isFinite(incoming)) return false
  if (!currentTime) return true
  const current = Date.parse(currentTime)
  return !Number.isFinite(current) || incoming > current
}
