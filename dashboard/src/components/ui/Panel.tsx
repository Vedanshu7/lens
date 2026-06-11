import type { ReactNode } from 'react'
import { countBadge, panelHeader } from './theme'

export function EmptyState({ icon, text }: { icon?: ReactNode; text: string }) {
  return (
    <div style={{ color: 'var(--muted)', fontSize: 12, padding: '24px 16px', textAlign: 'center', lineHeight: 1.6 }}>
      {icon && <div style={{ opacity: 0.3, marginBottom: 12 }}>{icon}</div>}
      <div>{text}</div>
    </div>
  )
}

export function Spinner() {
  return <div style={{ color: 'var(--muted)', fontSize: 13 }}>Loading…</div>
}

export function PanelHeader({ label, count, children }: { label: string; count?: number; children?: ReactNode }) {
  return (
    <div style={panelHeader}>
      {label}
      {count !== undefined && count > 0 && <span style={countBadge}>{count}</span>}
      {children}
    </div>
  )
}
