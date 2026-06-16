import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip,
  BarChart, Bar, Cell, ScatterChart, Scatter, ResponsiveContainer, Legend,
} from 'recharts'
import { api } from '../lib/api'
import { ProviderStack } from '../components/ProviderStack'

const RANGES = ['1 hour', '6 hours', '24 hours', '7 days'] as const
type Range = typeof RANGES[number]

function StatCard({ label, value, unit }: { label: string; value: string | number; unit?: string }) {
  return (
    <div style={{
      background: 'var(--surface)', border: '1px solid var(--border)',
      borderRadius: 10, padding: '16px 20px',
    }}>
      <div style={{ fontSize: 11, color: 'var(--muted)', marginBottom: 4 }}>{label}</div>
      <div style={{ fontSize: 24, fontWeight: 700, color: 'var(--text)' }}>
        {value}
        {unit && <span style={{ fontSize: 12, fontWeight: 400, color: 'var(--muted)', marginLeft: 4 }}>{unit}</span>}
      </div>
    </div>
  )
}

export function ObservabilityPage({ service }: { service?: string }) {
  const [range, setRange] = useState<Range>('24 hours')
  const svc = service ?? ''

  const { data: summary } = useQuery({
    queryKey: ['obs-summary', svc, range],
    queryFn: () => api.obs.summary(svc, range),
    enabled: !!svc,
    refetchInterval: 30000,
  })

  const { data: latencyData } = useQuery({
    queryKey: ['obs-latency', svc, range],
    queryFn: () => api.obs.latency(svc, range),
    enabled: !!svc,
    refetchInterval: 30000,
  })

  const { data: deadPodsData } = useQuery({
    queryKey: ['obs-deadpods', svc, range],
    queryFn: () => api.obs.deadpods(svc, range),
    enabled: !!svc,
    refetchInterval: 60000,
  })

  const { data: discoveryData } = useQuery({
    queryKey: ['obs-discovery', range],
    queryFn: () => api.obs.discovery(range),
    refetchInterval: 60000,
  })

  const { data: flowData } = useQuery({
    queryKey: ['obs-flow', svc, range],
    queryFn: () => api.obs.flow(svc, range),
    enabled: !!svc,
    refetchInterval: 30000,
  })

  return (
    <div style={{ padding: '24px 28px', height: '100%', overflowY: 'auto' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 24 }}>
        <h1 style={{ fontSize: 20, fontWeight: 700, margin: 0 }}>Observability</h1>
        <div style={{ display: 'flex', gap: 6 }}>
          {RANGES.map(r => (
            <button
              key={r}
              onClick={() => setRange(r)}
              style={{
                padding: '5px 12px', borderRadius: 6, border: '1px solid var(--border)',
                background: range === r ? 'var(--surface-active)' : 'transparent',
                color: range === r ? 'var(--text)' : 'var(--muted)',
                fontSize: 12, fontWeight: range === r ? 600 : 400, cursor: 'pointer',
              }}
            >
              {r}
            </button>
          ))}
        </div>
      </div>

      {svc && <ProviderStack service={svc} />}

      {!svc && (
        <div style={{
          padding: 32, textAlign: 'center',
          background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 10,
          color: 'var(--muted)', fontSize: 14,
        }}>
          Select a service from the Services page to view observability data.
        </div>
      )}

      {svc && (
        <>
          {/* Summary cards */}
          {summary && (
            <>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 12, marginBottom: 12 }}>
                <StatCard label="Invalidations" value={summary.totalInvalidations} />
                <StatCard label="Avg Latency" value={summary.avgLatencyMs.toFixed(1)} unit="ms" />
                <StatCard label="p99 Latency" value={summary.p99LatencyMs.toFixed(1)} unit="ms" />
                <StatCard label="Failure Rate" value={summary.failureRatePct.toFixed(1)} unit="%" />
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(6, 1fr)', gap: 12, marginBottom: 28 }}>
                <StatCard label="Avg Transport" value={summary.avgTransportMs.toFixed(1)} unit="ms" />
                <StatCard label="Avg Target" value={summary.avgTargetMs.toFixed(1)} unit="ms" />
                <StatCard label="Avg Persistence" value={summary.avgPersistenceMs.toFixed(1)} unit="ms" />
                <StatCard label="Dead Pods" value={summary.deadPodsDetected} />
                <StatCard label="Peers Joined" value={summary.peersJoined} />
                <StatCard label="Peers Left" value={summary.peersLeft} />
              </div>
            </>
          )}

          {/* End-to-end latency over time */}
          <section style={{ marginBottom: 32 }}>
            <h2 style={{ fontSize: 14, fontWeight: 600, marginBottom: 12 }}>Invalidation Latency</h2>
            <div style={{
              background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 10, padding: 20,
            }}>
              <ResponsiveContainer width="100%" height={240}>
                <LineChart data={latencyData?.buckets ?? []}>
                  <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                  <XAxis dataKey="bucket" tick={{ fontSize: 11 }}
                    tickFormatter={v => new Date(v).toLocaleTimeString()} />
                  <YAxis tick={{ fontSize: 11 }} unit="ms" />
                  <Tooltip
                    labelFormatter={v => new Date(v as string).toLocaleString()}
                    formatter={(v) => [typeof v === 'number' ? `${v.toFixed(1)}ms` : String(v ?? '')]}
                  />
                  <Legend />
                  <Line type="monotone" dataKey="p50" name="p50" stroke="#7c8cf5" dot={false} />
                  <Line type="monotone" dataKey="p95" name="p95" stroke="#f59e0b" dot={false} />
                  <Line type="monotone" dataKey="p99" name="p99" stroke="#ef4444" dot={false} />
                </LineChart>
              </ResponsiveContainer>
            </div>
          </section>

          {/* Per-layer latency breakdown */}
          {(latencyData?.buckets ?? []).length > 0 && (
            <section style={{ marginBottom: 32 }}>
              <h2 style={{ fontSize: 14, fontWeight: 600, marginBottom: 12 }}>Layer Latency Breakdown (p50)</h2>
              <div style={{
                background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 10, padding: 20,
              }}>
                <ResponsiveContainer width="100%" height={240}>
                  <BarChart data={latencyData?.buckets ?? []} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="bucket" tick={{ fontSize: 11 }}
                      tickFormatter={v => new Date(v).toLocaleTimeString()} />
                    <YAxis tick={{ fontSize: 11 }} unit="ms" />
                    <Tooltip
                      labelFormatter={v => new Date(v as string).toLocaleString()}
                      formatter={(v) => [typeof v === 'number' ? `${v.toFixed(1)}ms` : String(v ?? '')]}
                      contentStyle={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 6, fontSize: 12 }}
                    />
                    <Legend />
                    <Bar dataKey="transportP50" name="Transport p50" stackId="layers" fill="#7c8cf5" />
                    <Bar dataKey="targetP50" name="Target p50" stackId="layers" fill="#22c55e" />
                    <Bar dataKey="persistenceP50" name="Persistence p50" stackId="layers" fill="#f59e0b" radius={[4, 4, 0, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </section>
          )}

          {/* Event flow */}
          {flowData && (
            <section style={{ marginBottom: 32 }}>
              <h2 style={{ fontSize: 14, fontWeight: 600, marginBottom: 12 }}>Event Flow</h2>
              <div style={{
                background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 10, padding: 20,
              }}>
                <ResponsiveContainer width="100%" height={220}>
                  <BarChart
                    data={[
                      { name: 'Inval OK',      value: flowData.invalidate.success,         fill: '#22c55e' },
                      { name: 'Inval Partial', value: flowData.invalidate.partial,          fill: '#f59e0b' },
                      { name: 'Inval Fail',    value: flowData.invalidate.failure,          fill: '#ef4444' },
                      { name: 'Apply OK',      value: flowData.apply?.success ?? 0,         fill: '#34d399' },
                      { name: 'Apply Fail',    value: flowData.apply?.failure ?? 0,         fill: '#f43f5e' },
                      { name: 'Fetch OK',      value: flowData.fetch.success,               fill: '#7c8cf5' },
                      { name: 'Fetch Fail',    value: flowData.fetch.failure,               fill: '#c084fc' },
                      { name: 'Persist Writes', value: flowData.persistenceWrite?.total ?? 0, fill: '#fb923c' },
                      { name: 'Replay',        value: flowData.replay.total,                fill: '#a78bfa' },
                    ]}
                    margin={{ top: 4, right: 8, left: 0, bottom: 24 }}
                  >
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" vertical={false} />
                    <XAxis dataKey="name" tick={{ fontSize: 10, fill: 'var(--muted)' }} interval={0} angle={-20} textAnchor="end" />
                    <YAxis tick={{ fontSize: 10, fill: 'var(--muted)' }} />
                    <Tooltip
                      contentStyle={{ background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 6, fontSize: 12 }}
                      cursor={{ fill: 'rgba(255,255,255,0.04)' }}
                    />
                    <Bar dataKey="value" radius={[4, 4, 0, 0]}>
                      {[
                        '#22c55e', '#f59e0b', '#ef4444',
                        '#34d399', '#f43f5e',
                        '#7c8cf5', '#c084fc',
                        '#fb923c', '#a78bfa',
                      ].map((color, i) => <Cell key={i} fill={color} />)}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </section>
          )}

          {/* Dead pods */}
          {deadPodsData && (deadPodsData.events ?? []).length > 0 && (
            <section style={{ marginBottom: 32 }}>
              <h2 style={{ fontSize: 14, fontWeight: 600, marginBottom: 12 }}>Dead Pod Events</h2>
              <div style={{
                background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 10, overflow: 'hidden',
              }}>
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                  <thead>
                    <tr style={{ background: 'var(--bg-subtle)', borderBottom: '1px solid var(--border)' }}>
                      {['Time', 'Peer ID', 'Detection (ms)'].map(h => (
                        <th key={h} style={{ padding: '8px 16px', textAlign: 'left', fontWeight: 600, color: 'var(--muted)' }}>{h}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {(deadPodsData?.events ?? []).map((e, i) => (
                      <tr key={i} style={{ borderBottom: '1px solid var(--border)' }}>
                        <td style={{ padding: '8px 16px', color: 'var(--muted)' }}>{new Date(e.ts).toLocaleString()}</td>
                        <td style={{ padding: '8px 16px', fontFamily: 'monospace', color: 'var(--text)' }}>{e.peerId}</td>
                        <td style={{ padding: '8px 16px', color: 'var(--text)' }}>{e.detectionMs.toFixed(1)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>
          )}

          {/* Discovery timeline */}
          {discoveryData && (discoveryData.events ?? []).length > 0 && (
            <section style={{ marginBottom: 32 }}>
              <h2 style={{ fontSize: 14, fontWeight: 600, marginBottom: 12 }}>Discovery Timeline</h2>
              <div style={{
                background: 'var(--surface)', border: '1px solid var(--border)', borderRadius: 10, padding: 20,
              }}>
                <ResponsiveContainer width="100%" height={200}>
                  <ScatterChart>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" />
                    <XAxis dataKey="peerCount" name="Peers" tick={{ fontSize: 11 }} />
                    <YAxis dataKey="resolutionMs" name="Resolution" unit="ms" tick={{ fontSize: 11 }} />
                    <Tooltip cursor={{ strokeDasharray: '3 3' }} />
                    <Scatter data={discoveryData?.events ?? []} fill="#7c8cf5" />
                  </ScatterChart>
                </ResponsiveContainer>
              </div>
            </section>
          )}
        </>
      )}
    </div>
  )
}
