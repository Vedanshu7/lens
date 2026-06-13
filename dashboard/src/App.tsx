import { useState } from 'react'
import { Layers, Server, Key, FileText, Activity, Zap, type LucideIcon } from 'lucide-react'
import { InvalidateModal } from './components/InvalidateModal'
import { ServicesPage } from './pages/ServicesPage'
import { NodesPage } from './pages/NodesPage'
import { KeysPage } from './pages/KeysPage'
import { AuditPage } from './pages/AuditPage'
import { ObservabilityPage } from './pages/ObservabilityPage'

type Tab = 'services' | 'nodes' | 'keys' | 'audit' | 'observability'

const NAV: { id: Tab; icon: LucideIcon; label: string }[] = [
  { id: 'services', icon: Layers, label: 'Services' },
  { id: 'nodes', icon: Server, label: 'Nodes' },
  { id: 'keys', icon: Key, label: 'Keys' },
  { id: 'audit', icon: FileText, label: 'Audit' },
  { id: 'observability', icon: Activity, label: 'Observability' },
]

export function App() {
  const [tab, setTab] = useState<Tab>('services')
  const [service, setService] = useState('')
  const [showInvalidate, setShowInvalidate] = useState(false)

  const handleSelectService = (svc: string) => {
    setService(svc)
    setTab('nodes')
  }

  return (
    <div style={{ display: 'flex', height: '100vh', overflow: 'hidden' }}>
      {/* Sidebar */}
      <div style={{
        width: 200, flexShrink: 0, display: 'flex', flexDirection: 'column',
        borderRight: '1px solid var(--border)', background: 'var(--bg-subtle)',
      }}>
        {/* Logo */}
        <div style={{
          padding: '16px 18px', borderBottom: '1px solid var(--border)',
          display: 'flex', alignItems: 'center', gap: 10,
        }}>
          <div style={{
            width: 28, height: 28, borderRadius: 7,
            background: 'var(--accent-dim)', border: '1px solid rgba(124,140,245,0.2)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <Layers size={14} style={{ color: 'var(--accent)' }} />
          </div>
          <div>
            <div style={{ fontSize: 14, fontWeight: 700, color: 'var(--text)' }}>Lens</div>
            <div style={{ fontSize: 10, color: 'var(--muted)' }}>Cache Sidecar</div>
          </div>
        </div>

        {/* Service selector */}
        {service && (
          <div style={{
            margin: '10px 10px 0', padding: '7px 10px',
            background: 'var(--surface)', border: '1px solid var(--border)',
            borderRadius: 7, display: 'flex', alignItems: 'center', gap: 6,
          }}>
            <span style={{
              width: 6, height: 6, borderRadius: '50%',
              background: 'var(--green)', boxShadow: '0 0 5px var(--green)',
              flexShrink: 0,
            }} />
            <span style={{ fontSize: 12, fontWeight: 500, flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
              {service}
            </span>
            <button
              onClick={() => setService('')}
              title="Deselect service"
              style={{
                background: 'none', border: 'none', cursor: 'pointer',
                color: 'var(--muted)', fontSize: 14, lineHeight: 1, padding: 0, flexShrink: 0,
              }}
            >
              x
            </button>
          </div>
        )}

        {/* Nav */}
        <nav style={{ flex: 1, padding: '10px 8px', display: 'flex', flexDirection: 'column', gap: 2 }}>
          {NAV.map(({ id, icon: Icon, label }) => (
            <button
              key={id}
              onClick={() => setTab(id)}
              style={{
                display: 'flex', alignItems: 'center', gap: 9,
                padding: '8px 10px', borderRadius: 7, border: 'none',
                background: tab === id ? 'var(--surface-active)' : 'transparent',
                color: tab === id ? 'var(--text)' : 'var(--muted)',
                fontSize: 12, fontWeight: tab === id ? 600 : 400,
                cursor: 'pointer', textAlign: 'left', width: '100%',
                transition: 'all 0.1s',
              }}
              onMouseEnter={e => {
                if (id !== tab) (e.currentTarget as HTMLElement).style.background = 'var(--surface-hover)'
              }}
              onMouseLeave={e => {
                if (id !== tab) (e.currentTarget as HTMLElement).style.background = 'transparent'
              }}
            >
              <Icon size={14} />
              {label}
            </button>
          ))}
        </nav>

        {/* Invalidate button */}
        <div style={{ padding: '10px 10px 14px' }}>
          <button
            onClick={() => setShowInvalidate(true)}
            style={{
              width: '100%', padding: '8px 12px',
              display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 6,
              background: 'var(--accent-dim)', border: '1px solid rgba(124,140,245,0.25)',
              borderRadius: 7, color: 'var(--accent)', fontSize: 12, fontWeight: 600,
              cursor: 'pointer', transition: 'all 0.12s',
            }}
          >
            <Zap size={12} /> Invalidate
          </button>
        </div>
      </div>

      {/* Main content */}
      <main style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
        {tab === 'services' && (
          <ServicesPage selectedService={service} onSelectService={handleSelectService} />
        )}
        {tab === 'nodes' && <NodesPage service={service} />}
        {tab === 'keys' && <KeysPage service={service} />}
        {tab === 'audit' && <AuditPage />}
        {tab === 'observability' && <ObservabilityPage service={service} />}
      </main>

      {showInvalidate && (
        <InvalidateModal
          defaultService={service}
          onClose={() => setShowInvalidate(false)}
        />
      )}
    </div>
  )
}
