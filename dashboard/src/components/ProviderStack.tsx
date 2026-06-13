import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'

const COLORS: Record<string, { bg: string; color: string }> = {
  // transport
  grpc:        { bg: 'rgba(124,140,245,0.15)', color: '#7c8cf5' },
  nats:        { bg: 'rgba(34,197,94,0.15)',   color: '#22c55e' },
  kafka:       { bg: 'rgba(245,158,11,0.15)',  color: '#f59e0b' },
  zmq:         { bg: 'rgba(168,85,247,0.15)',  color: '#a855f7' },
  // persistence
  redis:       { bg: 'rgba(239,68,68,0.12)',   color: '#ef4444' },
  memory:      { bg: 'rgba(148,163,184,0.15)', color: '#94a3b8' },
  // discovery
  memberlist:  { bg: 'rgba(251,146,60,0.15)',  color: '#fb923c' },
  static:      { bg: 'rgba(148,163,184,0.15)', color: '#94a3b8' },
  dnssrv:      { bg: 'rgba(52,211,153,0.15)',  color: '#34d399' },
  // observers
  sql:         { bg: 'rgba(96,165,250,0.15)',  color: '#60a5fa' },
  prometheus:  { bg: 'rgba(251,191,36,0.15)',  color: '#fbbf24' },
  stdout:      { bg: 'rgba(148,163,184,0.15)', color: '#94a3b8' },
  webhook:     { bg: 'rgba(244,114,182,0.15)', color: '#f472b6' },
}

function Badge({ label, value }: { label: string; value: string }) {
  const c = COLORS[value] ?? { bg: 'rgba(148,163,184,0.12)', color: '#94a3b8' }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
      <span style={{ fontSize: 9, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.06em', color: 'var(--muted)' }}>
        {label}
      </span>
      <span style={{
        fontSize: 10, fontWeight: 700,
        padding: '2px 7px', borderRadius: 4,
        background: c.bg, color: c.color,
        letterSpacing: '0.02em',
      }}>
        {value}
      </span>
    </div>
  )
}

interface Props {
  service: string
  compact?: boolean
}

export function ProviderStack({ service, compact = false }: Props) {
  const { data } = useQuery({
    queryKey: ['providers', service],
    queryFn: () => api.providers(service),
    enabled: !!service,
    staleTime: 60_000,
  })

  if (!data) return null

  const gap = compact ? 8 : 14

  return (
    <div style={{
      display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap,
      padding: compact ? '6px 10px' : '10px 14px',
      background: compact ? 'transparent' : 'var(--bg-subtle)',
      borderBottom: compact ? 'none' : '1px solid var(--border)',
    }}>
      <Badge label="transport"   value={data.transport} />
      {!compact && <Divider />}
      <Badge label="persistence" value={data.persistence} />
      {!compact && <Divider />}
      <Badge label="discovery"   value={data.discovery} />
      {(data.observers ?? []).length > 0 && (
        <>
          {!compact && <Divider />}
          <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
            <span style={{ fontSize: 9, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.06em', color: 'var(--muted)' }}>
              observers
            </span>
            {(data.observers ?? []).map(o => {
              const c = COLORS[o] ?? { bg: 'rgba(148,163,184,0.12)', color: '#94a3b8' }
              return (
                <span key={o} style={{
                  fontSize: 10, fontWeight: 700,
                  padding: '2px 7px', borderRadius: 4,
                  background: c.bg, color: c.color,
                }}>
                  {o}
                </span>
              )
            })}
          </div>
        </>
      )}
    </div>
  )
}

function Divider() {
  return <span style={{ width: 1, height: 12, background: 'var(--border)', flexShrink: 0 }} />
}
