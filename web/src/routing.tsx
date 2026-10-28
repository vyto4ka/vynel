import { ClientRouting } from './api'
import { Field, Info, Switch } from './ui'
import { t, Trans } from './i18n'

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
          <b>{t('routing.russia_direct')}<Info>{t('routing.russian_sites_geosite_category')}</Info></b>
        </span>
      </label>
      <div className="grid2">
        <Field label={t('routing.dns_for_direct_sites')} help={t('routing.one_per_line')} info={t('routing.dns_info')}>
          <textarea className="input mono" rows={3} {...list('dnsDirect')} placeholder={'localhost\n77.88.8.8'} />
        </Field>
        <Field label={t('routing.dns_through_the_tunnel')} help={t('routing.one_per_line')} info={t('routing.tunnel_dns_info')}>
          <textarea className="input mono" rows={3} {...list('dnsProxy')} placeholder={'https://1.1.1.1/dns-query\nhttps://8.8.8.8/dns-query'} />
        </Field>
      </div>
      <div className="grid2">
        <Field label={t('routing.torrents')} info={t('routing.bittorrent_mihomo_cannot_detect')}>
          <select className="input" value={r.torrent} onChange={(e) => set({ ...r, torrent: e.target.value as ClientRouting['torrent'] })}>
            <option value="direct">{t('routing.direct')}</option>
            <option value="block">{t('routing.block')}</option>
            <option value="proxy">{t('routing.through_the_tunnel')}</option>
          </select>
        </Field>
        <Field label={t('routing.ads')} help={t('routing.geosite_category_ads_all_block')}>
          <label className="row" style={{ gap: 10, height: 36 }}>
            <Switch checked={r.blockAds} onChange={(v) => set({ ...r, blockAds: v })} /> <span>{t('routing.block_ads')}</span>
          </label>
        </Field>
      </div>
      <div className="label" style={{ margin: '4px 0 8px' }}>{t('routing.rule_lists')}<Info><Trans k="routing.lists_take_one_entry_per" /></Info></div>
      <div className="grid3">
        <Field label={t('routing.always_direct')}>
          <textarea className="input mono" rows={4} {...list('direct')} placeholder={'domain:example.ru\ngeoip:by'} />
        </Field>
        <Field label={t('routing.always_through_the_tunnel')} help={t('routing.takes_priority_over_russia')}>
          <textarea className="input mono" rows={4} {...list('proxy')} placeholder={'domain:youtube.com\ngeosite:telegram'} />
        </Field>
        <Field label={t('routing.block_2')}>
          <textarea className="input mono" rows={4} {...list('block')} placeholder={'domain:ads.example.com'} />
        </Field>
      </div>
    </>
  )
}
