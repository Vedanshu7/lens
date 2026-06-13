import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Activity, CheckCircle, XCircle, Zap, Database, Radio } from 'lucide-react'
import { api, HealthResponse } from '../lib/api'
import { InvalidateModal } from '../components/InvalidateModal'
import { PanelHeader, EmptyState, Spinner } from '../components/ui/Panel'
import { ProviderStack } from '../components/ProviderStack'

interface Props {
  selectedService: string
  onSelectService: (svc: string) => void
}

function HealthDot({ ok }: { ok: boolean }) {
  return (
    <span style={{
      display: 'inline-block', width: 7, height: 7, borderRadius: '50%',
      background: ok ? 'var(--green)' : 'var(--red)',
      boxShadow: ok ? '0 0 6px var(--green)' : '0 0 6px var(--red)',
    }} />
  )
}

function HealthBar({ health }: { health: HealthResponse }) {
  const items = [
    { icon: Database, label: 'Store', ok: health.redis },
    { icon: Radio, label: 'Target', ok: health.target },
    { icon: Activity, label: 'Observability', ok: health.observability },
  ]
  return (
    <div style={{
      display: 'flex', gap: 8, flexWrap: 'wrap',
      padding: '10px 14px', borderBottom: '1px solid var(--border)',
      background: 'var(--bg-subtle)',
    }}>
      {items.map(({ icon: Icon, label, ok }) => (
        <div key={label} style={{
          display: 'flex', alignItems: 'center', gap: 6,
          padding: '4px 10px', borderRadius: 6,
          background: 'var(--surface)', border: '1px solid var(--border)',
          fontSize: 11, color: 'var(--text-secondary)',
        }}>
          <HealthDot ok={ok} />
          <Icon size={11} style={{ opacity: 0.5 }} />
          {label}
        </div>
      ))}
    </div>
  )
}

export function ServicesPage({ selectedService, onSelectService }: Props) {
  const [showModal, setShowModal] = useState(false)
  const [modalService, setModalService] = useState('')

  const { data: health } = useQuery({
    queryKey: ['health'],
    queryFn: () => api.health(),
    refetchInterval: 10000,
  })

  const { data, isLoading } = useQuery({
    queryKey: ['services'],
    queryFn: () => api.services().then(d => d.services ?? []),
    refetchInterval: 15000,
  })

  const services = data ?? []

  const openInvalidate = (svc: string, e: React.MouseEvent) => {
    e.stopPropagation()
    setModalService(svc)
    setShowModal(true)
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      {health && <HealthBar health={health} />}

      <PanelHeader label="Services" count={services.length} />

      <div style={{ flex: 1, overflowY: 'auto' }}>
        {isLoading && (
          <div style={{ padding: 20 }}>
            <Spinner />
          </div>
        )}

        {!isLoading && services.length === 0 && (
          <EmptyState
            icon={<Activity size={28} />}
            text={'No services registered yet.\nStart the example app to see services appear here.'}
          />
        )}

        {services.map(svc => (
          <div
            key={svc}
            onClick={() => onSelectService(svc)}
            style={{
              padding: '13px 16px',
              display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              cursor: 'pointer',
              borderBottom: '1px solid var(--border)',
              background: selectedService === svc ? 'var(--surface-active)' : 'transparent',
              transition: 'background 0.1s',
            }}
            onMouseEnter={e => {
              if (svc !== selectedService)
                (e.currentTarget as HTMLElement).style.background = 'var(--surface-hover)'
            }}
            onMouseLeave={e => {
              if (svc !== selectedService)
                (e.currentTarget as HTMLElement).style.background = 'transparent'
            }}
          >
            <div style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, minWidth: 0 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{
                  width: 7, height: 7, borderRadius: '50%', flexShrink: 0,
                  background: 'var(--green)',
                  boxShadow: '0 0 6px var(--green)',
                  animation: 'pulse 2s infinite',
                }} />
                <span style={{ fontSize: 13, fontWeight: 500 }}>{svc}</span>
              </div>
              <ProviderStack service={svc} compact />
            </div>

            <button
              onClick={e => openInvalidate(svc, e)}
              title="Invalidate cache"
              style={{
                background: 'none', border: '1px solid transparent',
                borderRadius: 6, padding: '4px 8px',
                cursor: 'pointer', color: 'var(--muted)',
                display: 'flex', alignItems: 'center', gap: 4, fontSize: 11,
                transition: 'all 0.12s',
              }}
              onMouseEnter={e => {
                const el = e.currentTarget as HTMLElement
                el.style.color = 'var(--accent)'
                el.style.borderColor = 'var(--accent-dim)'
                el.style.background = 'var(--accent-dim)'
              }}
              onMouseLeave={e => {
                const el = e.currentTarget as HTMLElement
                el.style.color = 'var(--muted)'
                el.style.borderColor = 'transparent'
                el.style.background = 'none'
              }}
            >
              <Zap size={11} /> Flush
            </button>
          </div>
        ))}
      </div>

      {/* Summary footer */}
      {services.length > 0 && (
        <div style={{
          padding: '8px 14px', borderTop: '1px solid var(--border)',
          fontSize: 11, color: 'var(--muted)',
          display: 'flex', alignItems: 'center', gap: 6,
        }}>
          {health?.redis
            ? <CheckCircle size={11} style={{ color: 'var(--green)' }} />
            : <XCircle size={11} style={{ color: 'var(--red)' }} />
          }
          Store {health?.redis ? 'connected' : 'offline'}
        </div>
      )}

      {showModal && (
        <InvalidateModal
          defaultService={modalService}
          onClose={() => setShowModal(false)}
        />
      )}
    </div>
  )
}
