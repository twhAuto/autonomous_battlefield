#!/usr/bin/env bash
# Records the build host's CPU, memory and pressure-stall figures while checks run, so a
# check that fails on a time limit can be told apart from one that ran on a starved host.
#
#   record-host-load.sh record OUTPUT   sample until stopped or HOST_LOAD_MAX_SECONDS pass
#   record-host-load.sh finish OUTPUT   stop that recorder and print a one-line summary

set -Eeuo pipefail

mode="${1:-}"
output="${2:-}"
if [[ ("${mode}" != record && "${mode}" != finish) || -z "${output}" ]]; then
  echo "usage: record-host-load.sh record|finish OUTPUT" >&2
  exit 64
fi

proc="${HOST_LOAD_PROC:-/proc}"
interval="${HOST_LOAD_INTERVAL_SECONDS:-5}"
max_seconds="${HOST_LOAD_MAX_SECONDS:-2700}"
pid_file="${output}.pid"

# Jiffies since boot: all states, idle, and waiting for I/O.
cpu_counters() {
  awk '/^cpu /{print $2+$3+$4+$5+$6+$7+$8+$9, $5, $6; exit}' "${proc}/stat"
}

# Share of the last 10 seconds in which some task waited for the resource; "-" without PSI.
pressure() {
  [[ -r "${proc}/pressure/$1" ]] || { echo -; return; }
  awk '/^some/{sub("avg10=", "", $2); print $2; exit}' "${proc}/pressure/$1"
}

record() {
  local total idle iowait prev_total prev_idle prev_iowait deadline
  mkdir -p "$(dirname -- "${output}")"
  echo "$$" >"${pid_file}"
  read -r prev_total prev_idle prev_iowait < <(cpu_counters)
  printf 'time\tload1\tcpu_busy_pct\tiowait_pct\tmem_available_mib\tswap_used_mib\tpsi_cpu\tpsi_memory\tpsi_io\n' >"${output}"
  deadline=$((SECONDS + max_seconds))
  while ((SECONDS < deadline)); do
    sleep "${interval}"
    read -r total idle iowait < <(cpu_counters)
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      "$(cut -d' ' -f1 "${proc}/loadavg")" \
      "$(awk -v t="$((total - prev_total))" -v i="$((idle - prev_idle))" -v w="$((iowait - prev_iowait))" \
        'BEGIN { if (t <= 0) { printf "0.0\t0.0"; exit } printf "%.1f\t%.1f", 100 * (t - i - w) / t, 100 * w / t }')" \
      "$(awk '/^MemAvailable:/{a=$2} /^SwapTotal:/{t=$2} /^SwapFree:/{f=$2} END{printf "%d\t%d", a / 1024, (t - f) / 1024}' "${proc}/meminfo")" \
      "$(pressure cpu)" "$(pressure memory)" "$(pressure io)" >>"${output}"
    prev_total="${total}" prev_idle="${idle}" prev_iowait="${iowait}"
  done
}

finish() {
  if [[ -r "${pid_file}" ]]; then
    kill "$(<"${pid_file}")" 2>/dev/null || true
    rm -f -- "${pid_file}"
  fi
  [[ -r "${output}" ]] || { echo "Host load: no record at ${output}."; return; }
  awk -F'\t' -v cpus="$(nproc)" '
    function peak(value, current) { return (value != "-" && value + 0 > current) ? value + 0 : current }
    NR == 1 { next }
    {
      samples++; busy += $3
      busy_peak = peak($3, busy_peak); iowait_peak = peak($4, iowait_peak); load_peak = peak($2, load_peak)
      if (samples == 1 || $5 + 0 < memory_low) memory_low = $5 + 0
      swap_peak = peak($6, swap_peak)
      if ($7 != "-") pressure_seen = 1
      cpu_peak = peak($7, cpu_peak); memory_peak = peak($8, memory_peak); io_peak = peak($9, io_peak)
    }
    END {
      if (!samples) { print "Host load: no samples were recorded."; exit }
      printf "Host load over %d samples on %d CPUs: CPU busy average %.0f%%, peak %.0f%%; I/O wait peak %.0f%%; load peak %.1f; least memory available %d MiB; most swap used %d MiB", \
        samples, cpus, busy / samples, busy_peak, iowait_peak, load_peak, memory_low, swap_peak
      if (pressure_seen) printf "; pressure peaks: CPU %.0f%%, memory %.0f%%, I/O %.0f%%", cpu_peak, memory_peak, io_peak
      print "."
    }' "${output}"
}

"${mode}"
