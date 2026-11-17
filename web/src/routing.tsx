import { ClientRouting, get, Profile } from './api'
import { Field, Icon, Info, Switch, useLoad } from './ui'
import { t, Trans } from './i18n'

const lines = (v: string[]) => v.join('\n')
const parse = (s: string) => s.split('\n').map((x) => x.trim())

// Пресеты — типовые комбинации переключателей; списки и DNS не трогают, сохранение руками.
const presets: { key: string; label: () => string; apply: (r: ClientRouting) => ClientRouting }[] = [
  { key: 'runet', label: () => t('routing.preset_runet'), apply: (r) => ({ ...r, ruDirect: true, blockAds: false, torrent: 'direct' }) },
  { key: 'allvpn', label: () => t('routing.preset_all_vpn'), apply: (r) => ({ ...r, ruDirect: false, blockAds: false, torrent: 'proxy' }) },
  { key: 'antiads', label: () => t('routing.preset_antiads'), apply: (r) => ({ ...r, ruDirect: true, blockAds: true, torrent: 'block' }) },
]

// RoutingForm edits how client apps route traffic: Russian sites past the tunnel, DNS for direct
// and tunnelled names, torrents and own lists. Блоки сворачиваются; пресеты сверху ставят
// типовую комбинацию одним кликом.
export function RoutingForm({ r, set }: { r: ClientRouting; set: (r: ClientRouting) => void }) {
  const list = (k: 'direct' | 'proxy' | 'block' | 'dnsDirect' | 'dnsProxy') => ({
    value: lines(r[k]),
    onChange: (e: React.ChangeEvent<HTMLTextAreaElement>) => set({ ...r, [k]: parse(e.target.value) }),
  })
  return (
    <>
      <div className="row" style={{ gap: 6, marginBottom: 14, flexWrap: 'wrap', alignItems: 'center' }}>
        <span className="muted small">{t('routing.presets')}</span>
        {presets.map((p) => (
          <button key={p.key} type="button" className="chip-btn" onClick={() => set(p.apply(r))}>{p.label()}</button>
        ))}
      </div>

      <details className="form-section" open>
        <summary>{t('routing.block_direct_title')}</summary>
        <div className="form-section-body">
          <label className="row" style={{ gap: 10, marginBottom: 6 }}>
            <Switch checked={r.ruDirect} onChange={(v) => set({ ...r, ruDirect: v })} />
            <span>
              <b>{t('routing.russia_direct')}<Info>{t('routing.russian_sites_geosite_category')}</Info></b>
              <div className="small muted">{t('routing.ru_direct_consequence')}</div>
            </span>
          </label>
          <Field label={t('routing.always_direct')} help={t('routing.one_per_line')}>
            <textarea className="input mono" rows={4} {...list('direct')} placeholder={'domain:example.ru\ngeoip:by'} />
          </Field>
        </div>
      </details>

      <details className="form-section" open>
        <summary>{t('routing.block_proxy_title')}<span className="muted small">{t('routing.proxy_priority_note')}</span></summary>
        <div className="form-section-body">
          <Field label={t('routing.always_through_the_tunnel')} help={t('routing.one_per_line')}>
            <textarea className="input mono" rows={4} {...list('proxy')} placeholder={'domain:youtube.com\ngeosite:telegram'} />
          </Field>
        </div>
      </details>

      <details className="form-section">
        <summary>{t('routing.block_block_title')}</summary>
        <div className="form-section-body">
          <div className="grid2">
            <Field label={t('routing.ads')} help={t('routing.geosite_category_ads_all_block')}>
              <label className="row" style={{ gap: 10, height: 36 }}>
                <Switch checked={r.blockAds} onChange={(v) => set({ ...r, blockAds: v })} /> <span>{t('routing.block_ads')}</span>
              </label>
            </Field>
            <Field label={t('routing.torrents')} info={t('routing.bittorrent_mihomo_cannot_detect')}>
              <select className="input" value={r.torrent} onChange={(e) => set({ ...r, torrent: e.target.value as ClientRouting['torrent'] })}>
                <option value="direct">{t('routing.direct')}</option>
                <option value="block">{t('routing.block')}</option>
                <option value="proxy">{t('routing.through_the_tunnel')}</option>
              </select>
            </Field>
          </div>
          <Field label={t('routing.block_2')} help={t('routing.one_per_line')}>
            <textarea className="input mono" rows={4} {...list('block')} placeholder={'domain:ads.example.com'} />
          </Field>
        </div>
      </details>

      <details className="form-section">
        <summary>{t('routing.block_dns_title')}</summary>
        <div className="form-section-body">
          <div className="grid2">
            <Field label={t('routing.dns_for_direct_sites')} help={t('routing.one_per_line')} info={t('routing.dns_info')}>
              <textarea className="input mono" rows={3} {...list('dnsDirect')} placeholder={'localhost\n77.88.8.8'} />
            </Field>
            <Field label={t('routing.dns_through_the_tunnel')} help={t('routing.one_per_line')} info={t('routing.tunnel_dns_info')}>
              <textarea className="input mono" rows={3} {...list('dnsProxy')} placeholder={'https://1.1.1.1/dns-query\nhttps://8.8.8.8/dns-query'} />
            </Field>
          </div>
        </div>
      </details>

      <div className="small muted" style={{ marginTop: 4 }}><Trans k="routing.lists_take_one_entry_per" /></div>
    </>
  )
}

// RoutingChain — постоянная схема «кто кого перекрывает» над формой маршрутов:
// базовые маршруты подписки → профили со своими маршрутами → ограничения приложений.
export function RoutingChain({ r, onTest }: { r: ClientRouting; onTest?: () => void }) {
  const profiles = useLoad(() => get<Profile[]>('profiles'))
  const custom = (profiles.data || []).filter((p) => p.clientCustom)
  return (
    <div className="card card-pad">
      <div className="row between" style={{ marginBottom: 10, flexWrap: 'wrap', gap: 8 }}>
        <div className="label">{t('routing.chain_title')}</div>
        {onTest && (
          <button className="btn sm" onClick={onTest}><Icon name="check" size={16} />{' '}{t('routing.check_in_app')}</button>
        )}
      </div>
      <div className="routing-chain">
        <div className="chain-layer">
          <span className="chain-node">{t('routing.chain_sub')}</span>
          <div className="chain-desc">{t('routing.chain_sub_desc')}</div>
        </div>
        <span className="chain-arrow">→</span>
        <div className="chain-layer">
          <span className={'chain-node' + (custom.length ? ' warn' : '')}>{t('routing.chain_profiles')}</span>
          <div className="chain-desc">
            {custom.length === 0 ? t('routing.chain_profiles_none') : (
              <>
                {t('routing.chain_profiles_override')}{' '}
                {custom.map((p) => <a key={p.id} className="chip" href="#/profiles">{p.name}</a>)}
              </>
            )}
          </div>
        </div>
        <span className="chain-arrow">→</span>
        <div className="chain-layer">
          <span className="chain-node">{t('routing.chain_app')}</span>
          <div className="chain-desc">
            <div>Happ: {t('routing.chain_happ_limit')}{' '}<Info>{t('subscription.happ_routing_limits')}</Info></div>
            <div>Karing: {t('routing.chain_karing_limit')}{' '}<Info>{t('subscription.karing_routing_setup')}</Info></div>
          </div>
        </div>
      </div>
      {r.fragment?.enabled && <div className="small muted" style={{ marginTop: 10 }}>{t('routing.chain_fragment_on')}</div>}
    </div>
  )
}
