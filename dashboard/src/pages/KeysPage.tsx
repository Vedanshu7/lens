import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Key, Search } from 'lucide-react'
import { api, KeyEntry } from '../lib/api'
import { PanelHeader, EmptyState, Spinner } from '../components/ui/Panel'
import { shortPod } from '../lib/utils'
import { input, th, td } from '../components/ui/theme'

interface Props {
  service: string
}

export function KeysPage({ service }: Props) {
  const [pattern, setPattern] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['keys', service, pattern],
    queryFn: () => api.keys(service, undefined, pattern || undefined),
    enabled: !!service,
    refetchInterval: 30000,
  })

  const keys: KeyEntry[] = data?.keys ?? []
  const failed = data?.failedInstances ?? []

  if (!service) {
    return (
      <div style={{ padding: 40, textAlign: 'center', color: 'var(--muted)', fontSize: 13 }}>
        Select a service from the sidebar to view its declared keys.
      </div>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <PanelHeader label={`Key Registry — ${service}`} count={keys.length} />

      <div style={{
        padding: '10px 14px', borderBottom: '1px solid var(--border)',
        background: 'var(--bg-subtle)', display: 'flex', gap: 8, alignItems: 'center',
      }}>
        <Search size={13} style={{ color: 'var(--muted)', flexShrink: 0 }} />
        <input
          value={pattern}
          onChange={e => setPattern(e.target.value)}
          placeholder="Filter by key name…"
          style={{ ...input, padding: '5px 10px', fontSize: 12 }}
        />
      </div>

      {failed.length > 0 && (
        <div style={{
          padding: '7px 14px', fontSize: 11,
          background: 'rgba(239,68,68,0.06)', borderBottom: '1px solid rgba(239,68,68,0.15)',
          color: 'var(--red)',
        }}>
          Failed to load keys from: {failed.map(f => shortPod(f)).join(', ')}
        </div>
      )}

      <div style={{ flex: 1, overflowY: 'auto' }}>
        {isLoading && (
          <div style={{ padding: 20 }}>
            <Spinner />
          </div>
        )}

        {!isLoading && keys.length === 0 && (
          <EmptyState
            icon={<Key size={28} />}
            text={pattern
              ? `No keys match "${pattern}".`
              : 'No declared keys yet.\nCall POST /api/declare from your app to register cache keys.'}
          />
        )}

        {keys.length > 0 && (
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
            <thead>
              <tr>
                {['Key Name', 'Instance', 'TTL', 'Registered'].map(h => (
                  <th key={h} style={th}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {keys.map((k, i) => (
                <tr
                  key={`${k.instance}:${k.keyName}:${i}`}
                  style={{ borderBottom: '1px solid var(--border)' }}
                >
                  <td style={td}>
                    <span className="mono" style={{ color: 'var(--accent)', fontSize: 12 }}>
                      {k.keyName}
                    </span>
                  </td>
                  <td style={{ ...td, color: 'var(--text-secondary)', fontSize: 11 }}>
                    {shortPod(k.instance)}
                  </td>
                  <td style={{ ...td, color: 'var(--muted)', fontSize: 11 }}>
                    {k.ttlInSeconds ? `${k.ttlInSeconds}s` : '-'}
                  </td>
                  <td style={{ ...td, color: 'var(--muted)', fontSize: 11 }}>
                    {k.registeredAt
                      ? new Date(k.registeredAt).toLocaleString()
                      : '-'}
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
