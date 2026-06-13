export const shortPod = (name: string): string => {
  const parts = name.split('-')
  if (parts.length > 2) {
    return parts.slice(-2).join('-')
  }
  return name
}

export const relativeTime = (iso: string): string => {
  const diff = Date.now() - new Date(iso).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

export const fmtMs = (ms: number): string => {
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`
  return `${ms.toFixed(1)}ms`
}
