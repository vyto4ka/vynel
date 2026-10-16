import { ClientRouting } from './api'
import { Field, Switch } from './ui'

const lines = (v: string[]) => v.join('\n')
const parse = (s: string) => s.split('\n').map((x) => x.trim())

// RoutingForm edits how client apps route traffic: Russian sites past the tunnel, DNS for direct
// and tunnelled names, torrents and own lists.
export function RoutingForm({ r, set }: { r: ClientRouting; set: (r: ClientRouting) => void }) {
  const list = (k: 'direct' | 'proxy' | 'block' | 'dnsDirect' | 'dnsProxy') => ({
    value: lines(r[k]),
    onChange: (e: React.ChangeEvent<HTMLTextAreaElement>) => set({ ...r, [k]: parse(e.target.value) }),
  })
  return (
    <>
      <label className="row" style={{ gap: 10, marginBottom: 12 }}>
        <Switch checked={r.ruDirect} onChange={(v) => set({ ...r, ruDirect: v })} />
        <span>
          <b>RU напрямую</b>
          <div className="small text-2">Российские сайты (geosite:category-ru, .ru, .su, .рф) и адреса (geoip:ru) идут сразу с устройства, мимо туннеля</div>
        </span>
      </label>
      <div className="grid2">
        <Field label="DNS для прямых сайтов" help="По одному в строке: localhost — DNS устройства, IP (77.88.8.8 — Яндекс) или DoH https://…">
          <textarea className="input mono" rows={3} {...list('dnsDirect')} placeholder={'localhost\n77.88.8.8'} />
        </Field>
        <Field label="DNS через туннель" help="Для всего остального: IP или DoH. Запросы идут через сервер">
          <textarea className="input mono" rows={3} {...list('dnsProxy')} placeholder={'https://1.1.1.1/dns-query\nhttps://8.8.8.8/dns-query'} />
        </Field>
      </div>
      <div className="grid2">
        <Field label="Торренты" help="BitTorrent: Mihomo не умеет его распознавать, там правило не действует">
          <select className="input" value={r.torrent} onChange={(e) => set({ ...r, torrent: e.target.value as ClientRouting['torrent'] })}>
            <option value="direct">напрямую</option>
            <option value="block">блокировать</option>
            <option value="proxy">через туннель</option>
          </select>
        </Field>
        <Field label="Реклама" help="geosite:category-ads-all → блок">
          <label className="row" style={{ gap: 10, height: 36 }}>
            <Switch checked={r.blockAds} onChange={(v) => set({ ...r, blockAds: v })} /> <span>блокировать рекламу</span>
          </label>
        </Field>
      </div>
      <div className="grid3">
        <Field label="Всегда напрямую">
          <textarea className="input mono" rows={4} {...list('direct')} placeholder={'domain:example.ru\ngeoip:by'} />
        </Field>
        <Field label="Всегда через туннель" help="Важнее «RU напрямую» и прямого списка">
          <textarea className="input mono" rows={4} {...list('proxy')} placeholder={'domain:youtube.com\ngeosite:telegram'} />
        </Field>
        <Field label="Блокировать">
          <textarea className="input mono" rows={4} {...list('block')} placeholder={'domain:ads.example.com'} />
        </Field>
      </div>
      <div className="small text-2">
        Списки — по одной записи в строке, как в Xray: <span className="mono">domain:</span> (домен с поддоменами, можно без префикса), <span className="mono">full:</span>,{' '}
        <span className="mono">keyword:</span>, <span className="mono">regexp:</span>, <span className="mono">geosite:</span>, <span className="mono">geoip:</span>, IP или подсеть.
        Кириллические домены — в punycode (.рф → xn--p1ai).
      </div>
    </>
  )
}
