import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Server, ExternalLink, Zap } from 'lucide-react'
import { api } from '../lib/api'
import { InvalidateModal } from '../components/InvalidateModal'
import { PanelHeader, EmptyState, Spinner } from '../components/ui/Panel'
import { shortPod } from '../lib/utils'

interface Props {
  service: string
}

export function NodesPage({ service }: Props) {
  const [showModal, setShowModal] = useState(false)

  const { data, isLoading, refetch } = useQuery({
    queryKey: ['nodes', service],
    queryFn: () => api.nodes(service).then(d => d.instances ?? []),
    enabled: !!service,
    refetchInterval: 10000,
  })

  const nodes = data ?? []

  if (!service) {
    return (
      <div style={{ padding: 40, textAlign: 'center', color: 'var(--muted)', fontSize: 13 }}>
        Select a service from the sidebar to view its nodes.
      </div>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <PanelHeader label={`Nodes — ${service}`} count={nodes.length}>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 6 }}>
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
          <button
            onClick={() => setShowModal(true)}
            style={{
              background: 'var(--accent-dim)', border: '1px solid rgba(124,140,245,0.2)',
              borderRadius: 5, padding: '3px 8px',
              fontSize: 10, color: 'var(--accent)', cursor: 'pointer',
              display: 'flex', alignItems: 'center', gap: 4, fontWeight: 600,
            }}
          >
            <Zap size={10} /> Invalidate All
          </button>
        </div>
      </PanelHeader>

      <div style={{ flex: 1, overflowY: 'auto', padding: 16 }}>
        {isLoading && <Spinner />}

        {!isLoading && nodes.length === 0 && (
          <EmptyState
            icon={<Server size={28} />}
            text={`No live nodes for "${service}".`}
          />
        )}

        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
          {nodes.map(node => (
            <div
              key={node.instance}
              style={{
                background: 'var(--surface)', border: '1px solid var(--border)',
                borderRadius: 10, padding: '14px 16px',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 12 }}>
                <span style={{
                  width: 8, height: 8, borderRadius: '50%',
                  background: 'var(--green)', boxShadow: '0 0 6px var(--green)',
                  flexShrink: 0, animation: 'pulse 2s infinite',
                }} />
                <span style={{ fontSize: 13, fontWeight: 600 }}>{shortPod(node.instance)}</span>
                {node.instance !== shortPod(node.instance) && (
                  <span
                    title={node.instance}
                    style={{ fontSize: 10, color: 'var(--muted)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                  >
                    {node.instance}
                  </span>
                )}
              </div>

              <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 10 }}>
                <span className="mono" style={{ color: 'var(--text-secondary)' }}>{node.agentUrl}</span>
              </div>

              <div style={{ display: 'flex', gap: 6 }}>
                <a
                  href={`${node.agentUrl}/api/health`}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{
                    display: 'flex', alignItems: 'center', gap: 4,
                    fontSize: 10, color: 'var(--muted)',
                    textDecoration: 'none', padding: '3px 8px',
                    border: '1px solid var(--border)', borderRadius: 5,
                    background: 'var(--bg)',
                  }}
                >
                  <ExternalLink size={9} /> Health
                </a>
                <a
                  href={`${node.agentUrl}/metrics`}
                  target="_blank"
                  rel="noopener noreferrer"
                  style={{
                    display: 'flex', alignItems: 'center', gap: 4,
                    fontSize: 10, color: 'var(--muted)',
                    textDecoration: 'none', padding: '3px 8px',
                    border: '1px solid var(--border)', borderRadius: 5,
                    background: 'var(--bg)',
                  }}
                >
                  <ExternalLink size={9} /> Metrics
                </a>
              </div>
            </div>
          ))}
        </div>
      </div>

      {showModal && (
        <InvalidateModal
          defaultService={service}
          onClose={() => setShowModal(false)}
        />
      )}
    </div>
  )
}
