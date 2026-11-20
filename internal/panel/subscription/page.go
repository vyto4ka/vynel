package subscription

import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/vyto4ka/vynel/internal/panel/service"
	"github.com/vyto4ka/vynel/internal/panel/store"
)

// The page shows the subscription URL (QR and deep links), never raw server links: those would
// bypass the HWID limit (docs/ARCHITECTURE.md §8.4). Its look comes from service.SubPage.
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="{{.Lang}}" translate="no" class="notranslate"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex"><meta name="google" content="notranslate"><title>{{.Heading}}</title>
<style>
:root{--accent:{{.Accent}}}
body{--bg:#191c20;--card:#23272c;--card2:#2b3036;--card3:#373b3e;--ink:#e6ecf0;--muted:#9ba8b4;--line:#393f46;--outline:#5a646e;
 --primary:color-mix(in oklab,var(--accent) 78%,#fff);--on-primary:#072224;
 --primary-soft:color-mix(in oklab,var(--accent) 16%,transparent);
 --pink:#f04a9d;--pink-soft:rgba(240,74,157,.16);
 --good:var(--primary);--bad:#f04a9d;--bad-bg:rgba(225,40,133,.16);
 --sh:0 4px 16px rgba(0,0,0,.28);color-scheme:dark}
body.light{--bg:#edf0f3;--card:#ffffff;--card2:#f3f5f8;--card3:#e6eaef;--ink:#24292e;--muted:#5c6670;--line:#d8dee5;--outline:#96a1ad;
 --primary:color-mix(in oklab,var(--accent) 70%,#000);--on-primary:#fff;
 --primary-soft:color-mix(in oklab,var(--accent) 13%,transparent);
 --pink:#d6247c;--pink-soft:rgba(214,36,124,.12);
 --bad:#c81d74;--bad-bg:rgba(200,29,116,.12);
 --sh:0 2px 10px rgba(36,41,46,.08);color-scheme:light}
@media (prefers-color-scheme:light){body.auto{--bg:#edf0f3;--card:#ffffff;--card2:#f3f5f8;--card3:#e6eaef;--ink:#24292e;--muted:#5c6670;--line:#d8dee5;--outline:#96a1ad;
 --primary:color-mix(in oklab,var(--accent) 70%,#000);--on-primary:#fff;
 --primary-soft:color-mix(in oklab,var(--accent) 13%,transparent);
 --pink:#d6247c;--pink-soft:rgba(214,36,124,.12);
 --bad:#c81d74;--bad-bg:rgba(200,29,116,.12);
 --sh:0 2px 10px rgba(36,41,46,.08);color-scheme:light}}
*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
body{margin:0;min-height:100vh;background:var(--bg);color:var(--ink);font:14.5px/1.5 "Inter",system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;-webkit-font-smoothing:antialiased}
main{max-width:640px;margin:0 auto;padding:20px 16px calc(36px + env(safe-area-inset-bottom))}
svg{width:17px;height:17px;flex:none;fill:none;stroke:currentColor;stroke-width:1.9;stroke-linecap:round;stroke-linejoin:round}

.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:18px;margin-bottom:14px}
.card-head{display:flex;align-items:center;justify-content:space-between;gap:10px;margin-bottom:14px;flex-wrap:wrap}
h1{font-size:21px;line-height:26px;font-weight:600;margin:0;letter-spacing:-.2px}
h2{font-size:15.5px;line-height:22px;font-weight:600;margin:0}
.muted{color:var(--muted);font-size:13px}.desc{color:var(--muted);white-space:pre-line;font-size:14px}

.hero{display:flex;align-items:center;justify-content:space-between;gap:14px;flex-wrap:wrap}
.hero-left{display:flex;align-items:center;gap:14px;min-width:0}
.avatar{width:46px;height:46px;border-radius:10px;flex:none;display:grid;place-items:center;
 background:linear-gradient(135deg,var(--primary) 0%,var(--pink) 100%);color:#fff;font-size:20px;font-weight:700}
.chip{display:inline-flex;align-items:center;gap:6px;height:26px;padding:0 10px;border-radius:7px;font-size:12.5px;font-weight:500;background:var(--primary-soft);color:var(--primary);white-space:nowrap}
.chip:before{content:"";width:6px;height:6px;border-radius:50%;background:currentColor}
.chip.bad{background:var(--bad-bg);color:var(--bad)}
.chip.pink{background:var(--pink-soft);color:var(--pink)}
.chip.pink:before{display:none}

.ann{display:flex;gap:11px;align-items:flex-start;border-radius:10px;padding:13px 15px;margin-bottom:14px;
 background:var(--primary-soft);border:1px solid color-mix(in oklab,var(--primary) 30%,transparent);color:var(--ink);text-decoration:none;white-space:pre-line;font-size:13.5px}
.ann svg{color:var(--primary);margin-top:2px}

.kpis{display:grid;grid-template-columns:1fr 1fr;gap:12px;margin-bottom:14px}
.kpi{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:15px 16px}
.kpi .k{color:var(--muted);font-size:12px;margin-bottom:4px}
.kpi .v{font-size:17px;font-weight:600;font-variant-numeric:tabular-nums;line-height:1.3}
.kpi .s{color:var(--muted);font-size:12px;margin-top:4px}
.bar{height:5px;border-radius:3px;background:var(--card3);overflow:hidden;margin-top:8px}
.bar i{display:block;height:100%;border-radius:3px;background:var(--primary)}

/* Traffic chart */
.chart-info{font-size:12.5px;color:var(--muted);font-variant-numeric:tabular-nums}
.chart-info b{color:var(--ink);font-weight:600}
.chart-bars{display:grid;grid-template-columns:repeat(14,minmax(0,1fr));gap:5px;align-items:end;height:108px;padding:6px 0 2px}
.day-col{display:flex;flex-direction:column;align-items:center;gap:5px;height:100%;justify-content:flex-end;cursor:pointer;border:0;background:none;padding:0;color:inherit;font:inherit}
.day-track{width:100%;height:84px;border-radius:5px;background:var(--card2);display:flex;flex-direction:column;justify-content:flex-end;overflow:hidden;padding:1px;transition:background .15s,box-shadow .15s}
.day-col:hover .day-track,.day-col.on .day-track{box-shadow:0 0 0 1.5px var(--pink)}
.day-up{width:100%;background:var(--pink);border-radius:3px 3px 0 0;min-height:0}
.day-down{width:100%;background:var(--primary);border-radius:2px 2px 3px 3px;min-height:0}
.day-lbl{font-size:10px;color:var(--muted);font-variant-numeric:tabular-nums;line-height:1}
.day-col.on .day-lbl{color:var(--ink);font-weight:600}
.legend{display:flex;gap:14px;font-size:12px;color:var(--muted);margin-top:10px;align-items:center;flex-wrap:wrap}
.legend i{display:inline-block;width:9px;height:9px;border-radius:3px;margin-right:5px;vertical-align:-1px}

/* Connect: QR + copy bar */
.connect-grid{display:grid;grid-template-columns:156px minmax(0,1fr);gap:16px;align-items:center}
.qr-wrap{background:#fff;padding:8px;border-radius:10px;width:156px;height:156px;margin:0 auto}
.qr-wrap img{display:block;width:100%;height:100%}
.copy{display:flex;gap:6px;align-items:center;background:var(--bg);border:1px solid var(--line);border-radius:8px;padding:5px 5px 5px 11px}
.copy input{flex:1;min-width:0;border:0;background:none;color:var(--primary);font:12.5px "JetBrains Mono",ui-monospace,monospace;outline:none;padding:6px 0}

.btn,button.btn{display:inline-flex;align-items:center;justify-content:center;gap:7px;height:36px;padding:0 14px;border-radius:8px;border:1px solid transparent;
 background:var(--primary);color:var(--on-primary);text-decoration:none;font:600 13.5px/1 inherit;cursor:pointer;white-space:nowrap;transition:filter .15s,background .15s,border-color .15s}
.btn:hover{filter:brightness(1.06)}
.btn:active{filter:brightness(.95)}
.btn.sec{background:var(--card2);color:var(--ink);border-color:var(--line);font-weight:500}
.btn.sec:hover{border-color:var(--outline);filter:none}
.btn.ghost{background:transparent;color:var(--muted);padding:0 10px;font-weight:500}
.btn.ghost:hover{color:var(--ink);background:var(--card2);filter:none}
.btn.danger{background:transparent;color:var(--bad);border-color:color-mix(in oklab,var(--bad) 40%,transparent);height:32px;padding:0 10px;font-size:12.5px;font-weight:500}
.btn.danger.arm{background:var(--bad);color:#fff;border-color:transparent}
.btn.wide{display:flex;width:100%;height:42px}
.btn.sm{height:32px;padding:0 11px;font-size:13px}

/* OS tabs (subtle Miku pink active highlight like panel sidebar) */
.os-tabs{display:flex;gap:6px;overflow-x:auto;padding-bottom:4px;margin-bottom:14px;scrollbar-width:none}
.os-tabs::-webkit-scrollbar{display:none}
.os-tab{flex:none;height:34px;padding:0 13px;border-radius:8px;border:1px solid var(--line);background:var(--card2);color:var(--muted);
 font:500 13px/1 inherit;cursor:pointer;display:inline-flex;align-items:center;gap:6px;transition:background .15s,color .15s,border-color .15s}
.os-tab:hover{color:var(--ink)}
.os-tab.on{background:var(--pink-soft);border-color:color-mix(in oklab,var(--pink) 45%,transparent);color:var(--ink);font-weight:600}
.os-dot{width:6px;height:6px;border-radius:50%;background:var(--pink);display:none}
.os-tab.detected .os-dot{display:inline-block}

.apps{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px}
.app{background:var(--card2);border:1px solid var(--line);border-radius:10px;padding:14px;display:flex;flex-direction:column;gap:10px}
.app.rec{border-color:color-mix(in oklab,var(--primary) 55%,transparent);background:color-mix(in oklab,var(--accent) 8%,var(--card2))}
.app-top{display:flex;align-items:center;justify-content:space-between;gap:6px}
.app b{font-size:14.5px;font-weight:600}
.rec-tag{font-size:11px;font-weight:600;padding:2px 7px;border-radius:6px;background:var(--primary-soft);color:var(--primary)}
.app .note{font-size:12.5px;color:var(--muted);margin-top:-4px;line-height:1.4}
.app-actions{display:flex;gap:8px;margin-top:auto;flex-wrap:wrap}
.app-actions .btn{flex:1}

.guide{margin-top:14px;padding:14px 16px;border-radius:10px;background:var(--bg);border:1px solid var(--line)}
.guide-title{font-size:13px;font-weight:600;margin-bottom:8px;color:var(--ink)}
.guide ol{margin:0;padding-left:18px;color:var(--muted);font-size:13px;line-height:1.55}
.guide li+li{margin-top:4px}
.steps{white-space:pre-line;color:var(--muted);font-size:13px;line-height:1.55}

/* Devices HWID list */
.dev-list{display:flex;flex-direction:column;gap:8px}
.dev-row{display:flex;align-items:center;justify-content:space-between;gap:10px;padding:11px 13px;border-radius:10px;background:var(--card2);border:1px solid var(--line)}
.dev-info{min-width:0}
.dev-name{font-size:13.5px;font-weight:500;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dev-meta{font-size:12px;color:var(--muted);margin-top:2px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.empty-dev{color:var(--muted);font-size:13px;padding:8px 2px}

.foot{text-align:center;color:var(--muted);font-size:12.5px;margin-top:18px;white-space:pre-line}

@media (max-width:560px){
  main{padding:14px 12px calc(28px + env(safe-area-inset-bottom))}
  .card{padding:15px}
  .kpis{grid-template-columns:1fr 1fr;gap:10px}
  .kpi{padding:13px 14px}
  .kpi .v{font-size:15.5px}
  .connect-grid{grid-template-columns:1fr;text-align:center}
  .apps{grid-template-columns:1fr}
  .chart-bars{gap:3px;height:96px}
  .day-track{height:72px}
  .day-lbl{font-size:9px}
  .copy{flex-wrap:wrap}
  .copy .btn{width:100%}
}
@media (prefers-reduced-motion:reduce){*{transition:none!important}}
</style></head><body class="{{.Theme}}"><main>

<div class="card hero">
  <div class="hero-left">
    <div class="avatar">{{.Initial}}</div>
    <div style="min-width:0">
      <h1>{{.Heading}}</h1>
      <div class="muted" style="overflow:hidden;text-overflow:ellipsis">{{.Username}}</div>
    </div>
  </div>
  <span class="chip{{if not .Active}} bad{{end}}">{{.Status}}</span>
</div>

{{if .Description}}<div class="card desc">{{.Description}}</div>{{end}}
{{if .Announce}}{{if .AnnounceURL}}<a class="ann" href="{{.AnnounceURL}}"{{if .Preview}} target="_blank" rel="noopener noreferrer"{{end}}><svg viewBox="0 0 24 24"><path d="M3 11v2a1 1 0 0 0 1 1h2l5 4V6L6 10H4a1 1 0 0 0-1 1zM15.5 8.5a5 5 0 0 1 0 7"/></svg><span>{{.Announce}}</span></a>{{else}}<div class="ann"><svg viewBox="0 0 24 24"><path d="M3 11v2a1 1 0 0 0 1 1h2l5 4V6L6 10H4a1 1 0 0 0-1 1zM15.5 8.5a5 5 0 0 1 0 7"/></svg><span>{{.Announce}}</span></div>{{end}}{{end}}

{{if .ShowTraffic}}
<div class="kpis">
  <div class="kpi">
    <div class="k">{{.T.Traffic}}</div>
    <div class="v">{{.Used}} <span class="muted" style="font-weight:400">/ {{.Limit}}</span></div>
    {{if .Percent}}<div class="bar"><i style="width:{{.Percent}}%"></i></div>{{end}}
  </div>
  <div class="kpi">
    <div class="k">{{.T.ValidUntil}}</div>
    <div class="v">{{.Expire}}</div>
    {{if .DaysLeft}}<div class="s">{{.DaysLeft}}</div>{{end}}
  </div>
</div>
{{end}}

{{if .ShowTraffic}}{{if .Daily}}
<div class="card">
  <div class="card-head">
    <h2>{{.T.TrafficByDay}}</h2>
    <div class="chart-info" id="chart-info">{{if .ChartHasData}}{{.T.Max}}: <b>{{.ChartMax}}</b>{{else}}{{.T.NoDataYet}}{{end}}</div>
  </div>
  <div class="chart-bars" id="chart-bars">
    {{range $i, $d := .Daily}}
    <button type="button" class="day-col" data-date="{{$d.Date}}" data-down="{{$d.HumanDown}}" data-up="{{$d.HumanUp}}" data-total="{{$d.HumanTotal}}" title="{{$d.Date}}: {{$d.HumanTotal}}">
      <div class="day-track">
        {{if $d.UpPct}}<div class="day-up" style="height:{{$d.UpPct}}%"></div>{{end}}
        {{if $d.DownPct}}<div class="day-down" style="height:{{$d.DownPct}}%"></div>{{end}}
      </div>
      <span class="day-lbl">{{$d.Label}}</span>
    </button>
    {{end}}
  </div>
  <div class="legend">
    <span><i style="background:var(--primary)"></i>{{.T.Download}}</span>
    <span><i style="background:var(--pink)"></i>{{.T.Upload}}</span>
    <span class="muted" style="margin-left:auto">{{.T.Days14}}</span>
  </div>
</div>
{{end}}{{end}}

<div class="card">
  <div class="card-head">
    <h2>{{.T.Connection}}</h2>
    <span class="muted">{{.T.LinkAndQR}}</span>
  </div>
  <div class="{{if .QR}}connect-grid{{end}}">
    {{if .QR}}<div class="qr-wrap"><img src="data:image/png;base64,{{.QR}}" alt="{{.T.QRAlt}}"></div>{{end}}
    <div style="min-width:0">
      <div class="muted" style="margin-bottom:8px">{{.T.ScanOrCopy}}</div>
      <div class="copy">
        <input id="u" readonly value="{{.URL}}" onclick="this.select()" aria-label="{{.T.SubLinkLabel}}">
        <button id="c" type="button" class="btn sm"><svg viewBox="0 0 24 24"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/></svg><span>{{.T.Copy}}</span></button>
      </div>
    </div>
  </div>
</div>

{{if .Apps}}
<div class="card">
  <div class="card-head">
    <h2 id="apps-h">{{.T.Apps}}</h2>
    <span class="chip pink" id="os-badge" style="display:none"></span>
  </div>
  <div class="os-tabs" id="os-tabs" role="tablist">
    <button type="button" class="os-tab" data-os="ios"><span class="os-dot"></span>iOS</button>
    <button type="button" class="os-tab" data-os="android"><span class="os-dot"></span>Android</button>
    <button type="button" class="os-tab" data-os="windows"><span class="os-dot"></span>Windows</button>
    <button type="button" class="os-tab" data-os="macos"><span class="os-dot"></span>macOS</button>
    <button type="button" class="os-tab" data-os="linux"><span class="os-dot"></span>Linux</button>
  </div>
  <div class="apps" id="apps-grid">
    {{range .Apps}}
    <div class="app{{if .Recommended}} rec{{end}}" data-id="{{.ID}}" data-p="{{.Platforms}}">
      <div class="app-top"><b>{{.Name}}</b><span class="rec-tag"{{if not .Recommended}} style="display:none"{{end}}>{{$.T.Recommended}}</span></div>
      {{if .Note}}<span class="note">{{.Note}}</span>{{end}}
      <div class="app-actions">
        {{if .Link}}<a class="btn sm app-add" href="{{.Link}}"{{if $.Preview}} target="_blank" rel="noopener noreferrer"{{end}}><svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>{{$.T.Add}}</a>{{end}}
        <button type="button" class="btn sm app-copy"{{if .Link}} style="display:none"{{end}}><svg viewBox="0 0 24 24"><rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/></svg><span>{{$.T.CopyLink}}</span></button>
        {{if .Download}}<a class="btn sec sm" href="{{.Download}}" target="_blank" rel="noopener"><svg viewBox="0 0 24 24"><path d="M12 4v11m-5-5 5 5 5-5M5 20h14"/></svg>{{$.T.DownloadBtn}}</a>{{end}}
      </div>
    </div>
    {{end}}
  </div>
  <div class="guide">
    <div class="guide-title" id="guide-title">{{.T.HowToConnect}}</div>
    {{if .Instructions}}<div class="steps">{{.Instructions}}</div>{{else}}
    <ol id="guide-steps">
      <li>{{.T.Step1}}</li>
      <li>{{.T.Step2}}</li>
      <li>{{.T.Step3}}</li>
    </ol>{{end}}
  </div>
</div>
{{else if .Instructions}}
<div class="card"><div class="card-head"><h2>{{.T.HowToConnect}}</h2></div><div class="steps">{{.Instructions}}</div></div>
{{end}}

{{if .ShowDevices}}
<div class="card" id="devices-card" data-base="{{.DeviceActionBase}}">
  <div class="card-head">
    <h2>{{.T.DevicesTitle}}</h2>
    <span class="muted" id="dev-counter">{{if .HWIDOff}}{{.T.CheckDisabled}}{{else if .DeviceLimit}}{{len .Devices}} {{.T.Of}} {{.DeviceLimit}}{{else}}{{len .Devices}} {{.T.Connected}}{{end}}</span>
  </div>
  <div class="dev-list" id="dev-list">
    {{if not .Devices}}
    <div class="empty-dev">{{.T.DevicesEmpty}}</div>
    {{else}}
    {{range .Devices}}
    <div class="dev-row" data-id="{{.ID}}">
      <div class="dev-info">
        <div class="dev-name">{{.Title}}</div>
        <div class="dev-meta">{{.Meta}}{{if .LastSeen}} · {{.LastSeen}}{{end}}</div>
      </div>
      {{if $.DeviceActionBase}}
      <button type="button" class="btn danger dev-del" data-id="{{.ID}}">{{$.T.Delete}}</button>
      {{end}}
    </div>
    {{end}}
    {{end}}
  </div>
</div>
{{end}}

{{if .SupportURL}}<a class="btn sec wide" href="{{.SupportURL}}"{{if .Preview}} target="_blank" rel="noopener noreferrer"{{end}}><svg viewBox="0 0 24 24"><path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z"/></svg>{{.T.Support}}</a>{{end}}
{{if .Footer}}<div class="foot">{{.Footer}}</div>{{end}}
</main>
<script>
(function(){
  var L={
    copy:{{.T.Copy}},copied:{{.T.Copied}},copyLink:{{.T.CopyLink}},
    yourOS:{{.T.YourOS}},howOn:{{.T.HowToConnectOn}},
    del:{{.T.Delete}},sure:{{.T.Sure}},err:{{.T.Error}},noDev:{{.T.NoLinkedDevices}}
  };
  var c=document.getElementById('c');
  if(c){var s=c.querySelector('span');c.onclick=function(){var i=document.getElementById('u');
    (navigator.clipboard?navigator.clipboard.writeText(i.value):Promise.reject()).catch(function(){i.select();document.execCommand('copy')});
    s.textContent=L.copied;setTimeout(function(){s.textContent=L.copy},1600)}}

  var bars=document.getElementById('chart-bars'), info=document.getElementById('chart-info');
  if(bars&&info){
    var cols=[].slice.call(bars.querySelectorAll('.day-col'));
    function showDay(el){
      cols.forEach(function(x){x.classList.remove('on')});
      el.classList.add('on');
      info.innerHTML='<b>'+el.dataset.date+'</b> · ↓ '+el.dataset.down+' · ↑ '+el.dataset.up;
    }
    cols.forEach(function(el){
      el.addEventListener('click',function(){showDay(el)});
      el.addEventListener('mouseenter',function(){showDay(el)});
    });
  }

  var ua=navigator.userAgent||'';
  var detected=/android/i.test(ua)?'android':/iphone|ipad|ipod/i.test(ua)?'ios':/mac os|macintosh/i.test(ua)?'macos':/windows/i.test(ua)?'windows':/linux/i.test(ua)?'linux':'ios';
  var osNames={ios:'iOS',android:'Android',windows:'Windows',macos:'macOS',linux:'Linux'};
  var tabsWrap=document.getElementById('os-tabs'), grid=document.getElementById('apps-grid'), badge=document.getElementById('os-badge'), gTitle=document.getElementById('guide-title');
  if(tabsWrap&&grid){
    var tabs=[].slice.call(tabsWrap.querySelectorAll('.os-tab'));
    var cards=[].slice.call(grid.querySelectorAll('.app'));
    tabs.forEach(function(t){if(t.dataset.os===detected)t.classList.add('detected')});
    if(badge){badge.textContent=L.yourOS+osNames[detected];badge.style.display='inline-flex'}
    function selectOS(os){
      tabs.forEach(function(t){t.classList.toggle('on',t.dataset.os===os)});
      var shown=0;
      cards.forEach(function(el){
        var ps=(el.dataset.p||'').trim();
        var ok=!ps||ps.indexOf(os)>=0;
        el.style.display=ok?'':'none';
        var isRec=ok&&shown===0;
        el.classList.toggle('rec',isRec);
        var tag=el.querySelector('.rec-tag');if(tag)tag.style.display=isRec?'':'none';
        var add=el.querySelector('.app-add'), cp=el.querySelector('.app-copy');
        if(add&&cp){
          var noScheme=el.dataset.id==='keqdroid'&&os==='linux';
          add.style.display=noScheme?'none':'';
          cp.style.display=noScheme?'':'none';
        }
        if(ok)shown++;
      });
      if(!shown){cards.forEach(function(el,idx){el.style.display='';el.classList.toggle('rec',idx===0);var tag=el.querySelector('.rec-tag');if(tag)tag.style.display=idx===0?'':'none'})}
      if(gTitle){gTitle.textContent=L.howOn+osNames[os]}
    }
    grid.addEventListener('click',function(e){
      var b=e.target.closest('.app-copy');if(!b)return;
      var i=document.getElementById('u'), sp=b.querySelector('span');
      if(i)(navigator.clipboard?navigator.clipboard.writeText(i.value):Promise.reject()).catch(function(){i.select();document.execCommand('copy')});
      if(sp){sp.textContent=L.copied;setTimeout(function(){sp.textContent=L.copyLink},1600)}
    });
    tabs.forEach(function(t){t.onclick=function(){selectOS(t.dataset.os)}});
    selectOS(detected);
  }

  var dCard=document.getElementById('devices-card');
  if(dCard&&dCard.dataset.base){
    var base=dCard.dataset.base, list=document.getElementById('dev-list'), counter=document.getElementById('dev-counter');
    dCard.addEventListener('click',function(e){
      var btn=e.target.closest('.dev-del');
      if(!btn||btn.disabled)return;
      if(!btn.classList.contains('arm')){
        btn.classList.add('arm');btn.textContent=L.sure;
        setTimeout(function(){if(btn.isConnected&&btn.classList.contains('arm')){btn.classList.remove('arm');btn.textContent=L.del}},3500);
        return;
      }
      var id=btn.dataset.id;
      btn.disabled=true;btn.textContent='…';
      fetch(base+encodeURIComponent(id)+'/delete',{method:'POST',headers:{'X-Vynel':'1'}}).then(function(r){
        if(!r.ok)throw new Error('err');
        var row=btn.closest('.dev-row');if(row)row.remove();
        var left=list.querySelectorAll('.dev-row').length;
        if(!left){list.innerHTML='<div class="empty-dev">'+L.noDev+'</div>'}
        if(counter){counter.textContent=counter.textContent.replace(/^\d+/,String(left))}
      }).catch(function(){btn.disabled=false;btn.classList.remove('arm');btn.textContent=L.err});
    });
  }
})();
</script></body></html>`))

// PageText holds localized UI labels of the public subscription page.
type PageText struct {
	Traffic, ValidUntil, TrafficByDay, Max, NoDataYet      string
	Download, Upload, Days14                               string
	Connection, LinkAndQR, QRAlt, ScanOrCopy, SubLinkLabel string
	Copy, Copied, CopyLink                                 string
	Apps, Recommended, Add, DownloadBtn                    string
	HowToConnect, HowToConnectOn, YourOS                   string
	Step1, Step2, Step3                                    string
	DevicesTitle, CheckDisabled, Of, Connected             string
	DevicesEmpty, NoLinkedDevices, Delete, Sure, Error     string
	Support                                                string
}

var pageTextRU = PageText{
	Traffic: "Трафик", ValidUntil: "Действует до", TrafficByDay: "Трафик по дням",
	Max: "Макс", NoDataYet: "Пока нет данных",
	Download: "Скачивание", Upload: "Отдача", Days14: "14 дней",
	Connection: "Подключение", LinkAndQR: "Ссылка и QR-код", QRAlt: "QR-код подписки",
	ScanOrCopy: "Отсканируйте QR-код в приложении или скопируйте ссылку подписки:", SubLinkLabel: "Ссылка подписки",
	Copy: "Копировать", Copied: "Скопировано", CopyLink: "Скопировать ссылку",
	Apps: "Приложения", Recommended: "Рекомендуем", Add: "Добавить", DownloadBtn: "Скачать",
	HowToConnect: "Как подключиться", HowToConnectOn: "Как подключиться на ", YourOS: "Ваша ОС: ",
	Step1:        "Установите рекомендованное приложение кнопкой «Скачать».",
	Step2:        "Вернитесь на эту страницу и нажмите «Добавить» — подписка загрузится автоматически.",
	Step3:        "Выберите сервер в приложении и включите подключение.",
	DevicesTitle: "Устройства (HWID)", CheckDisabled: "Проверка отключена", Of: "из", Connected: "подключено",
	DevicesEmpty:    "Устройства появятся здесь после первого подключения приложения.",
	NoLinkedDevices: "Привязанных устройств нет.", Delete: "Удалить", Sure: "Точно?", Error: "Ошибка",
	Support: "Поддержка",
}

var pageTextEN = PageText{
	Traffic: "Traffic", ValidUntil: "Valid until", TrafficByDay: "Traffic by day",
	Max: "Max", NoDataYet: "No data yet",
	Download: "Download", Upload: "Upload", Days14: "14 days",
	Connection: "Connect", LinkAndQR: "Link and QR code", QRAlt: "Subscription QR code",
	ScanOrCopy: "Scan the QR code in your app or copy the subscription link:", SubLinkLabel: "Subscription link",
	Copy: "Copy", Copied: "Copied", CopyLink: "Copy link",
	Apps: "Apps", Recommended: "Recommended", Add: "Add", DownloadBtn: "Download",
	HowToConnect: "How to connect", HowToConnectOn: "How to connect on ", YourOS: "Your OS: ",
	Step1:        "Install the recommended app using the “Download” button.",
	Step2:        "Return to this page and tap “Add” — the subscription will import automatically.",
	Step3:        "Pick a server in the app and turn the connection on.",
	DevicesTitle: "Devices (HWID)", CheckDisabled: "Check disabled", Of: "of", Connected: "connected",
	DevicesEmpty:    "Devices will appear here after the first app connection.",
	NoLinkedDevices: "No linked devices.", Delete: "Remove", Sure: "Sure?", Error: "Error",
	Support: "Support",
}

type pageApp struct {
	ID, Name, Note, Platforms string
	Link                      template.URL
	Download                  template.URL
	Recommended               bool
}

type pageDevice struct {
	ID       int64
	Title    string
	Meta     string
	LastSeen string
}

type pageDay struct {
	Date       string
	Label      string
	DownPct    int
	UpPct      int
	HumanDown  string
	HumanUp    string
	HumanTotal string
}

// PageData is everything the subscription page shows.
type PageData struct {
	Lang string
	T    PageText
	// Preview opens links outside the panel's iframe; failed external protocol
	// launches must not replace the preview with a browser error page.
	Preview                               bool
	Heading, Username, Description, Theme string
	Initial                               string
	Accent                                template.CSS
	Announce                              string
	AnnounceURL                           template.URL
	ShowTraffic                           bool
	Status                                string
	Active                                bool
	Used, Limit, Expire, DaysLeft         string
	Percent                               int
	QR                                    string
	URL                                   string
	Apps                                  []pageApp
	Instructions, Footer                  string
	SupportURL                            template.URL

	Daily            []pageDay
	ChartMax         string
	ChartHasData     bool
	ShowDevices      bool
	HWIDOff          bool
	DeviceLimit      int
	DeviceActionBase string
	Devices          []pageDevice
}

func safeURL(s string) template.URL {
	l := strings.ToLower(strings.TrimSpace(s))
	if l == "" || strings.HasPrefix(l, "javascript:") || strings.HasPrefix(l, "data:") || strings.HasPrefix(l, "vbscript:") {
		return ""
	}
	return template.URL(s) // #nosec G203 -- deep links of app schemes; dangerous schemes are cut above
}

// BuildPage prepares the page for a user with a page config.
func BuildPage(u *store.User, subURL string, b service.SubBasics, p service.SubPage, now time.Time) (PageData, error) {
	v := UserVars(u, subURL, b, now)
	d := PageData{
		Heading: p.Heading, Username: u.Username, Description: v.Expand(p.Description), Theme: p.Theme,
		Accent: template.CSS(p.Accent), Announce: b.Announce, AnnounceURL: safeURL(b.AnnounceURL),
		ShowTraffic: p.ShowTraffic, Status: v["status"], Active: u.Status == store.StatusActive,
		Used: v["used"], Limit: v["limit"], Expire: v["expire_date"], URL: subURL,
		Instructions: v.Expand(p.Instructions), Footer: v.Expand(p.Footer), SupportURL: safeURL(b.SupportURL),
	}
	if d.Heading == "" {
		d.Heading = b.Title
	}
	for _, r := range d.Heading {
		d.Initial = strings.ToUpper(string(r))
		break
	}
	if d.Theme == "" {
		d.Theme = "dark"
	}
	if !accentRe(p.Accent) {
		d.Accent = "#39c5bb"
	}
	if days := v["days_left"]; days != "" {
		d.DaysLeft = "осталось " + days + " дн."
	}
	if u.TrafficLimitBytes != nil && *u.TrafficLimitBytes > 0 {
		d.Percent = int(min(100, 100*u.TrafficUsedBytes / *u.TrafficLimitBytes))
	}
	if p.ShowQR {
		png, err := qrcode.Encode(subURL, qrcode.Medium, 512)
		if err != nil {
			return d, err
		}
		d.QR = base64.StdEncoding.EncodeToString(png)
	}
	first := true
	for _, a := range p.Apps {
		if !a.Enabled {
			continue
		}
		link := strings.TrimSuffix(v.Expand(a.Link), "&outbound-dns=")
		d.Apps = append(d.Apps, pageApp{ID: a.ID, Name: a.Name, Note: a.Note, Platforms: strings.Join(a.Platforms, " "),
			Link: safeURL(link), Download: safeURL(a.Download), Recommended: first})
		first = false
	}
	return d, nil
}

// PopulatePageExtras attaches HWID devices and daily traffic chart data to PageData.
func PopulatePageExtras(d *PageData, devices []*store.Device, deviceLimit int, hwidOff bool, daily []service.DayTraffic, actionBase string, now time.Time) {
	d.ShowDevices = true
	d.HWIDOff = hwidOff
	d.DeviceLimit = deviceLimit
	d.DeviceActionBase = actionBase
	d.Devices = make([]pageDevice, 0, len(devices))
	for _, dev := range devices {
		title := strings.TrimSpace(dev.Model)
		if title == "" {
			title = strings.TrimSpace(dev.Platform)
		}
		if title == "" {
			title = "Устройство"
		}
		var meta []string
		if dev.Platform != "" && dev.Platform != title {
			meta = append(meta, strings.TrimSpace(dev.Platform+" "+dev.OSVersion))
		} else if dev.OSVersion != "" {
			meta = append(meta, dev.OSVersion)
		}
		if dev.HWID != "" {
			short := dev.HWID
			if len(short) > 10 {
				short = short[:10] + "…"
			}
			meta = append(meta, "HWID "+short)
		}
		d.Devices = append(d.Devices, pageDevice{
			ID:       dev.ID,
			Title:    title,
			Meta:     strings.Join(meta, " · "),
			LastSeen: formatAgo(dev.LastSeen, now),
		})
	}

	if len(daily) == 0 {
		return
	}
	var maxTotal int64
	for _, row := range daily {
		if tot := row.Up + row.Down; tot > maxTotal {
			maxTotal = tot
		}
	}
	d.ChartHasData = maxTotal > 0
	if maxTotal > 0 {
		d.ChartMax = human(maxTotal)
	}
	d.Daily = make([]pageDay, 0, len(daily))
	for _, row := range daily {
		tm := time.Unix(row.Day, 0).UTC()
		tot := row.Up + row.Down
		downPct, upPct := 0, 0
		if maxTotal > 0 && tot > 0 {
			downPct = int(row.Down * 100 / maxTotal)
			upPct = int(row.Up * 100 / maxTotal)
			if row.Down > 0 && downPct < 4 {
				downPct = 4
			}
			if row.Up > 0 && upPct < 3 {
				upPct = 3
			}
			if downPct+upPct > 100 {
				downPct = 100 - upPct
			}
		}
		d.Daily = append(d.Daily, pageDay{
			Date:       tm.Format("02.01"),
			Label:      tm.Format("02"),
			DownPct:    downPct,
			UpPct:      upPct,
			HumanDown:  human(row.Down),
			HumanUp:    human(row.Up),
			HumanTotal: human(tot),
		})
	}
}

func formatAgo(ts int64, now time.Time) string {
	if ts <= 0 {
		return ""
	}
	diff := now.Sub(time.Unix(ts, 0))
	if diff < time.Minute {
		return "только что"
	}
	if diff < time.Hour {
		return fmt.Sprintf("%d мин назад", int(diff.Minutes()))
	}
	if diff < 24*time.Hour {
		return fmt.Sprintf("%d ч назад", int(diff.Hours()))
	}
	return fmt.Sprintf("%d дн назад", int(diff.Hours()/24))
}

func accentRe(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// ApplyPageLang switches page labels and generated status/unit text to lang ("en" or "ru").
func ApplyPageLang(d *PageData, lang string) {
	if lang != "en" {
		d.Lang = "ru"
		d.T = pageTextRU
		return
	}
	d.Lang = "en"
	d.T = pageTextEN
	switch d.Status {
	case "активна":
		d.Status = "active"
	case "отключена":
		d.Status = "disabled"
	case "исчерпан трафик":
		d.Status = "traffic limit"
	case "истекла":
		d.Status = "expired"
	}
	if d.Limit == "безлимит" {
		d.Limit = "unlimited"
	}
	if d.Expire == "бессрочно" {
		d.Expire = "unlimited"
	}
	if strings.HasPrefix(d.DaysLeft, "осталось ") && strings.HasSuffix(d.DaysLeft, " дн.") {
		n := strings.TrimSuffix(strings.TrimPrefix(d.DaysLeft, "осталось "), " дн.")
		d.DaysLeft = n + " d left"
	}
	enUnits := func(s string) string {
		s = strings.ReplaceAll(s, " ТБ", " TB")
		s = strings.ReplaceAll(s, " ГБ", " GB")
		s = strings.ReplaceAll(s, " МБ", " MB")
		s = strings.ReplaceAll(s, " КБ", " KB")
		return s
	}
	d.Used = enUnits(d.Used)
	d.Limit = enUnits(d.Limit)
	d.ChartMax = enUnits(d.ChartMax)
	for i := range d.Daily {
		d.Daily[i].HumanDown = enUnits(d.Daily[i].HumanDown)
		d.Daily[i].HumanUp = enUnits(d.Daily[i].HumanUp)
		d.Daily[i].HumanTotal = enUnits(d.Daily[i].HumanTotal)
	}
	for i := range d.Devices {
		if d.Devices[i].Title == "Устройство" {
			d.Devices[i].Title = "Device"
		}
		ls := d.Devices[i].LastSeen
		switch {
		case ls == "только что":
			d.Devices[i].LastSeen = "just now"
		case strings.HasSuffix(ls, " мин назад"):
			d.Devices[i].LastSeen = strings.TrimSuffix(ls, " мин назад") + " min ago"
		case strings.HasSuffix(ls, " ч назад"):
			d.Devices[i].LastSeen = strings.TrimSuffix(ls, " ч назад") + " h ago"
		case strings.HasSuffix(ls, " дн назад"):
			d.Devices[i].LastSeen = strings.TrimSuffix(ls, " дн назад") + " d ago"
		}
	}
}

// RenderPage writes the page.
func RenderPage(w io.Writer, d PageData) error {
	if d.Lang == "" {
		d.Lang = "ru"
	}
	if d.T.Traffic == "" {
		d.T = pageTextRU
	}
	return pageTmpl.Execute(w, d)
}

func (h *Handler) handleDeviceDelete(ctx context.Context, w http.ResponseWriter, r *http.Request, u *store.User, explicit string) {
	rest, ok := strings.CutPrefix(explicit, "devices/")
	if !ok {
		h.notFound(ctx, w)
		return
	}
	idStr, ok := strings.CutSuffix(rest, "/delete")
	if !ok {
		h.notFound(ctx, w)
		return
	}
	devID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || devID <= 0 {
		h.notFound(ctx, w)
		return
	}
	if err := h.svc.DeleteDevice(ctx, service.Actor{Kind: "user", ID: u.Username}, u.ID, devID); err != nil {
		http.Error(w, "delete failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (h *Handler) page(ctx context.Context, w http.ResponseWriter, r *http.Request, u *store.User) {
	subURL, err := h.svc.SubscriptionURL(ctx, u)
	if err != nil {
		subURL = "https://" + r.Host + r.URL.Path
	}
	p, err := h.svc.SubPage(ctx)
	if err != nil {
		h.log.Warn("subscription page config", "err", err)
	}
	now := time.Now()
	d, err := BuildPage(u, subURL, h.svc.SubBasicsGet(ctx), p, now)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	devs, _ := h.svc.Devices(ctx, u.ID)
	daily, _ := h.svc.TrafficByDay(ctx, u.ID, 14)
	hwidEnabled, _ := h.svc.Setting(ctx, service.SettingHWIDEnabled, "true")
	hwidOff := hwidEnabled != "true" || u.HWIDOff
	limit := 0
	if u.HWIDLimit != nil {
		limit = int(*u.HWIDLimit)
	} else if v, _ := h.svc.Setting(ctx, service.SettingHWIDLimit, "0"); v != "" {
		limit, _ = strconv.Atoi(v)
	}
	prefix := h.svc.SubPrefix(ctx)
	PopulatePageExtras(&d, devs, limit, hwidOff, daily, prefix+u.SubToken+"/devices/", now)
	ApplyPageLang(&d, i18n.FromRequest(r))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = RenderPage(w, d)
}

func human(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f ГБ", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f МБ", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d КБ", b>>10)
}
