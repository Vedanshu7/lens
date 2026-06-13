#!/usr/bin/env bash
# Simulates realistic user traffic with failure injection across 10 pods.
# Failures: dead app nodes, dead sidecars, partial broadcasts, burst stress.

N_PODS=10

# Build app/lens URL arrays
APPS=(); LENSES=(); LENS_CONTAINERS=()
for i in $(seq 1 $N_PODS); do
  port=$((8080 + i)); [[ $i -eq 10 ]] && port=8090
  APPS+=("http://localhost:$port")
  lport=$((8900 + i))
  LENSES+=("http://localhost:$lport")
  LENS_CONTAINERS+=("example-lens-$i-1")
done
APP_CONTAINERS=(); for i in $(seq 1 $N_PODS); do APP_CONTAINERS+=("example-app-$i-1"); done

KEYS=("user:1" "user:2" "user:3" "user:4" "user:5"
      "config:theme" "config:flags" "config:rate" "config:ab"
      "session:abc" "session:xyz" "session:q99" "session:r12"
      "product:42" "product:99" "product:7" "product:88"
      "cart:7" "cart:22" "cart:55"
      "feed:top" "feed:trending" "feed:new" "feed:hot"
      "order:1001" "order:1002" "order:1003"
      "promo:summer" "promo:flash"
      "meta:version")

sep() { printf '\n%s\n' "────────────────────────────────────────────────────────────────"; }

rand_app()  { echo "${APPS[$((RANDOM % N_PODS))]}"; }
rand_lens() { echo "${LENSES[$((RANDOM % N_PODS))]}"; }
rand_key()  { echo "${KEYS[$((RANDOM % ${#KEYS[@]}))]}"; }

timed_curl() {
  local out
  out=$(curl -s -o /tmp/lens_body -w "%{http_code}|%{time_total}" "$@")
  local http_code="${out%%|*}"
  local ms_raw="${out##*|}"
  local ms; ms=$(printf "%.0f" "$(echo "$ms_raw * 1000" | bc)")
  echo "${ms}|${http_code}"
}

record() {
  local label="$1" result="$2"
  local ms="${result%%|*}" code="${result##*|}"
  # 2xx ok; 404 = cache miss (separate bucket); 429 = rate limited (ok); else fail
  if [[ "$code" == "2"* ]] || [[ "$code" == "429" ]]; then
    eval "${label}_ok+=($ms)"
  elif [[ "$code" == "404" ]]; then
    eval "${label}_miss+=($ms)"
  else
    eval "${label}_fail+=($ms)"
  fi
}

stats() {
  local label="$1"; shift
  local values=("$@")
  local count=${#values[@]}
  [[ $count -eq 0 ]] && return
  IFS=$'\n' sorted=($(printf '%s\n' "${values[@]}" | sort -n)); unset IFS
  local sum=0; for v in "${values[@]}"; do sum=$((sum + v)); done
  local avg=$((sum / count))
  local p50=${sorted[$((count / 2))]}
  local p95idx=$(( count * 95 / 100 )); [[ $p95idx -ge $count ]] && p95idx=$((count-1))
  local p99idx=$(( count * 99 / 100 )); [[ $p99idx -ge $count ]] && p99idx=$((count-1))
  printf "  %-38s  min=%-6s avg=%-6s p50=%-6s p95=%-6s p99=%-6s max=%-6s  n=%d\n" \
    "$label" \
    "${sorted[0]}ms" "${avg}ms" "${p50}ms" \
    "${sorted[$p95idx]}ms" "${sorted[$p99idx]}ms" \
    "${sorted[$((count-1))]}ms" "$count"
}

app_pause()   { docker pause   "$1" > /dev/null 2>&1; }
app_resume()  { docker unpause "$1" > /dev/null 2>&1; }

echo ""
echo "  Lens load test — 10 pods, failure injection"
printf "  Apps:    %s\n" "${APPS[*]}"
printf "  Keys:    %d\n" "${#KEYS[@]}"

# ── Verify all pods reachable ──────────────────────────────────────────────────
sep
echo "  Checking pods…"
live_apps=(); live_lenses=()
for i in $(seq 0 $((N_PODS-1))); do
  code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 1 "${APPS[$i]}/api/cache/ping" 2>/dev/null || echo 000)
  if [[ "$code" != "000" ]]; then
    live_apps+=("${APPS[$i]}")
    live_lenses+=("${LENSES[$i]}")
    printf "    pod %-2d  app=%s  lens=%s  [UP]\n" "$((i+1))" "${APPS[$i]}" "${LENSES[$i]}"
  else
    printf "    pod %-2d  app=%s  [DOWN — skipped]\n" "$((i+1))" "${APPS[$i]}"
  fi
