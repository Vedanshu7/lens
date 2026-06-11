import type { CSSProperties } from 'react'

export const label: CSSProperties = {
  fontSize: 10,
  fontWeight: 700,
  textTransform: 'uppercase',
  letterSpacing: '0.06em',
  color: 'var(--muted)',
}

export const panelHeader: CSSProperties = {
  padding: '11px 14px',
  fontSize: 10,
  fontWeight: 700,
  textTransform: 'uppercase',
  letterSpacing: '0.08em',
  color: 'var(--muted)',
  borderBottom: '1px solid var(--border)',
  display: 'flex',
  alignItems: 'center',
  gap: 6,
  background: 'var(--bg-subtle)',
  position: 'sticky',
  top: 0,
  zIndex: 1,
}

export const countBadge: CSSProperties = {
  background: 'var(--accent-dim)',
  color: 'var(--accent)',
  padding: '1px 7px',
  borderRadius: 10,
  fontSize: 10,
  fontWeight: 600,
}

export const input: CSSProperties = {
  width: '100%',
  padding: '7px 12px',
  background: 'var(--surface)',
  border: '1px solid var(--border)',
  borderRadius: 6,
  color: 'var(--text)',
  fontSize: 12,
  outline: 'none',
}

export const inputLg: CSSProperties = {
  width: '100%',
  padding: '9px 12px',
  background: 'var(--bg)',
  border: '1px solid var(--border)',
  borderRadius: 7,
  color: 'var(--text)',
  fontSize: 13,
  outline: 'none',
}

export const btnPrimary: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 6,
  padding: '7px 14px',
  borderRadius: 7,
  background: 'var(--surface-hover)',
  border: '1px solid var(--border)',
  color: 'var(--text-secondary)',
  fontSize: 12,
  fontWeight: 500,
  cursor: 'pointer',
  transition: 'all 0.12s',
}

export const btnDanger: CSSProperties = {
  ...btnPrimary,
  color: 'var(--red)',
  borderColor: 'rgba(244,114,114,0.2)',
}

export const th: CSSProperties = {
  textAlign: 'left',
  padding: '10px 16px',
  fontSize: 10,
  fontWeight: 700,
  textTransform: 'uppercase',
  letterSpacing: '0.06em',
  color: 'var(--muted)',
  background: 'var(--bg-subtle)',
  borderBottom: '1px solid var(--border)',
}

export const td: CSSProperties = {
  padding: '12px 16px',
}
