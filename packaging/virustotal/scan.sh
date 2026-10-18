#!/usr/bin/env bash
set -euo pipefail

if [ -z "${VT_API_KEY:-}" ]; then
  echo "Set VT_API_KEY to scan files with VirusTotal." >&2
  exit 1
fi

api=https://www.virustotal.com/api/v3
max_direct_upload_bytes=$((32 * 1024 * 1024))
seconds_between_requests=${VT_REQUEST_INTERVAL:-16}
timeout_seconds=${VT_TIMEOUT:-1200}

vt() {
  local status=0
  curl --silent --show-error --fail-with-body \
    --retry 5 --retry-delay 30 --retry-all-errors \
    --header "x-apikey: ${VT_API_KEY}" "$@" || status=$?
  sleep "$seconds_between_requests"
  return $status
}

upload() {
  local file=$1 url="$api/files"
  if (( $(wc -c < "$file") > max_direct_upload_bytes )); then
    url=$(vt "$api/files/upload_url" | jq -r .data)
  fi
  vt --form "file=@${file}" "$url" | jq -r .data.id
}

files=("$@")
analysis_ids=()
rows=()

for i in "${!files[@]}"; do
  echo "Uploading $(basename "${files[i]}")" >&2
  analysis_ids[i]=$(upload "${files[i]}")
done

deadline=$((SECONDS + timeout_seconds))
pending=${#files[@]}
while (( pending > 0 )); do
  if (( SECONDS > deadline )); then
    echo "VirusTotal did not finish scanning within ${timeout_seconds} seconds." >&2
    exit 1
  fi
  pending=0
  for i in "${!files[@]}"; do
    [ -n "${rows[i]:-}" ] && continue
    analysis=$(vt "$api/analyses/${analysis_ids[i]}")
    if [ "$(jq -r .data.attributes.status <<<"$analysis")" = completed ]; then
      rows[i]=$(jq -r --arg name "$(basename "${files[i]}")" '
        .data.attributes.stats as $s
        | ($s.malicious + $s.suspicious + $s.undetected + $s.harmless) as $engines
        | "| `\($name)` | \($s.malicious) | \($s.suspicious) | \($engines) | [Report](https://www.virustotal.com/gui/file/\(.meta.file_info.sha256)) |"
      ' <<<"$analysis")
      echo "Scanned $(basename "${files[i]}")" >&2
    else
      pending=$((pending + 1))
    fi
  done
done

echo '<details>'
echo '<summary>VirusTotal scan</summary>'
echo
echo "Each download was scanned by [VirusTotal](https://www.virustotal.com) when this release was built."
echo
echo "| File | Malicious | Suspicious | Engines | Report |"
echo "| --- | --- | --- | --- | --- |"
printf '%s\n' "${rows[@]}"
echo
echo '</details>'