done
N_LIVE=${#live_apps[@]}
echo "  $N_LIVE / $N_PODS pods live"
if [[ $N_LIVE -eq 0 ]]; then echo "  No pods reachable. Run: docker compose up --build -d"; exit 1; fi

# ── Phase 1: Seed ──────────────────────────────────────────────────────────────
sep
printf "  Phase 1: Seeding %d keys on all %d live pods…\n" "${#KEYS[@]}" "$N_LIVE"
for k in "${KEYS[@]}"; do
  for app in "${live_apps[@]}"; do
    curl -sX POST "$app/api/cache" -H 'Content-Type: application/json' \
      -d "{\"key\":\"$k\",\"value\":\"v0-$k\"}" > /dev/null
  done
done
echo "  done"

# ── Phase 2: Normal reads + writes (no failures) ───────────────────────────────
sep
echo "  Phase 2: 200 reads + 60 writes — normal traffic across all pods…"
read2_ok=(); read2_fail=(); write2_ok=(); write2_fail=()
for i in $(seq 1 200); do
  r=$(timed_curl "${live_apps[$((RANDOM % N_LIVE))]}/api/cache/$(rand_key)")
  record read2 "$r"
done
for i in $(seq 1 60); do
  r=$(timed_curl -X POST "${live_apps[$((RANDOM % N_LIVE))]}/api/cache" \
    -H 'Content-Type: application/json' -d "{\"key\":\"$(rand_key)\",\"value\":\"w$i\"}")
  record write2 "$r"
done
echo "  done"

# ── Phase 3: Kill 3 random app nodes ──────────────────────────────────────────
sep
echo "  Phase 3: Pausing 3 random app nodes — partial reads + partial invalidations…"
dead_app_idx=(2 5 8)   # 0-based: pod 3, 6, 9
for idx in "${dead_app_idx[@]}"; do
  app_pause "${APP_CONTAINERS[$idx]}"
  echo "    paused ${APP_CONTAINERS[$idx]}"
done
sleep 0.5

read3_ok=(); read3_fail=(); inv3_ok=(); inv3_fail=()
echo "  80 reads (some route to dead nodes)…"
for i in $(seq 1 80); do
  r=$(timed_curl --max-time 2 "${live_apps[$((RANDOM % N_LIVE))]}/api/cache/$(rand_key)")
  record read3 "$r"
done
echo "  40 invalidation broadcasts from live sidecar (expect partial confirms)…"
for i in $(seq 1 40); do
  r=$(timed_curl --max-time 4 -X DELETE "${live_lenses[$((RANDOM % N_LIVE))]%:89*}:${live_lenses[$((RANDOM % N_LIVE))]##*:}/api/cache")
  # simpler: pick a live app and DELETE
  r=$(timed_curl --max-time 4 -X DELETE "${live_apps[$((RANDOM % N_LIVE))]}/api/cache")
  record inv3 "$r"
done

echo "  Resuming dead app nodes…"
for idx in "${dead_app_idx[@]}"; do
  app_resume "${APP_CONTAINERS[$idx]}"
done
sleep 1

# ── Phase 4: Kill 3 random lens sidecars ──────────────────────────────────────
sep
echo "  Phase 4: Pausing 3 lens sidecars — gRPC broadcast failures…"
dead_lens_idx=(1 4 7)  # pod 2, 5, 8
for idx in "${dead_lens_idx[@]}"; do
  app_pause "${LENS_CONTAINERS[$idx]}"
  echo "    paused ${LENS_CONTAINERS[$idx]}"
done
sleep 0.5

inv4_ok=(); inv4_fail=()
echo "  50 invalidation broadcasts (dead sidecars miss the broadcast)…"
for i in $(seq 1 50); do
  r=$(timed_curl --max-time 5 -X DELETE "${live_apps[$((RANDOM % N_LIVE))]}/api/cache")
  record inv4 "$r"
  sleep 0.05
done

echo "  Resuming sidecars…"
for idx in "${dead_lens_idx[@]}"; do
  app_resume "${LENS_CONTAINERS[$idx]}"
done
sleep 2

# ── Phase 5: Explicit peer-fetch (generates EventFetch in SQL) ────────────────
sep
echo "  Phase 5: 60 explicit peer-fetch requests — EventFetch success + failure…"
fetch_ok=(); fetch_fail=()
# Collect live instance names by asking lens-1 /api/nodes
instances=$(curl -s "http://localhost:8901/api/nodes?service=demo" \
  | grep -o '"instance":"[^"]*"' | sed 's/"instance":"//;s/"//')
IFS=$'\n' read -rd '' -a INSTANCES <<< "$instances" || true

# Success: fetch existing keys from known instances via any live sidecar
for i in $(seq 1 40); do
  lens="${live_lenses[$((RANDOM % N_LIVE))]}"
  k="$(rand_key)"
  # pick a random known instance (or fallback to app-1)
  inst="app-$((1 + RANDOM % N_PODS))"
  r=$(timed_curl --max-time 3 -X POST "$lens/api/fetch" \
    -H 'Content-Type: application/json' \
    -d "{\"service\":\"demo\",\"instance\":\"$inst\",\"key\":\"$k\"}")
  record fetch "$r"
done

# Failure: fetch from a non-existent instance (generates EventFetch failure)
for i in $(seq 1 20); do
  lens="${live_lenses[$((RANDOM % N_LIVE))]}"
  r=$(timed_curl --max-time 2 -X POST "$lens/api/fetch" \
    -H 'Content-Type: application/json' \
    -d "{\"service\":\"demo\",\"instance\":\"ghost-pod-$i\",\"key\":\"user:$i\"}")
  record fetch "$r"
done
echo "  done — fetch ok=${#fetch_ok[@]} fail=${#fetch_fail[@]}"

# ── Phase 6: Replay — restart one sidecar so it replays Redis events ──────────
sep
echo "  Phase 6: Triggering replay — fire 20 invalidations then restart lens-3…"
# Seed some events that lens-3 will miss
for i in $(seq 1 20); do
  curl -sX DELETE "http://localhost:8081/api/cache" > /dev/null
done
echo "  Restarting lens-3 (it will replay missed invalidations from Redis)…"
docker restart "example-lens-3-1" > /dev/null 2>&1
sleep 4   # wait for it to reconnect and replay
echo "  done"

# ── Phase 7: Rapid-fire burst ─────────────────────────────────────────────────
sep
echo "  Phase 7: 500 mixed requests — rapid-fire burst across all 10 pods…"
burst_r_ok=(); burst_r_fail=()
burst_w_ok=(); burst_w_fail=()
burst_i_ok=(); burst_i_fail=()
for i in $(seq 1 500); do
  app="${live_apps[$((RANDOM % N_LIVE))]}"
  k="$(rand_key)"
  roll=$((RANDOM % 10))
  if (( roll < 7 )); then
    r=$(timed_curl "$app/api/cache/$k")
    record burst_r "$r"
  elif (( roll < 9 )); then
    r=$(timed_curl -X POST "$app/api/cache" \
      -H 'Content-Type: application/json' -d "{\"key\":\"$k\",\"value\":\"b$i\"}")
    record burst_w "$r"
  else
    r=$(timed_curl -X DELETE "$app/api/cache")
    record burst_i "$r"
  fi
done
echo "  done"

# ── Phase 8: Recovery check ────────────────────────────────────────────────────
sep
echo "  Phase 8: 100 reads after full recovery…"
rec_ok=(); rec_fail=()
for i in $(seq 1 100); do
  r=$(timed_curl "${live_apps[$((RANDOM % N_LIVE))]}/api/cache/$(rand_key)")
  record rec "$r"
done
echo "  done"

# ── Summary ────────────────────────────────────────────────────────────────────
sep
echo "  RESULTS  ($N_LIVE pods live)"
sep
echo ""
echo "  Normal traffic"
stats "read  OK  " "${read2_ok[@]}"
[[ ${#read2_miss[@]} -gt 0 ]] && stats "read  MISS " "${read2_miss[@]}"
[[ ${#read2_fail[@]} -gt 0 ]] && stats "read  FAIL " "${read2_fail[@]}"
stats "write OK  " "${write2_ok[@]}"
echo ""
echo "  Dead app nodes (3 of $N_LIVE paused)"
stats "read  OK  " "${read3_ok[@]}"
[[ ${#read3_miss[@]} -gt 0 ]] && stats "read  MISS " "${read3_miss[@]}"
[[ ${#read3_fail[@]} -gt 0 ]] && stats "read  FAIL " "${read3_fail[@]}"
stats "inval OK  " "${inv3_ok[@]}"
[[ ${#inv3_fail[@]} -gt 0 ]] && stats "inval FAIL " "${inv3_fail[@]}"
echo ""
echo "  Dead sidecars (3 of $N_LIVE paused)"
stats "inval OK  " "${inv4_ok[@]}"
[[ ${#inv4_fail[@]} -gt 0 ]] && stats "inval FAIL " "${inv4_fail[@]}"
echo ""
echo "  Peer fetch (EventFetch in SQL)"
stats "fetch OK  " "${fetch_ok[@]}"
[[ ${#fetch_fail[@]} -gt 0 ]] && stats "fetch FAIL " "${fetch_fail[@]}"
echo ""
echo "  Burst (500 req)"
stats "read  OK  " "${burst_r_ok[@]}"
[[ ${#burst_r_miss[@]} -gt 0 ]] && stats "read  MISS " "${burst_r_miss[@]}"
[[ ${#burst_r_fail[@]} -gt 0 ]] && stats "read  FAIL " "${burst_r_fail[@]}"
stats "write OK  " "${burst_w_ok[@]}"
stats "inval OK  " "${burst_i_ok[@]}"
echo ""
echo "  Recovery"
stats "read  OK  " "${rec_ok[@]}"
[[ ${#rec_miss[@]} -gt 0 ]] && stats "read  MISS " "${rec_miss[@]}"
[[ ${#rec_fail[@]} -gt 0 ]] && stats "read  FAIL " "${rec_fail[@]}"
echo ""
sep
echo "  SQL breakdown by event kind:"
docker compose exec -T postgres psql -U lens -d lens -q -c \
  "SELECT
     kind,
     count(*)                                                                       AS n,
     round(avg(latency_ms)::numeric, 1)                                            AS avg_ms,
     round((percentile_cont(0.5)  WITHIN GROUP (ORDER BY latency_ms))::numeric, 1) AS p50_ms,
     round((percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms))::numeric, 1) AS p99_ms,
     sum(CASE WHEN NOT success THEN 1 ELSE 0 END)                                  AS failures
   FROM lens_events
   GROUP BY kind
   ORDER BY kind;" 2>/dev/null || echo "  (postgres not available)"
sep
echo ""
echo "  Dashboard  -> http://localhost:5173"
echo "  Re-run     -> bash example/load_test.sh"
echo ""
