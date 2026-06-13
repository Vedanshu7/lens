const fetchJSON = async <T>(url: string, init?: RequestInit): Promise<T> => {
  const res = await fetch(url, init)
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(text || `HTTP ${res.status}`)
  }
  return res.json() as Promise<T>
}

const get = <T>(url: string) => fetchJSON<T>(url)

const post = <T>(url: string, body: unknown) =>
  fetchJSON<T>(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })

// ---- response types ----

export interface HealthResponse {
  redis: boolean
  target: boolean
  observability: boolean
}

export interface InstanceInfo {
  instance: string
  agentUrl: string
}

export interface KeyEntry {
  keyName: string
  instance: string
  keySchema?: unknown
  ttlInSeconds?: number
  registeredAt?: string
}

export interface InstanceResult {
  instance: string
  success: boolean
  error?: string
}

export interface InvalidateResult {
  service: string
  total: number
  confirmed: number
  instances: InstanceResult[]
}

export interface AuditEntry {
  ts: string
  action: string
  service: string
  pattern?: string | null
  initiator: string
  confirmed: number
  total: number
}

export interface LatencyBucket {
  bucket: string
  p50: number
  p95: number
  p99: number
}

export interface DeadPodEvent {
  ts: string
  peerId: string
  detectionMs: number
}

export interface DiscoveryEvent {
  peerCount: number
  resolutionMs: number
}

export interface FlowStats {
  invalidate: { success: number; partial: number; failure: number }
  fetch: { success: number; failure: number }
  replay: { total: number }
}

export interface ObsSummary {
  totalInvalidations: number
  avgLatencyMs: number
  p99LatencyMs: number
  failureRatePct: number
  deadPodsDetected: number
  peersJoined: number
  peersLeft: number
}

// ---- API surface ----

export const api = {
  health: () => get<HealthResponse>('/api/health'),

  providers: (service: string) =>
    get<{ transport: string; persistence: string; discovery: string; observers: string[] }>(
      `/api/providers?service=${encodeURIComponent(service)}`
    ),

  services: () => get<{ services: string[] }>('/api/services'),

  nodes: (service: string) =>
    get<{ instances: InstanceInfo[] }>(`/api/nodes?service=${encodeURIComponent(service)}`),

  keys: (service: string, instance?: string, pattern?: string) => {
    const p = new URLSearchParams({ service })
    if (instance) p.set('instance', instance)
    if (pattern) p.set('pattern', pattern)
    return get<{ keys: KeyEntry[] | null; failedInstances: string[] | null; source: string }>(
      `/api/keys?${p}`,
    )
  },

  invalidate: (service: string, pattern?: string) =>
    post<InvalidateResult>('/api/invalidate', { service, pattern: pattern || undefined }),

  audit: (limit = 50) =>
    get<{ entries: AuditEntry[]; count: number }>(`/api/audit?limit=${limit}`),

  obs: {
    summary: (service: string, from: string) =>
      get<ObsSummary>(`/api/obs/summary?service=${encodeURIComponent(service)}&from=${encodeURIComponent(from)}`),

    latency: (service: string, from: string) =>
      get<{ buckets: LatencyBucket[] }>(`/api/obs/latency?service=${encodeURIComponent(service)}&from=${encodeURIComponent(from)}`),

    deadpods: (service: string, from: string) =>
      get<{ events: DeadPodEvent[] }>(`/api/obs/deadpods?service=${encodeURIComponent(service)}&from=${encodeURIComponent(from)}`),

    discovery: (from: string) =>
      get<{ events: DiscoveryEvent[] }>(`/api/obs/discovery?from=${encodeURIComponent(from)}`),

    flow: (service: string, from: string) =>
      get<FlowStats>(`/api/obs/flow?service=${encodeURIComponent(service)}&from=${encodeURIComponent(from)}`),
  },
}
