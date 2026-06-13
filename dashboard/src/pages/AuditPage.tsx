import { useQuery } from '@tanstack/react-query'
import { FileText, CheckCircle, AlertCircle } from 'lucide-react'
import { api, AuditEntry } from '../lib/api'
import { PanelHeader, EmptyState, Spinner } from '../components/ui/Panel'
import { shortPod, relativeTime } from '../lib/utils'
import { th, td } from '../components/ui/theme'

export function AuditPage() {
  const { data, isLoading, refetch } = useQuery({
    queryKey: ['audit'],
    queryFn: () => api.audit(100),
    refetchInterval: 10000,
  })

  const entries: AuditEntry[] = data?.entries ?? []

  const statusColor = (e: AuditEntry) => {
    if (e.confirmed === e.total) return 'var(--green)'
    if (e.confirmed === 0) return 'var(--red)'
    return 'var(--yellow)'
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <PanelHeader label="Audit Log" count={entries.length}>
        <div style={{ marginLeft: 'auto' }}>
          <button
            onClick={() => refetch()}
            style={{
              background: 'none', border: '1px solid var(--border)',
              borderRadius: 5, padding: '3px 8px',
              fontSize: 10, color: 'var(--muted)', cursor: 'pointer',
            }}
          >
            Refresh
          </button>
        </div>
      </PanelHeader>

      <div style={{ flex: 1, overflowY: 'auto' }}>
        {isLoading && (
          <div style={{ padding: 20 }}>
            <Spinner />
          </div>
        )}

        {!isLoading && entries.length === 0 && (
          <EmptyState
            icon={<FileText size={28} />}
            text="No audit entries yet. Trigger an invalidation to see events here."
          />
        )}

        {entries.length > 0 && (
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
            <thead>
              <tr>
                {['Time', 'Service', 'Action', 'Initiator', 'Pattern', 'Result'].map(h => (
                  <th key={h} style={th}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {entries.map((entry, i) => (
                <tr
                  key={i}
                  style={{ borderBottom: '1px solid var(--border)', transition: 'background 0.1s' }}
                  onMouseEnter={e => (e.currentTarget as HTMLElement).style.background = 'var(--surface-hover)'}
                  onMouseLeave={e => (e.currentTarget as HTMLElement).style.background = 'transparent'}
                >
                  <td style={{ ...td, color: 'var(--muted)', fontSize: 11, whiteSpace: 'nowrap' }}>
                    <span title={new Date(entry.ts).toLocaleString()}>
                      {relativeTime(entry.ts)}
                    </span>
                  </td>
                  <td style={{ ...td, fontWeight: 500 }}>{entry.service}</td>
                  <td style={{ ...td }}>
                    <span style={{
                      padding: '2px 7px', borderRadius: 4, fontSize: 10, fontWeight: 600,
                      background: 'var(--accent-dim)', color: 'var(--accent)',
                    }}>
                      {entry.action}
                    </span>
                  </td>
                  <td style={{ ...td, color: 'var(--text-secondary)', fontSize: 11 }}>
                    {shortPod(entry.initiator)}
                  </td>
                  <td style={{ ...td, color: 'var(--muted)', fontSize: 11 }}>
                    <span className="mono">{entry.pattern ?? '*'}</span>
                  </td>
                  <td style={td}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      {entry.confirmed === entry.total
                        ? <CheckCircle size={12} style={{ color: 'var(--green)' }} />
                        : <AlertCircle size={12} style={{ color: statusColor(entry) }} />
                      }
                      <span style={{ fontSize: 11, color: statusColor(entry), fontWeight: 600 }}>
                        {entry.confirmed}/{entry.total}
                      </span>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
