// Приёмник вебхуков Vynel на node:http, без зависимостей.
//
//   export VYNEL_WEBHOOK_SECRET=whsec_...
//   node webhook_receiver.mjs 8000
//
// Подпись считается по СЫРОМУ телу, поэтому тело читается как есть, до разбора JSON.
import { createServer } from 'node:http'
import { createHmac, timingSafeEqual } from 'node:crypto'

const secret = process.env.VYNEL_WEBHOOK_SECRET
const seen = new Set() // id обработанных событий; в настоящей программе — таблица в базе

function verify(headers, raw) {
  const ts = headers['x-vynel-timestamp'] ?? ''
  if (!/^\d+$/.test(ts) || Math.abs(Date.now() / 1000 - Number(ts)) > 300) return false // повтор перехваченного
  const mac = 'sha256=' + createHmac('sha256', secret).update(ts + '.').update(raw).digest('hex')
  const got = Buffer.from(headers['x-vynel-signature'] ?? '')
  const want = Buffer.from(mac)
  return got.length === want.length && timingSafeEqual(got, want)
}

function handle(e) {
  const who = e.entity.username ?? e.entity.code
  console.log(e.type, who, e.data)
}

createServer(async (req, res) => {
  const chunks = []
  for await (const c of req) chunks.push(c)
  const raw = Buffer.concat(chunks)
  if (req.method !== 'POST' || !verify(req.headers, raw)) {
    res.writeHead(401).end()
    return
  }
  const event = JSON.parse(raw)
  if (event.type !== 'ping' && !seen.has(event.id)) {
    seen.add(event.id)
    handle(event)
  }
  res.writeHead(204).end() // быстро и 2xx, иначе панель повторит позже
}).listen(Number(process.argv[2] ?? 8000), () => console.log('слушаю'))
