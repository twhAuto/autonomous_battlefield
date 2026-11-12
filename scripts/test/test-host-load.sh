#!/usr/bin/env bash
# Checks that record-host-load.sh turns /proc counters into busy and I/O-wait shares,
# stops when asked, tolerates a kernel without pressure files, and summarizes its record.

set -Eeuo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
recorder="${project_dir}/scripts/ci/record-host-load.sh"
test_dir="$(mktemp -d "${TMPDIR:-/tmp}/battlefield-host-load-test.XXXXXX")"
cleanup() { rm -rf -- "${test_dir}"; }
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# A fixed /proc with counters past 2^31, as on a host that has been up for weeks: between
# the recorder's two reads, 1000 jiffies pass with 150 idle and 50 in I/O wait.
proc="${test_dir}/proc"
mkdir -p "${proc}/pressure"
echo 'cpu  3000001000 0 500 9000008000 500 0 0 0 0 0' >"${proc}/stat"
echo '7.50 3.00 1.00 9/900 1234' >"${proc}/loadavg"
printf 'MemAvailable:    2097152 kB\nSwapTotal:       1048576 kB\nSwapFree:         524288 kB\n' >"${proc}/meminfo"
for resource in cpu memory io; do
  echo 'some avg10=40.00 avg60=1.00 avg300=0.50 total=1' >"${proc}/pressure/${resource}"
done
echo 'some avg10=2.50 avg60=1.00 avg300=0.50 total=1' >"${proc}/pressure/memory"

record="${test_dir}/out/host-load.tsv"
HOST_LOAD_PROC="${proc}" HOST_LOAD_INTERVAL_SECONDS=1 "${recorder}" record "${record}" &
for _ in $(seq 50); do [[ -s "${record}" ]] && break; sleep 0.1; done
echo 'cpu  3000001500 0 800 9000008150 550 0 0 0 0 0' >"${proc}/stat"
for _ in $(seq 50); do (($(wc -l <"${record}") >= 2)) && break; sleep 0.1; done

summary="$(HOST_LOAD_PROC="${proc}" "${recorder}" finish "${record}")"
wait || true
[[ ! -e "${record}.pid" ]] || fail "finish left the pid file behind"
[[ "$(sed -n 2p "${record}" | cut -f2-)" == $'7.50\t80.0\t5.0\t2048\t512\t40.00\t2.50\t40.00' ]] ||
  fail "unexpected first sample: $(sed -n 2p "${record}")"
[[ "${summary}" == *"CPU busy average"*"peak 80%"*"I/O wait peak 5%"*"load peak 7.5"*"least memory available 2048 MiB"*"most swap used 512 MiB"*"pressure peaks: CPU 40%, memory 2%, I/O 40%." ]] ||
  fail "unexpected summary: ${summary}"

# Without pressure files the columns hold "-" and the summary leaves pressure out.
rm -r "${proc}/pressure"
HOST_LOAD_PROC="${proc}" HOST_LOAD_INTERVAL_SECONDS=1 HOST_LOAD_MAX_SECONDS=1 "${recorder}" record "${record}"
[[ "$(sed -n 2p "${record}" | cut -f7-)" == $'-\t-\t-' ]] || fail "missing pressure files were not recorded as '-'"
summary="$("${recorder}" finish "${record}")"
[[ "${summary}" == "Host load over 1 samples"* && "${summary}" != *"pressure peaks"* ]] || fail "unexpected summary without pressure: ${summary}"

# A build that never started the recorder still gets a clear line and a zero exit.
[[ "$("${recorder}" finish "${test_dir}/absent.tsv")" == "Host load: no record at "* ]] || fail "missing record was not reported"
if "${recorder}" record 2>/dev/null; then fail "a missing output path was accepted"; fi

echo "Host load recorder tests passed."
