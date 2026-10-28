// Examples of a request in the languages integrations are written in. The tester shows them for
// the request that is set up on the page, so what works there can be copied as it is.

export interface Req {
  method: string
  url: string
  body?: string // JSON text
  idempotencyKey?: string
}

export type SnippetLang = 'curl' | 'python' | 'node' | 'php' | 'go'

export const snippetLangs: { id: SnippetLang; label: string }[] = [
  { id: 'curl', label: 'curl' },
  { id: 'python', label: 'Python' },
  { id: 'node', label: 'Node.js' },
  { id: 'php', label: 'PHP' },
  { id: 'go', label: 'Go' },
]

const q = (s: string) => JSON.stringify(s)
const sq = (s: string) => "'" + s.replace(/'/g, "'\\''") + "'"

// Compact JSON of the body, or null when there is no (valid) body.
function compact(body?: string): string | null {
  if (!body?.trim()) return null
  try {
    return JSON.stringify(JSON.parse(body))
  } catch {
    return body.trim()
  }
}

export function snippet(lang: SnippetLang, r: Req): string {
  const body = compact(r.body)
  const idem = r.idempotencyKey
  switch (lang) {
    case 'curl': {
      const parts = [`curl -X ${r.method} ${sq(r.url)}`, `-H "Authorization: Bearer $VYNEL_TOKEN"`]
      if (body) parts.push(`-H "Content-Type: application/json"`)
      if (idem) parts.push(`-H "Idempotency-Key: ${idem}"`)
      if (body) parts.push(`-d ${sq(body)}`)
      return parts.join(' \\\n  ')
    }
    case 'python': {
      const lines = ['import os, requests', '', 'r = requests.request(', `    ${q(r.method)}, ${q(r.url)},`,
        '    headers={', '        "Authorization": "Bearer " + os.environ["VYNEL_TOKEN"],']
      if (idem) lines.push(`        "Idempotency-Key": ${q(idem)},`)
      lines.push('    },')
      if (body) lines.push(`    json=${pyJSON(body)},`)
      lines.push('    timeout=15,', ')', 'r.raise_for_status()', 'print(r.json() if r.content else None)')
      return lines.join('\n')
    }
    case 'node': {
      const headers = [`    Authorization: \`Bearer \${process.env.VYNEL_TOKEN}\`,`]
      if (body) headers.push(`    'Content-Type': 'application/json',`)
      if (idem) headers.push(`    'Idempotency-Key': ${q(idem)},`)
      const lines = [`const res = await fetch(${q(r.url)}, {`, `  method: ${q(r.method)},`, '  headers: {', ...headers, '  },']
      if (body) lines.push(`  body: JSON.stringify(${body}),`)
      lines.push('})', 'if (!res.ok) throw new Error(await res.text())', 'console.log(res.status === 204 ? null : await res.json())')
      return lines.join('\n')
    }
    case 'php': {
      const lines = ['<?php', `$ch = curl_init(${q(r.url)});`, 'curl_setopt_array($ch, [', `    CURLOPT_CUSTOMREQUEST => ${q(r.method)},`, '    CURLOPT_RETURNTRANSFER => true,',
        '    CURLOPT_HTTPHEADER => [', '        "Authorization: Bearer " . getenv("VYNEL_TOKEN"),']
      if (body) lines.push('        "Content-Type: application/json",')
      if (idem) lines.push(`        "Idempotency-Key: ${idem}",`)
      lines.push('    ],')
      if (body) lines.push(`    CURLOPT_POSTFIELDS => ${sq(body)},`)
      lines.push(']);', '$out = curl_exec($ch);', '$code = curl_getinfo($ch, CURLINFO_HTTP_CODE);', 'echo $code, " ", $out;')
      return lines.join('\n')
    }
    case 'go': {
      const lines = ['package main', '', 'import (', '\t"fmt"', '\t"io"', '\t"net/http"', '\t"os"']
      if (body) lines.push('\t"strings"')
      lines.push(')', '', 'func main() {')
      lines.push(body ? `\treq, _ := http.NewRequest(${q(r.method)}, ${q(r.url)}, strings.NewReader(\`${body}\`))` : `\treq, _ := http.NewRequest(${q(r.method)}, ${q(r.url)}, nil)`)
      lines.push('\treq.Header.Set("Authorization", "Bearer "+os.Getenv("VYNEL_TOKEN"))')
      if (body) lines.push('\treq.Header.Set("Content-Type", "application/json")')
      if (idem) lines.push(`\treq.Header.Set("Idempotency-Key", ${q(idem)})`)
      lines.push('\tres, err := http.DefaultClient.Do(req)', '\tif err != nil {', '\t\tpanic(err)', '\t}', '\tdefer res.Body.Close()',
        '\tout, _ := io.ReadAll(res.Body)', '\tfmt.Println(res.StatusCode, string(out))', '}')
      return lines.join('\n')
    }
  }
}

// Python spells true, false and null differently.
function pyJSON(json: string): string {
  try {
    return toPy(JSON.parse(json))
  } catch {
    return json
  }
}
function toPy(v: unknown): string {
  if (v === null) return 'None'
  if (v === true) return 'True'
  if (v === false) return 'False'
  if (Array.isArray(v)) return '[' + v.map(toPy).join(', ') + ']'
  if (typeof v === 'object') return '{' + Object.entries(v as object).map(([k, x]) => `${q(k)}: ${toPy(x)}`).join(', ') + '}'
  return JSON.stringify(v)
}

// How to check a webhook, in each language: the receiver must reject what is not signed.
export const verifySnippets: Record<Exclude<SnippetLang, 'curl'>, string> = {
  python: `import hmac, hashlib, time

def verify(secret: str, headers, raw_body: bytes) -> bool:
    ts = headers["X-Vynel-Timestamp"]
    if abs(time.time() - int(ts)) > 300:      # a replay of an old delivery
        return False
    mac = hmac.new(secret.encode(), ts.encode() + b"." + raw_body, hashlib.sha256).hexdigest()
    return hmac.compare_digest("sha256=" + mac, headers["X-Vynel-Signature"])`,
  node: `import { createHmac, timingSafeEqual } from 'node:crypto'

export function verify(secret, headers, rawBody /* Buffer */) {
  const ts = headers['x-vynel-timestamp']
  if (Math.abs(Date.now() / 1000 - Number(ts)) > 300) return false // a replay of an old delivery
  const mac = 'sha256=' + createHmac('sha256', secret).update(ts + '.').update(rawBody).digest('hex')
  const a = Buffer.from(mac), b = Buffer.from(headers['x-vynel-signature'] ?? '')
  return a.length === b.length && timingSafeEqual(a, b)
}`,
  php: `<?php
function verify(string $secret, array $h, string $raw): bool {
    $ts = $h['X-Vynel-Timestamp'] ?? '';
    if (abs(time() - (int)$ts) > 300) return false;          // a replay of an old delivery
    $mac = 'sha256=' . hash_hmac('sha256', $ts . '.' . $raw, $secret);
    return hash_equals($mac, $h['X-Vynel-Signature'] ?? '');
}`,
  go: `func verify(secret string, h http.Header, raw []byte) bool {
	ts := h.Get("X-Vynel-Timestamp")
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || math.Abs(float64(time.Now().Unix()-n)) > 300 { // a replay of an old delivery
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(raw)
	return hmac.Equal([]byte("sha256="+hex.EncodeToString(m.Sum(nil))), []byte(h.Get("X-Vynel-Signature")))
}`,
}
