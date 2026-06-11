import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { X, Zap, CheckCircle, XCircle } from 'lucide-react'
import { toast } from 'sonner'
import { api, InvalidateResult } from '../lib/api'
import { shortPod } from '../lib/utils'
import { inputLg } from './ui/theme'

interface Props {
  onClose: () => void
  defaultService?: string
}

export function InvalidateModal({ onClose, defaultService }: Props) {
  const [service, setService] = useState(defaultService ?? '')
  const [pattern, setPattern] = useState('')
  const [result, setResult] = useState<InvalidateResult | null>(null)
  const queryClient = useQueryClient()

  const { data: services } = useQuery({
    queryKey: ['services'],
    queryFn: () => api.services().then(d => d.services),
  })

  const mutation = useMutation({
    mutationFn: () => api.invalidate(service, pattern || undefined),
    onSuccess: (data) => {
      setResult(data)
      queryClient.invalidateQueries({ queryKey: ['audit'] })
      const ok = data.confirmed
      const total = data.total
      if (ok === total) {
        toast.success(`${ok}/${total} instances invalidated`)
      } else {
        toast.warning(`${ok}/${total} instances confirmed`)
      }
    },
    onError: (err: Error) => {
      toast.error(err.message)
    },
  })

  return (
    <div
      onClick={e => e.target === e.currentTarget && onClose()}
      style={{
        position: 'fixed', inset: 0, zIndex: 200,
        background: 'rgba(0,0,0,0.7)', backdropFilter: 'blur(4px)',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
      }}
    >
      <div style={{
        background: 'var(--surface)', border: '1px solid var(--border-light)',
        borderRadius: 12, width: 520,
        maxHeight: '80vh', overflow: 'hidden', display: 'flex', flexDirection: 'column',
        boxShadow: '0 24px 48px rgba(0,0,0,0.5)',
      }}>
        {/* Header */}
        <div style={{
          padding: '18px 24px', borderBottom: '1px solid var(--border)',
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <Zap size={16} style={{ color: 'var(--accent)' }} />
            <span style={{ fontSize: 14, fontWeight: 600 }}>Invalidate Cache</span>
          </div>
          <button onClick={onClose} style={{
            background: 'none', border: 'none', cursor: 'pointer',
            color: 'var(--muted)', padding: 4, borderRadius: 4, display: 'flex', alignItems: 'center',
          }}>
            <X size={16} />
          </button>
        </div>

        {/* Body */}
        <div style={{ padding: '20px 24px', overflowY: 'auto', flex: 1 }}>
          <Field label="Service">
            <select
              value={service}
              onChange={e => { setService(e.target.value); setResult(null) }}
              style={inputLg}
            >
              <option value="">Select a service…</option>
              {services?.map(s => (
                <option key={s} value={s}>{s}</option>
              ))}
            </select>
          </Field>

          <Field label="Key Pattern (optional)" hint="Leave empty to flush all keys">
            <input
              value={pattern}
              onChange={e => setPattern(e.target.value)}
              placeholder="e.g. config, user:42"
              style={inputLg}
            />
          </Field>

          <button
            onClick={() => mutation.mutate()}
            disabled={!service || mutation.isPending}
            style={{
              width: '100%', padding: '10px 16px',
              background: service ? 'var(--accent)' : 'var(--surface-hover)',
              color: service ? '#fff' : 'var(--muted)',
              border: 'none', borderRadius: 8,
              fontSize: 13, fontWeight: 600, cursor: service ? 'pointer' : 'not-allowed',
              display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 8,
              transition: 'all 0.12s',
            }}
          >
            {mutation.isPending
              ? <><Zap size={14} style={{ animation: 'spin 1s linear infinite' }} /> Invalidating…</>
              : <><Zap size={14} /> Trigger Invalidation</>
            }
          </button>

          {result && (
            <div style={{
              marginTop: 16, background: 'var(--bg)',
              border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden',
            }}>
              <div style={{
                padding: '10px 14px', borderBottom: '1px solid var(--border)',
                display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              }}>
                <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-secondary)' }}>
                  Results — {result.service}
                </span>
                <span style={{
                  fontSize: 12, fontWeight: 700,
                  color: result.confirmed === result.total ? 'var(--green)'
                    : result.confirmed === 0 ? 'var(--red)' : 'var(--yellow)',
                }}>
                  {result.confirmed}/{result.total} confirmed
                </span>
              </div>
              <div style={{ padding: 10, display: 'flex', flexDirection: 'column', gap: 4 }}>
                {result.instances.map(inst => (
                  <div key={inst.instance} style={{
                    display: 'flex', alignItems: 'center', gap: 8,
                    padding: '6px 10px', borderRadius: 6,
                    background: 'var(--surface)', border: '1px solid var(--border)',
                    fontSize: 12,
                  }}>
                    {inst.success
                      ? <CheckCircle size={13} style={{ color: 'var(--green)', flexShrink: 0 }} />
                      : <XCircle size={13} style={{ color: 'var(--red)', flexShrink: 0 }} />
                    }
                    <span className="mono" style={{ flex: 1, color: 'var(--text-secondary)', fontSize: 11 }}>
                      {shortPod(inst.instance)}
                    </span>
                    <span style={{
                      fontSize: 10, fontWeight: 600,
                      color: inst.success ? 'var(--green)' : 'var(--red)',
                    }}>
                      {inst.success ? 'cleared' : 'failed'}
                    </span>
                    {inst.error && (
                      <span
                        title={inst.error}
                        style={{ fontSize: 10, color: 'var(--muted)', maxWidth: 160, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
                      >
                        {inst.error}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>

      <style>{`
        @keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
      `}</style>
    </div>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 14 }}>
      <div style={{
        fontSize: 10, fontWeight: 700, textTransform: 'uppercase',
        letterSpacing: '0.06em', color: 'var(--muted)', marginBottom: 6,
        display: 'flex', justifyContent: 'space-between',
      }}>
        {label}
        {hint && <span style={{ fontWeight: 400, textTransform: 'none', letterSpacing: 0 }}>{hint}</span>}
      </div>
      {children}
    </div>
  )
}
