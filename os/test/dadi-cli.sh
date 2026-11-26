#!/usr/bin/env bash
# Runs usr/bin/dadi against a stub of the Nas API; usage: test/dadi-cli.sh
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
port=18092
python3 -I "$here/dadi-cli-stub.py" "$port" "$work/requests.log" &
stub=$!
trap 'kill "$stub" 2>/dev/null || true; rm -rf "$work"' EXIT
sed "s#http://127.0.0.1:8092#http://127.0.0.1:$port#" "$here/../usr/bin/dadi" >"$work/dadi"
chmod +x "$work/dadi"
for _ in $(seq 1 50); do curl -s "http://127.0.0.1:$port/access" >/dev/null 2>&1 && break; sleep 0.1; done

dadi() { "$work/dadi" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
expect() { grep -qF -- "$2" <<<"$1" || fail "expected '$2' in: $1"; }

out="$(dadi users)"
expect "$out" "ankur	password set"
expect "$out" "tpm: enrolled (PCR 7)"

expect "$(printf 'hunter2\n' | dadi users set newuser)" "user newuser added"
expect "$(printf 'hunter3\n' | dadi users set ankur)" "password saved for ankur"
grep -qF 'POST /access/users {"username":"newuser","password":"hunter2"}' "$work/requests.log" || fail "user request body"
expect "$(dadi users remove newuser)" "user newuser removed"

out="$(dadi keys)"
expect "$out" "ANTHROPIC_API_KEY    …3456"
expect "$out" "GEMINI_API_KEY       (not set)"
expect "$out" "CHAAVI_EMAIL         me@example.com"
expect "$out" "CHAAVI_PASSWORD      (not set)"
if grep -qF 'sk-ant-abcdef123456' <<<"$out"; then fail "key value printed unmasked"; fi

expect "$(printf 'AIza-new\n' | dadi keys set GEMINI_API_KEY)" "GEMINI_API_KEY saved; dwar restarted"
last_put="$(grep '^PUT /modules/dwar/settings' "$work/requests.log" | tail -1)"
expect "$last_put" '"ANTHROPIC_API_KEY":"sk-ant-abcdef123456"'
expect "$last_put" '"GEMINI_API_KEY":"AIza-new"'
expect "$(dadi keys unset GEMINI_API_KEY)" "GEMINI_API_KEY saved; dwar restarted"
expect "$(printf 'pw\n' | dadi keys set CHAAVI_PASSWORD)" "chaavi restarted"
if dadi keys set NOT_A_KEY </dev/null 2>"$work/err"; then fail "unknown key accepted"; fi
expect "$(cat "$work/err")" "unknown key NOT_A_KEY"
if printf '\n' | dadi keys set OPENAI_API_KEY 2>"$work/err"; then fail "empty value accepted"; fi
expect "$(cat "$work/err")" "empty value"

out="$(dadi devices)"
expect "$out" "mac	online	100.64.0.8"
expect "$out" "phone	pending"
out="$(dadi devices add tablet)"
expect "$out" "BUNDLE-tablet"
if dadi devices add taken 2>"$work/err"; then fail "conflict accepted"; fi
expect "$(cat "$work/err")" "node name taken (HTTP 409)"

if "$work/dadi" 2>/dev/null; then fail "no-arg run should fail"; fi
echo "dadi cli ok"
