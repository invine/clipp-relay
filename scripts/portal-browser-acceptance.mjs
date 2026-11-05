#!/usr/bin/env node
// Browser acceptance for the local built-service fixture. It owns and deletes
// its temporary Chrome profile and only permits the localhost test certificate.
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { spawn, spawnSync } from 'node:child_process'

const base = process.argv[2]
const chrome = process.env.CHROME_BIN
if (!base || !chrome) throw new Error('usage: CHROME_BIN=".../Google Chrome for Testing" node scripts/portal-browser-acceptance.mjs https://localhost:18443')
const baseURL = new URL(base)
if (baseURL.protocol !== 'https:' || baseURL.hostname !== 'localhost') throw new Error('acceptance URL must use https://localhost')
const artifacts = process.env.ARTIFACT_DIR || '/private/tmp/clipp-relay-portal-browser-evidence'
await mkdir(artifacts, { recursive: true })
const profile = await mkdtemp(join(tmpdir(), 'clipp-relay-portal-chrome-'))
const chromeProc = spawn(chrome, ['--headless=new', '--no-first-run', '--disable-gpu', '--disable-background-networking', '--allow-insecure-localhost', `--user-data-dir=${profile}`, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
let cdp
try {
  const portFile = join(profile, 'DevToolsActivePort')
  let port
  for (let i = 0; i < 100; i++) {
    try { port = (await readFile(portFile, 'utf8')).split('\n')[0]; break } catch {}
    if (chromeProc.exitCode !== null) throw new Error(`Chrome exited ${chromeProc.exitCode}`)
    await new Promise(r => setTimeout(r, 100))
  }
  if (!port) throw new Error('Chrome DevTools endpoint did not start within 10 seconds')
  let target
  for (let i = 0; i < 100; i++) {
    try { target = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find(t => t.type === 'page'); if (target) break } catch {}
    await new Promise(r => setTimeout(r, 100))
  }
  if (!target) throw new Error('Chrome page target unavailable')
  cdp = new WebSocket(target.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => { cdp.onopen = resolve; cdp.onerror = reject })
  let id = 0
  const pending = new Map()
  const responses = []
  const requests = []
  cdp.onmessage = ({ data }) => { const m = JSON.parse(data); if (m.method === 'Network.requestWillBeSent') requests.push({url:m.params.request.url,method:m.params.request.method,initiator:m.params.initiator?.type,redirectURL:m.params.redirectResponse?.url,redirectStatus:m.params.redirectResponse?.status,redirectHeaders:m.params.redirectResponse?.headers}); if (m.method === 'Network.loadingFailed') requests.push({requestId:m.params.requestId,error:m.params.errorText,blocked:m.params.blockedReason}); if (m.method === 'Network.responseReceived') responses.push({ url: m.params.response.url, status: m.params.response.status, headers: m.params.response.headers }); if (m.id && pending.has(m.id)) { const { resolve, reject } = pending.get(m.id); pending.delete(m.id); m.error ? reject(new Error(m.error.message)) : resolve(m.result) } }
  const call = (method, params = {}) => new Promise((resolve, reject) => { const n = ++id; pending.set(n, { resolve, reject }); cdp.send(JSON.stringify({ id: n, method, params })) })
  const evalJS = async expression => { const result = await call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }); if (result.exceptionDetails) throw new Error(JSON.stringify({ text: result.exceptionDetails.text, exception: result.exceptionDetails.exception?.description, line: result.exceptionDetails.lineNumber, column: result.exceptionDetails.columnNumber })); return result.result.value }
  const wait = ms => new Promise(r => setTimeout(r, ms))
  const clickNode = async nodeId => { await call('DOM.scrollIntoViewIfNeeded', { nodeId }); const model = await call('DOM.getBoxModel', { nodeId }); const q = model.model.content; const x = (q[0] + q[2] + q[4] + q[6]) / 4; const y = (q[1] + q[3] + q[5] + q[7]) / 4; await call('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y }); await call('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 }); await call('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 }) }
  const ready = async () => { for (let i=0;i<60;i++) { if ((await evalJS('document.readyState')) === 'complete') return; await wait(100) }; throw new Error('navigation timeout') }
  const snap = async (name, fullPage = true) => { const shot = await call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: fullPage }); await writeFile(join(artifacts, name), Buffer.from(shot.data, 'base64')) }
  const viewportHeight = width => ({ 1440: 900, 768: 1024, 390: 844, 320: 844 }[width] || 900)
  const navigate = async width => { await call('Emulation.setDeviceMetricsOverride', { width, height: viewportHeight(width), deviceScaleFactor: 1, mobile: width < 640 }); await call('Page.navigate', { url: `${base}/` }); await ready(); await wait(1100) }
  await call('Page.enable'); await call('Runtime.enable'); await call('Network.enable'); await call('Emulation.setLocaleOverride', { locale: 'en-US' })
  await call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })
  await call('Page.navigate', { url: `${base}/` }); await ready(); await wait(150)
  await evalJS(`document.querySelector('.setup-instructions summary').focus()`)
  await call('Input.dispatchKeyEvent', { type:'keyDown',key:' ',code:'Space',windowsVirtualKeyCode:32 }); await call('Input.dispatchKeyEvent', { type:'keyUp',key:' ',code:'Space',windowsVirtualKeyCode:32 })
  const signedOut = await evalJS(`(()=>{const disclosure=document.querySelector('.setup-instructions');return {title:document.querySelector('h1')?.innerText,signIn:document.querySelector('.welcome a[href="/auth/login"]')?.innerText.trim().replace(/\\s+/g,' '),approval:document.body.innerText.includes('New accounts need administrator approval'),setup:document.body.innerText.includes('Add this relay to Clipp'),url:document.querySelector('#discovery-url')?.value,instructions:document.body.innerText.includes('Authenticated discovery'),instructionDisclosure:!!disclosure.querySelector('summary'),instructionKeyboard:disclosure.open&&document.activeElement===disclosure.querySelector('summary'),portalVsApp:document.body.innerText.includes('each Clipp app needs its own authorization'),browserFailure:document.body.innerText.includes('does not mean setup failed'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth}})()`)
  if (signedOut.signIn !== 'Sign in with Google →' || !signedOut.approval || !signedOut.setup || signedOut.url !== `${base}/v1/relay` || !signedOut.instructions || !signedOut.instructionKeyboard || !signedOut.portalVsApp || !signedOut.browserFailure || signedOut.overflow) throw new Error(`signed-out setup acceptance failed: ${JSON.stringify(signedOut)}`)
  await snap('signin-1440.png')
  const clipboardSuccess = await evalJS(`(async()=>{window.__copiedRelayURL=null;Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{window.__copiedRelayURL=value}}});document.querySelector('[data-copy]').click();await new Promise(r=>setTimeout(r,30));return {copied:window.__copiedRelayURL,status:document.querySelector('[data-copy-status]').innerText,selected:document.querySelector('#discovery-url').selectionStart!==document.querySelector('#discovery-url').selectionEnd}})()`)
  if (clipboardSuccess.copied !== signedOut.url || !clipboardSuccess.status.includes('copied') || clipboardSuccess.selected) throw new Error(`clipboard success was not reported accurately: ${JSON.stringify(clipboardSuccess)}`)
  const clipboardRefusal = await evalJS(`(async()=>{Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async()=>{throw new DOMException('denied','NotAllowedError')}}});document.querySelector('[data-copy]').click();await new Promise(r=>setTimeout(r,30));const input=document.querySelector('#discovery-url');return {status:document.querySelector('[data-copy-status]').innerText,selected:input.selectionStart===0&&input.selectionEnd===input.value.length,focused:document.activeElement===input,button:document.querySelector('[data-copy]').innerText}})()`)
  if (!clipboardRefusal.status.includes('Select and copy') || !clipboardRefusal.selected || !clipboardRefusal.focused || clipboardRefusal.button !== 'Copy URL') throw new Error(`clipboard refusal did not offer manual copy: ${JSON.stringify(clipboardRefusal)}`)
  await call('Emulation.setDeviceMetricsOverride', { width:390, height:844, deviceScaleFactor:1, mobile:true })
  await call('Page.navigate', { url: `${base}/` }); await ready(); await wait(100)
  signedOut.phone = await evalJS(`({overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,signInVisible:document.querySelector('.welcome a[href="/auth/login"]').getBoundingClientRect().top<innerHeight,copyVisible:(()=>{const b=document.querySelector('[data-copy]').getBoundingClientRect();return b.width>0&&b.height>=44})(),instructionsReachable:!!document.querySelector('.setup-instructions summary')})`)
  await snap('signin-390.png')
  if (signedOut.phone.overflow || !signedOut.phone.signInVisible || !signedOut.phone.copyVisible || !signedOut.phone.instructionsReachable) throw new Error(`signed-out phone entry failed: ${JSON.stringify(signedOut.phone)}`)
  await call('Emulation.setDeviceMetricsOverride', { width:768, height:1024, deviceScaleFactor:1, mobile:false })
  await call('Page.navigate', { url: `${base}/` }); await ready(); await wait(100)
  signedOut.tablet = await evalJS(`({width:innerWidth,height:innerHeight,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,signInVisible:document.querySelector('.welcome a[href="/auth/login"]').getBoundingClientRect().top<innerHeight})`)
  await snap('signin-768.png')
  if (signedOut.tablet.overflow || !signedOut.tablet.signInVisible) throw new Error(`signed-out tablet entry failed: ${JSON.stringify(signedOut.tablet)}`)
  await call('Emulation.setDeviceMetricsOverride', { width:1440, height:900, deviceScaleFactor:1, mobile:false })
  await call('Page.navigate', { url: `${base}/` }); await ready(); await wait(100)
  await evalJS(`document.querySelector('.welcome a[href="/auth/login"]').click()`); await wait(500); await ready(); await wait(500)
  if (!(await evalJS(`document.body.innerText.includes('Your account')`))) throw new Error(`OAuth login did not reach the account workspace at ${await evalJS('location.href')}: ${(await evalJS('document.body.innerText')).slice(0,1200)}; responses=${JSON.stringify(responses.slice(-12))}`)
  const facts = await evalJS(`({ title: document.querySelector('h1')?.innerText, account: document.body.innerText.includes('workspace-a@example.test'), allowance: document.body.innerText.includes('Your allowance'), setup: document.querySelector('#connection-setup')?.innerText.includes('Add this relay to Clipp') === true, discovery:document.querySelector('#discovery-url')?.value,width: innerWidth, scroll: document.documentElement.scrollWidth })`)
  if (!facts.account || !facts.allowance || !facts.setup) throw new Error(`expected account workspace facts missing: ${JSON.stringify(facts)}`)
  const connectTask = await evalJS(`(()=>{const trigger=document.querySelector('.connect-clipp');trigger.click();const dialog=document.querySelector('#connect-dialog');return {href:trigger.getAttribute('href'),open:dialog?.open===true,modal:dialog?.matches(':modal')===true,title:dialog?.querySelector('#setup-title')?.innerText,endpoint:dialog?.querySelector('#discovery-url')?.value,close:!!dialog?.querySelector('[data-close-connect]'),instructions:dialog?.querySelector('.setup-instructions')?.textContent.includes('Authenticated discovery')===true,portalBoundary:dialog?.innerText.includes('A Portal Session signs you into this website'),focusInside:dialog?.contains(document.activeElement)}})()`)
  if (connectTask.href !== '#connection-setup' || !connectTask.open || !connectTask.modal || !connectTask.title || connectTask.endpoint !== facts.discovery || !connectTask.close || !connectTask.instructions || !connectTask.portalBoundary || !connectTask.focusInside) throw new Error(`Connect Clipp did not open its contextual setup task: ${JSON.stringify(connectTask)}`)
  const connectClosed = await evalJS(`(()=>{const trigger=document.querySelector('.connect-clipp');document.querySelector('[data-close-connect]').click();return {closed:!document.querySelector('#connect-dialog').open,focusRestored:document.activeElement===trigger}})()`)
  if (!connectClosed.closed || !connectClosed.focusRestored) throw new Error(`Connect Clipp did not close and restore focus: ${JSON.stringify(connectClosed)}`)
  await evalJS(`document.querySelector('.connect-clipp').click()`)
  await call('Input.dispatchKeyEvent', { type:'keyDown', key:'Escape', code:'Escape', windowsVirtualKeyCode:27 })
  await call('Input.dispatchKeyEvent', { type:'keyUp', key:'Escape', code:'Escape', windowsVirtualKeyCode:27 })
  const connectEscape = await evalJS(`({closed:!document.querySelector('#connect-dialog').open,focusRestored:document.activeElement===document.querySelector('.connect-clipp')})`)
  if (!connectEscape.closed || !connectEscape.focusRestored) throw new Error(`Connect Clipp Escape did not close and restore focus: ${JSON.stringify(connectEscape)}`)
  const themeEvidence = await evalJS(`(()=>{const button=document.querySelector('.workspace-sidebar [data-theme-toggle]');if(!button.getBoundingClientRect().width)throw new Error('sidebar theme control unavailable');const initial=document.documentElement.dataset.theme;button.click();const selected={theme:document.documentElement.dataset.theme,label:button.innerText,pressed:button.getAttribute('aria-checked'),stored:localStorage.getItem('clipp-relay-appearance')};return {initial,...selected}})()`)
  if (themeEvidence.initial !== 'dark' || themeEvidence.theme !== 'light' || themeEvidence.label !== 'Light mode' || themeEvidence.pressed !== 'false' || themeEvidence.stored !== 'light') throw new Error(`theme toggle did not select and store light appearance: ${JSON.stringify(themeEvidence)}`)
  await snap('account-light-1440.png')
  await call('Page.reload'); await ready(); await wait(120)
  themeEvidence.reloaded = await evalJS(`({theme:document.documentElement.dataset.theme,label:([...document.querySelectorAll('[data-theme-toggle]')].find(e=>e.getBoundingClientRect().width>0)||document.querySelector('[data-theme-toggle]')).textContent,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  if (themeEvidence.reloaded.theme !== 'light' || themeEvidence.reloaded.label !== 'Light mode' || themeEvidence.reloaded.overflow) throw new Error(`saved theme was not restored safely: ${JSON.stringify(themeEvidence.reloaded)}`)
  await evalJS(`([...document.querySelectorAll('[data-theme-toggle]')].find(e=>e.getBoundingClientRect().width>0)||document.querySelector('[data-theme-toggle]')).click()`)
  themeEvidence.restored = await evalJS(`({theme:document.documentElement.dataset.theme,stored:localStorage.getItem('clipp-relay-appearance')})`)
  if (themeEvidence.restored.theme !== 'dark' || themeEvidence.restored.stored !== 'dark') throw new Error(`theme control did not restore dark appearance: ${JSON.stringify(themeEvidence.restored)}`)
  const lightThemeSweep = {}
  await evalJS(`localStorage.setItem('clipp-relay-appearance','light')`)
  const inspectLight = async (name, width, path) => {
    await call('Emulation.setDeviceMetricsOverride', { width, height: viewportHeight(width), deviceScaleFactor: 1, mobile: width < 640 })
    await call('Page.navigate', { url: `${base}${path}` }); await ready(); await wait(220)
    const result = await evalJS(`(()=>{const rgb=s=>{const m=s.match(/[\\d.]+/g);return m&&!(m.length>3&&Number(m[3])===0)?m.slice(0,3).map(Number):null};const lum=c=>{const a=c.map(v=>v/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4);return .2126*a[0]+.7152*a[1]+.0722*a[2]};const contrast=(a,b)=>{a=rgb(a);b=rgb(b);if(!a||!b)return null;const x=lum(a),y=lum(b);return +((Math.max(x,y)+.05)/(Math.min(x,y)+.05)).toFixed(2)};const ground=getComputedStyle(document.querySelector('.workspace-main')||document.body).backgroundColor;const controls=[...document.querySelectorAll('button:not(:disabled),.button')].filter(e=>{const r=e.getBoundingClientRect();return r.width>0&&r.height>0}).map(e=>({label:e.innerText.trim().replace(/\\s+/g,' ').slice(0,48),ratio:contrast(getComputedStyle(e).color,rgb(getComputedStyle(e).backgroundColor)?getComputedStyle(e).backgroundColor:ground),border:getComputedStyle(e).borderColor,bg:getComputedStyle(e).backgroundColor,fg:getComputedStyle(e).color}));return {theme:document.documentElement.dataset.theme,width:innerWidth,scroll:document.documentElement.scrollWidth,themeToggle:!!([...document.querySelectorAll('[data-theme-toggle]')].find(e=>e.getBoundingClientRect().width>0)||document.querySelector('[data-theme-toggle]')),controls,disabled:[...document.querySelectorAll('button:disabled,input:disabled')].length,mark:[...document.images].every(i=>i.complete&&i.naturalWidth>0)}})()`)
    if (result.theme !== 'light' || result.scroll > width || !result.mark) throw new Error(`light theme ${name} at ${width}px failed: ${JSON.stringify(result)}`)
    if (!result.themeToggle) throw new Error(`light theme switch is missing on ${name}: ${JSON.stringify(result)}`)
    if (result.controls.some(control => control.ratio !== null && control.ratio < 4.5)) throw new Error(`light theme control contrast failed on ${name}: ${JSON.stringify(result.controls)}`)
    await evalJS(`document.querySelector('.workspace-menu').open=true`)
    const toggleRect = await evalJS(`(()=>{const r=([...document.querySelectorAll('[data-theme-toggle]')].find(e=>e.getBoundingClientRect().width>0)||document.querySelector('[data-theme-toggle]'))?.getBoundingClientRect();return r?{x:r.x+r.width/2,y:r.y+r.height/2}:null})()`)
    if (!toggleRect) throw new Error(`light theme switch is not visible on ${name}`)
    if (toggleRect) {
      await call('Input.dispatchMouseEvent', { type:'mouseMoved', x:toggleRect.x, y:toggleRect.y }); await wait(80)
      const hover = await evalJS(`(()=>{const e=([...document.querySelectorAll('[data-theme-toggle]')].find(e=>e.getBoundingClientRect().width>0)||document.querySelector('[data-theme-toggle]')),s=getComputedStyle(e);const rgb=v=>(v.match(/[\\d.]+/g)||[]).slice(0,3).map(Number);const l=c=>c.map(v=>v/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((a,v,i)=>a+v*[.2126,.7152,.0722][i],0);const a=l(rgb(s.color)),b=l(rgb(s.backgroundColor));return {ratio:+((Math.max(a,b)+.05)/(Math.min(a,b)+.05)).toFixed(2),background:s.backgroundColor,color:s.color}})()`)
      if (hover.ratio < 4.5) throw new Error(`light theme toggle hover contrast failed on ${name}: ${JSON.stringify(hover)}`)
      await call('Input.dispatchKeyEvent', { type:'keyDown', key:'Tab', code:'Tab', windowsVirtualKeyCode:9 }); await call('Input.dispatchKeyEvent', { type:'keyUp', key:'Tab', code:'Tab', windowsVirtualKeyCode:9 })
      const focus = await evalJS(`(()=>{const e=([...document.querySelectorAll('[data-theme-toggle]')].find(e=>e.getBoundingClientRect().width>0)||document.querySelector('[data-theme-toggle]'));e.focus();const s=getComputedStyle(e);return {visible:e.matches(':focus-visible'),outline:s.outlineStyle,width:parseFloat(s.outlineWidth),color:s.outlineColor}})()`)
      if (focus.outline === 'none' || focus.width < 2) throw new Error(`light theme toggle focus indicator missing on ${name}: ${JSON.stringify(focus)}`)
      result.interaction = { hover, focus }
    }
    if (name === 'owner' && width === 1440) {
      const logoutRect = await evalJS(`(()=>{const menu=document.querySelector('.workspace-menu');if(menu)menu.open=true;const all=[...document.querySelectorAll('.sidebar-logout button,.menu-logout button')];const e=all.find(x=>x.getBoundingClientRect().width>0)||all[0];const r=e?.getBoundingClientRect();return r?{x:r.x+r.width/2,y:r.y+r.height/2,selector:e.matches('.sidebar-logout button')?'.sidebar-logout button':'.menu-logout button'}:null})()`)
      if (!logoutRect) throw new Error('light theme owner view has no visible logout control')
      if (logoutRect) {
        await call('Input.dispatchMouseEvent', { type:'mouseMoved', x:logoutRect.x, y:logoutRect.y }); await wait(80)
        result.logoutHover = await evalJS(`(()=>{const e=document.querySelector('${logoutRect.selector}'),s=getComputedStyle(e);const rgb=v=>(v.match(/[\\d.]+/g)||[]).slice(0,3).map(Number);const l=c=>c.map(v=>v/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((a,v,i)=>a+v*[.2126,.7152,.0722][i],0);const a=l(rgb(s.color)),b=l(rgb(s.backgroundColor==='rgba(0, 0, 0, 0)'?'rgb(255,255,255)':s.backgroundColor));return {color:s.color,background:s.backgroundColor,ratio:+((Math.max(a,b)+.05)/(Math.min(a,b)+.05)).toFixed(2),visible:e.getBoundingClientRect().width>0}})()`)
        if (!result.logoutHover.visible || result.logoutHover.ratio < 4.5) throw new Error(`light theme logout hover contrast failed: ${JSON.stringify(result.logoutHover)}`)
        await evalJS(`document.querySelector('.workspace-menu').open=false`)
      }
    }
    if (name === 'owner' && width === 390) {
      const menuLogout = await evalJS(`(()=>{const m=document.querySelector('.workspace-menu');if(m)m.open=true;const e=document.querySelector('.menu-logout button'),r=e?.getBoundingClientRect();return r&&r.width>0&&r.height>0?{x:r.x+r.width/2,y:r.y+r.height/2}:null})()`)
      if (!menuLogout) throw new Error('light theme phone menu has no visible logout control')
      await call('Input.dispatchMouseEvent', { type:'mouseMoved', x:menuLogout.x, y:menuLogout.y }); await wait(80)
      result.mobileLogoutHover = await evalJS(`(()=>{const e=document.querySelector('.menu-logout button'),s=getComputedStyle(e);const rgb=v=>(v.match(/[\\d.]+/g)||[]).slice(0,3).map(Number);const l=c=>c.map(v=>v/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((a,v,i)=>a+v*[.2126,.7152,.0722][i],0);let bg=s.backgroundColor;if(bg==='rgba(0, 0, 0, 0)')bg=getComputedStyle(e.parentElement).backgroundColor;const a=l(rgb(s.color)),b=l(rgb(bg));return {visible:e.getBoundingClientRect().width>0,ratio:+((Math.max(a,b)+.05)/(Math.min(a,b)+.05)).toFixed(2),background:bg,color:s.color}})()`)
      if (!result.mobileLogoutHover.visible || result.mobileLogoutHover.ratio < 4.5) throw new Error(`light theme phone menu logout hover contrast failed: ${JSON.stringify(result.mobileLogoutHover)}`)
      await call('Input.dispatchKeyEvent', { type:'keyDown', key:'Tab', code:'Tab', windowsVirtualKeyCode:9 }); await call('Input.dispatchKeyEvent', { type:'keyUp', key:'Tab', code:'Tab', windowsVirtualKeyCode:9 })
      result.mobileLogoutFocus = await evalJS(`(()=>{const e=document.querySelector('.menu-logout button');e.focus();const s=getComputedStyle(e);return {visible:e.matches(':focus-visible'),outline:s.outlineStyle,width:parseFloat(s.outlineWidth)}})()`)
      if (result.mobileLogoutFocus.outline === 'none' || result.mobileLogoutFocus.width < 2) throw new Error(`light theme phone menu logout focus indicator missing: ${JSON.stringify(result.mobileLogoutFocus)}`)
      await evalJS(`document.querySelector('.workspace-menu').open=false`)
    }
    await snap(`light-${name}-${width}.png`, false)
    return result
  }
  const portalCookie = (await call('Network.getAllCookies')).cookies.find(item => item.name === '__Host-clipp-portal' && item.domain === 'localhost')
  if (!portalCookie) throw new Error('light-theme sweep could not preserve the disposable portal session')
  await call('Network.deleteCookies', { name:portalCookie.name, url:`${base}/` })
  lightThemeSweep['signed-out-1440'] = await inspectLight('signed-out', 1440, '/')
  await call('Network.setCookie', { name:portalCookie.name, value:portalCookie.value, url:`${base}/`, path:'/', secure:true, httpOnly:true, sameSite:portalCookie.sameSite })
  for (const width of [1440, 768, 390, 320]) lightThemeSweep[`owner-${width}`] = await inspectLight('owner', width, '/')
  await call('Page.navigate', { url:`${base}/` }); await ready(); await wait(180)
  await evalJS(`document.querySelector('.connect-clipp')?.click()`)
  const closeState = await evalJS(`(()=>{const d=document.querySelector('.connect-dialog'),e=d?.querySelector('.dialog-close'),r=e?.getBoundingClientRect();return {open:d?.open===true,close:!!e,visible:!!r&&r.width>0&&r.height>0,rect:r?{x:r.x+r.width/2,y:r.y+r.height/2}:null}})()`)
  if (!closeState.open || !closeState.close || !closeState.visible) throw new Error(`light theme Connect Clipp dialog/close control is missing: ${JSON.stringify(closeState)}`)
  const closeRect = closeState.rect
  if (closeRect) {
    await call('Input.dispatchMouseEvent', { type:'mouseMoved', x:closeRect.x, y:closeRect.y }); await wait(80)
    lightThemeSweep.dialogCloseHover = await evalJS(`(()=>{const e=document.querySelector('.connect-dialog .dialog-close'),s=getComputedStyle(e);const rgb=v=>(v.match(/[\\d.]+/g)||[]).slice(0,3).map(Number);const l=c=>c.map(v=>v/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((a,v,i)=>a+v*[.2126,.7152,.0722][i],0);const a=l(rgb(s.color)),b=l(rgb(s.backgroundColor));return {ratio:+((Math.max(a,b)+.05)/(Math.min(a,b)+.05)).toFixed(2),background:s.backgroundColor,color:s.color}})()`)
    if (lightThemeSweep.dialogCloseHover.ratio < 4.5) throw new Error(`light theme dialog-close hover contrast failed: ${JSON.stringify(lightThemeSweep.dialogCloseHover)}`)
    await evalJS(`document.querySelector('.connect-dialog .dialog-close').focus()`)
    lightThemeSweep.dialogCloseFocus = await evalJS(`(()=>{const e=document.querySelector('.connect-dialog .dialog-close'),s=getComputedStyle(e);return {visible:e.matches(':focus-visible'),outline:s.outlineStyle,width:parseFloat(s.outlineWidth)}})()`)
    if (lightThemeSweep.dialogCloseFocus.outline === 'none' || lightThemeSweep.dialogCloseFocus.width < 2) throw new Error(`light theme dialog-close focus indicator missing: ${JSON.stringify(lightThemeSweep.dialogCloseFocus)}`)
    await evalJS(`document.querySelector('.connect-dialog .dialog-close').click()`)
  }
  for (const [name, path] of [['security','/account/security'],['admin','/admin'],['admin-detail','/admin?selected=66000000-0000-4000-8000-000000000001&view=detail'],['plans','/admin/plans']]) {
    lightThemeSweep[name] = await inspectLight(name, 1440, path)
    lightThemeSweep[`${name}-phone`] = await inspectLight(name, 390, path)
  }
  lightThemeSweep['error-1440'] = await inspectLight('error', 1440, '/auth/deletion/not-a-valid-id')
  lightThemeSweep['error-phone'] = await inspectLight('error', 390, '/auth/deletion/not-a-valid-id')
  await evalJS(`localStorage.setItem('clipp-relay-appearance','dark')`)
  await navigate(1440)
  lightThemeSweep.restoredDark = await evalJS(`({theme:document.documentElement.dataset.theme,stored:localStorage.getItem('clipp-relay-appearance')})`)
  if (lightThemeSweep.restoredDark.theme !== 'dark') throw new Error(`theme sweep did not restore dark appearance: ${JSON.stringify(lightThemeSweep.restoredDark)}`)
  const inspectDarkInteraction = async (name, selector) => {
    const rect = await evalJS(`(()=>{const r=document.querySelector(${JSON.stringify(selector)})?.getBoundingClientRect();return r&&r.width>0&&r.height>0?{x:r.x+r.width/2,y:r.y+r.height/2}:null})()`)
    if (!rect) throw new Error(`dark theme ${name} control is missing or hidden`)
    await call('Input.dispatchMouseEvent', { type:'mouseMoved', x:rect.x, y:rect.y }); await wait(80)
    const hover = await evalJS(`(()=>{const e=document.querySelector(${JSON.stringify(selector)}),s=getComputedStyle(e);const rgb=v=>(v.match(/[\\d.]+/g)||[]).slice(0,3).map(Number);const l=c=>c.map(v=>v/255).map(v=>v<=.04045?v/12.92:((v+.055)/1.055)**2.4).reduce((a,v,i)=>a+v*[.2126,.7152,.0722][i],0);let bg=s.backgroundColor;if(bg==='rgba(0, 0, 0, 0)')bg=getComputedStyle(e.parentElement).backgroundColor;const a=l(rgb(s.color)),b=l(rgb(bg));return {ratio:+((Math.max(a,b)+.05)/(Math.min(a,b)+.05)).toFixed(2),background:bg,color:s.color}})()`)
    if (hover.ratio < 4.5) throw new Error(`dark theme ${name} hover contrast failed: ${JSON.stringify(hover)}`)
    await call('Input.dispatchKeyEvent', { type:'keyDown', key:'Tab', code:'Tab', windowsVirtualKeyCode:9 }); await call('Input.dispatchKeyEvent', { type:'keyUp', key:'Tab', code:'Tab', windowsVirtualKeyCode:9 })
    const focus = await evalJS(`(()=>{const e=document.querySelector(${JSON.stringify(selector)});e.focus();const s=getComputedStyle(e);return {visible:e.matches(':focus-visible'),outline:s.outlineStyle,width:parseFloat(s.outlineWidth),color:s.outlineColor}})()`)
    if (focus.outline === 'none' || focus.width < 2) throw new Error(`dark theme ${name} focus indicator missing: ${JSON.stringify(focus)}`)
    return {hover,focus}
  }
  await evalJS(`document.querySelector('.workspace-menu').open=true`)
  lightThemeSweep.darkThemeToggle = await inspectDarkInteraction('theme toggle','.workspace-sidebar [data-theme-toggle]')
  await evalJS(`(()=>{const m=document.querySelector('.workspace-menu');if(m)m.open=true})()`)
  const darkLogout = await evalJS(`(()=>[...document.querySelectorAll('.sidebar-logout button,.menu-logout button')].find(e=>{const r=e.getBoundingClientRect();return r.width>0&&r.height>0})?.matches('.sidebar-logout button')?'.sidebar-logout button':'.menu-logout button')()`)
  lightThemeSweep.darkLogout = await inspectDarkInteraction('logout',darkLogout)
  await call('Emulation.setDeviceMetricsOverride', { width:390, height:844, deviceScaleFactor:1, mobile:true })
  await call('Page.navigate', { url:`${base}/` }); await ready(); await wait(180)
  await evalJS(`(()=>{const m=document.querySelector('.workspace-menu');if(m)m.open=true})()`)
  lightThemeSweep.darkMobileMenuLogout = await inspectDarkInteraction('phone menu logout','.menu-logout button')
  await navigate(1440)
  await evalJS(`(()=>{const m=document.querySelector('.workspace-menu');if(m)m.open=false;document.querySelector('.connect-clipp')?.click()})()`)
  const darkDialog = await evalJS(`(()=>({open:document.querySelector('.connect-dialog')?.open===true,close:!!document.querySelector('.connect-dialog .dialog-close')}))()`)
  if (!darkDialog.open || !darkDialog.close) throw new Error(`dark theme Connect Clipp dialog is not open: ${JSON.stringify(darkDialog)}`)
  lightThemeSweep.darkDialogClose = await inspectDarkInteraction('dialog close','.connect-dialog .dialog-close')
  await evalJS(`document.querySelector('.connect-dialog .dialog-close').click()`)
  if (process.env.PORTAL_APP_INSPECTOR) {
    const cookie = (await call('Network.getAllCookies')).cookies.find(item => item.name === '__Host-clipp-portal' && item.domain === 'localhost')?.value
    if (!cookie) throw new Error('authenticated browser did not expose its disposable portal session to the app inspector')
    const inspect = async (name, path, authenticated = true) => {
      const out = join(artifacts, 'app-inspector', name)
      await mkdir(out, { recursive: true })
      const run = spawnSync(process.execPath, [process.env.PORTAL_APP_INSPECTOR, `${base}${path}`, '--out', out, '--widths', '1440,768,390', '--app', '--allow', 'palette-purple', '--json'], {
        encoding: 'utf8',
        env: { ...process.env, TASTE_INSPECT_COOKIE: authenticated ? cookie : '' },
      })
      await writeFile(join(out, 'run.log'), `${run.stdout || ''}${run.stderr || ''}`)
      if (run.error || run.status !== 0) throw new Error(`app inspector failed for ${name}: ${run.error || `exit ${run.status}`}`)
      const report = JSON.parse(run.stdout)
      const failures = report.findings.filter(finding => finding.level === 'FAIL')
      if (failures.length) throw new Error(`app inspector found FAILs for ${name}: ${JSON.stringify(failures)}`)
    }
    await inspect('signed-out-entry', '/', false)
    await inspect('owner-account', '/')
    await inspect('owner-security', '/account/security')
    await inspect('admin-list', '/admin')
    await inspect('admin-detail', '/admin?selected=66000000-0000-4000-8000-000000000001&view=detail')
    await inspect('admin-plans', '/admin/plans')
    const inspectConsent = new URLSearchParams({ response_type: 'code', client_id: 'electron', redirect_uri: 'http://127.0.0.1:34567/oauth/callback', code_challenge_method: 'S256', code_challenge: 'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM', state: 'browser-consent-inspector-state-10' })
    await inspect('owner-consent', `/oauth/authorize?${inspectConsent}`)
  }
  const evidence = { url: base, signedOut, clipboardSuccess, clipboardRefusal, auth: facts, theme: themeEvidence, lightThemeSweep, viewports: {}, keyboard: {}, noScript: {}, failures: {} }

  const electronCallback = 'http://127.0.0.1:34567/oauth/callback'
  const consentState = decision => `browser-consent-${decision}-state-10`
  const consentURL = state => {
    const query = new URLSearchParams({ response_type:'code', client_id:'electron', redirect_uri:electronCallback, code_challenge_method:'S256', code_challenge:'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM', state })
    return `${base}/oauth/authorize?${query}`
  }
  evidence.consent = { viewports: {} }
  await call('Page.navigate', { url: consentURL(consentState('cancel-key')) }); await ready(); await wait(150)
  evidence.consent.page = await evalJS(`({title:document.querySelector('h1')?.innerText,email:document.body.innerText.includes('workspace-a@example.test'),host:document.body.innerText.includes('localhost:18443'),client:document.querySelector('h1')?.innerText.includes('Clipp desktop'),allowance:document.body.innerText.includes('saved allowance'),identityBoundary:document.body.innerText.includes('does not bind a Device Identity or add a Device Network member'),authorize:document.querySelector('button[value="authorize"]')?.innerText.trim(),cancel:document.querySelector('button[value="cancel"]')?.innerText.trim(),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  if (!evidence.consent.page.email || !evidence.consent.page.host || !evidence.consent.page.client || !evidence.consent.page.allowance || !evidence.consent.page.identityBoundary || !evidence.consent.page.authorize.includes('Clipp desktop') || !evidence.consent.page.cancel || evidence.consent.page.overflow) throw new Error(`consent identity/permission presentation failed: ${JSON.stringify(evidence.consent.page)}`)
  for (const width of [1440, 768, 390, 320]) {
    await call('Emulation.setDeviceMetricsOverride', { width, height: viewportHeight(width), deviceScaleFactor: 1, mobile: width < 640 })
    evidence.consent.viewports[width] = await evalJS(`({width:innerWidth,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,verticalOverflow:document.documentElement.scrollHeight>document.documentElement.clientHeight,authorizeVisible:document.querySelector('button[value="authorize"]').getBoundingClientRect().width>0,cancelVisible:document.querySelector('button[value="cancel"]').getBoundingClientRect().width>0})`)
    await snap(`consent-${width}.png`, false)
    if (evidence.consent.viewports[width].overflow || evidence.consent.viewports[width].verticalOverflow || !evidence.consent.viewports[width].authorizeVisible || !evidence.consent.viewports[width].cancelVisible) throw new Error(`consent ${width}px layout failed: ${JSON.stringify(evidence.consent.viewports[width])}`)
  }
  await writeFile(join(artifacts, 'consent-acceptance.json'), JSON.stringify({ url: base, viewports: evidence.consent.viewports }, null, 2) + '\n')
  await call('Emulation.setDeviceMetricsOverride', { width: 390, height: 844, deviceScaleFactor: 1, mobile: true })
  await evalJS(`document.querySelector('button[value="cancel"]').focus()`)
  evidence.consent.keyboardCancelFocused = await evalJS(`document.activeElement?.value==='cancel'`)
  if (!evidence.consent.keyboardCancelFocused) throw new Error('consent Cancel button cannot receive keyboard focus')
  await evalJS(`window.__consentKeyboardEvents=[];for(const type of ['keydown','keypress','keyup','click','submit'])document.addEventListener(type,event=>window.__consentKeyboardEvents.push({type,key:event.key,target:event.target?.value||event.target?.tagName,active:document.activeElement?.value||document.activeElement?.tagName}),true)`)
  responses.length = 0
  await call('Input.dispatchKeyEvent', { type:'rawKeyDown', key:' ', code:'Space', windowsVirtualKeyCode:32 })
  await call('Input.dispatchKeyEvent', { type:'keyUp', key:' ', code:'Space', windowsVirtualKeyCode:32 })
  evidence.consent.keyboardEvents = await evalJS(`window.__consentKeyboardEvents||[]`).catch(() => [])
  evidence.consent.keyboardCancel = { focused:evidence.consent.keyboardCancelFocused, activated:evidence.consent.keyboardEvents.some(event=>event.type==='click'&&event.target==='cancel'), submitted:evidence.consent.keyboardEvents.some(event=>event.type==='submit') }
  if (!evidence.consent.keyboardCancel.activated || !evidence.consent.keyboardCancel.submitted) throw new Error(`keyboard Cancel did not activate and submit its native form: ${JSON.stringify(evidence.consent.keyboardEvents)}`)
  await wait(500)

  await call('Page.navigate', { url: consentURL(consentState('cancel-no-script')) }); await ready(); await wait(150)
  for (let i = 0; i < 50; i++) {
    const loadedConsent = await evalJS(`location.pathname==='/oauth/authorize'&&document.readyState==='complete'&&!!document.querySelector('button[value="cancel"]')`)
    if (loadedConsent) break
    await wait(100)
    if (i === 49) throw new Error(`no-script consent did not load: ${await evalJS(`JSON.stringify({url:location.href,title:document.title})`)}`)
  }
  await snap('consent-cancel-no-script-390.png', false)
  await call('Emulation.setScriptExecutionDisabled', { value: true })
  responses.length = 0
  await call('DOM.enable')
  const cancelDocument = await call('DOM.getDocument', { depth: 1 })
  const cancelNode = await call('DOM.querySelector', { nodeId: cancelDocument.root.nodeId, selector:'button[value="cancel"]' })
  if (!cancelNode.nodeId) throw new Error('consent Cancel form control missing without scripts')
  requests.length = 0
  await clickNode(cancelNode.nodeId); await wait(500)
  const noScriptCancelCallback = [...requests].reverse().find(x => x.url?.startsWith(`${electronCallback}?`) && x.redirectStatus >= 300 && x.redirectStatus < 400)
  if (!noScriptCancelCallback) throw new Error(`no-script cancel did not return to its callback: requests=${JSON.stringify(requests.filter(x=>!x.url.startsWith('data:')).slice(-8))}`)
  const noScriptCancelled = new URL(noScriptCancelCallback.url)
  evidence.consent.cancel = { status:noScriptCancelCallback.redirectStatus, error:noScriptCancelled.searchParams.get('error'), state:noScriptCancelled.searchParams.get('state'), code:noScriptCancelled.searchParams.get('code') }
  if (evidence.consent.cancel.error !== 'access_denied' || evidence.consent.cancel.state !== consentState('cancel-no-script') || evidence.consent.cancel.code) throw new Error(`no-script cancel callback did not preserve denial/state without code: ${JSON.stringify(evidence.consent.cancel)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })

  await call('Page.navigate', { url: consentURL(consentState('authorize-key')) }); await ready(); await wait(150)
  await evalJS(`document.querySelector('button[value="authorize"]').focus();window.__consentKeyboardEvents=[];for(const type of ['keydown','keyup','click','submit'])document.addEventListener(type,event=>{window.__consentKeyboardEvents.push({type,key:event.key,target:event.target?.value||event.target?.tagName});if(type==='submit')event.preventDefault()},true)`)
  await call('Input.dispatchKeyEvent', { type:'rawKeyDown', key:' ', code:'Space', windowsVirtualKeyCode:32 })
  await call('Input.dispatchKeyEvent', { type:'keyUp', key:' ', code:'Space', windowsVirtualKeyCode:32 })
  evidence.consent.keyboardAuthorizeEvents = await evalJS(`window.__consentKeyboardEvents||[]`).catch(() => [])
  evidence.consent.keyboardAuthorize = { focused:await evalJS(`document.activeElement?.value==='authorize'`).catch(() => false), activated:evidence.consent.keyboardAuthorizeEvents.some(event=>event.type==='click'&&event.target==='authorize'), submitted:evidence.consent.keyboardAuthorizeEvents.some(event=>event.type==='submit') }
  if (!evidence.consent.keyboardAuthorize.activated || !evidence.consent.keyboardAuthorize.submitted) throw new Error(`keyboard Authorize did not activate and submit its native form: ${JSON.stringify(evidence.consent.keyboardAuthorizeEvents)}`)

  await call('Page.navigate', { url: consentURL(consentState('authorize')) }); await ready(); await wait(150)
  await call('Emulation.setScriptExecutionDisabled', { value: true })
  responses.length = 0
  await call('DOM.enable')
  const consentDocument = await call('DOM.getDocument', { depth: 1 })
  const authorizeNode = await call('DOM.querySelector', { nodeId: consentDocument.root.nodeId, selector:'button[value="authorize"]' })
  if (!authorizeNode.nodeId) {
    const page = await evalJS(`({url:location.href,title:document.querySelector('h1')?.innerText,text:document.body.innerText.slice(0,1000)})`).catch(error=>({evaluationError:String(error)}))
    await snap('consent-authorize-no-script-missing.png', false)
    throw new Error(`consent Authorize form control missing without scripts: ${JSON.stringify(page)}`)
  }
  requests.length = 0
  await clickNode(authorizeNode.nodeId); await wait(500)
  const authorizationCallback = [...requests].reverse().find(x => x.url?.startsWith(`${electronCallback}?`) && x.redirectStatus >= 300 && x.redirectStatus < 400)
  if (!authorizationCallback) throw new Error(`no-script authorize did not return to its callback: ${JSON.stringify(requests.filter(x=>!x.url.startsWith('data:')).slice(-8))}`)
  const authorized = new URL(authorizationCallback.url)
  evidence.consent.authorize = { status:authorizationCallback.redirectStatus, state:authorized.searchParams.get('state'), codePresent:!!authorized.searchParams.get('code'), error:authorized.searchParams.get('error') }
  if (evidence.consent.authorize.state !== consentState('authorize') || !evidence.consent.authorize.codePresent || evidence.consent.authorize.error) throw new Error(`authorize callback missing its registered code/state: ${JSON.stringify(evidence.consent.authorize)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })

  for (const width of [1440, 768, 390, 320]) {
    await navigate(width)
    const result = await evalJS(`({width:innerWidth,height:innerHeight,scrollWidth:document.documentElement.scrollWidth,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,historyScroll:document.querySelector('.history-chart-scroll').scrollWidth>document.querySelector('.history-chart-scroll').clientWidth,historyChartSize:{scroll:document.querySelector('.history-chart-scroll').scrollWidth,client:document.querySelector('.history-chart-scroll').clientWidth,svg:document.querySelector('.history-histogram').getBoundingClientRect().width},sidebar:getComputedStyle(document.querySelector('.workspace-sidebar')).display,menu:getComputedStyle(document.querySelector('.workspace-menu')).display,account:document.body.innerText.includes('Your account'),allowance:document.body.innerText.includes('Your allowance'),connect:[...document.querySelectorAll('.heading-actions a')].some(a=>a.textContent.includes('Connect Clipp')&&a.getBoundingClientRect().top<innerHeight),connectHref:document.querySelector('.heading-actions a.connect-clipp')?.getAttribute('href'),historyRows:document.querySelectorAll('.history-table tbody tr').length,currentVisible:!![...document.querySelectorAll('.history-table tr')].find(row=>row.textContent.includes('current week')),zeroDistinct:document.querySelector('.history-table').textContent.includes('0 bytes'),missingDistinct:document.querySelector('.history-table').textContent.includes('No observation'),historyRange:document.querySelector('.history-range')?.innerText,folded:!document.querySelector('.history-table-details').open,axis:!!document.querySelector('.history-axis-fixed')})`)
    evidence.viewports[width] = result
    await snap(`account-${width}.png`)
    if (result.overflow || !result.account || !result.allowance || !result.connect || result.connectHref !== '#connection-setup') throw new Error(`${width}px acceptance failed: ${JSON.stringify(result)}`)
    if (result.historyRows < 4 || !result.currentVisible || !result.zeroDistinct || !result.missingDistinct || !result.historyRange.includes('Weekly') || !result.folded || !result.axis) throw new Error(`${width}px recent history acceptance failed: ${JSON.stringify(result)}`)
    if ((width > 1100 && (result.sidebar === 'none' || result.menu !== 'none')) || (width <= 1100 && (result.sidebar !== 'none' || result.menu === 'none'))) throw new Error(`${width}px shell navigation state failed: ${JSON.stringify(result)}`)
  }
  await navigate(390)
  const historyBefore = await evalJS(`(()=>{const axis=document.querySelector('.history-axis-fixed').getBoundingClientRect(),graph=document.querySelector('.history-chart-scroll'),bars=[...document.querySelectorAll('.history-column')],link=document.querySelector('[data-history-more]');return {rows:document.querySelectorAll('.history-table tbody tr').length,axisLeft:axis.left,folded:!document.querySelector('.history-table-details').open,showMoreVisible:!!link?.getClientRects().length,atRight:Math.abs(graph.scrollWidth-graph.clientWidth-graph.scrollLeft)<2,first:bars[0]?.getAttribute('aria-label'),last:bars.at(-1)?.getAttribute('aria-label')}})()`)
  if (!historyBefore.folded || historyBefore.showMoreVisible || !historyBefore.atRight || !historyBefore.last?.includes('current week')) throw new Error(`history initial order or visibility failed: ${JSON.stringify(historyBefore)}`)
  await evalJS(`document.querySelector('.history-table-details summary').click()`)
  if (!(await evalJS(`document.querySelector('[data-history-more]').getClientRects().length>0`))) throw new Error('Show more was not visible in the expanded table')
  await evalJS(`(()=>{const graph=document.querySelector('.history-chart-scroll');graph.scrollLeft=0;graph.dispatchEvent(new Event('scroll'))})()`)
  await wait(1000)
  evidence.historyScroll = await evalJS(`(()=>({rows:document.querySelectorAll('.history-table tbody tr').length,bars:document.querySelectorAll('.history-column').length,axisLeft:document.querySelector('.history-axis-fixed').getBoundingClientRect().left,open:document.querySelector('.history-table-details').open}))()`)
  if (!historyBefore.folded || !evidence.historyScroll.open || evidence.historyScroll.rows <= historyBefore.rows || evidence.historyScroll.rows !== evidence.historyScroll.bars || Math.abs(evidence.historyScroll.axisLeft-historyBefore.axisLeft)>1) throw new Error(`history scroll did not load matching table rows with a fixed axis: ${JSON.stringify({historyBefore,after:evidence.historyScroll})}`)
  await evalJS(`document.querySelector('[aria-label="History grouping"] a').click()`); await ready(); await wait(500)
  evidence.monthlyHistory = await evalJS(`({group:document.querySelector('[aria-label="History grouping"] [aria-current="page"]').textContent.trim(),folded:!document.querySelector('.history-table-details').open,dates:[...document.querySelectorAll('.history-table tbody time')].map(x=>x.dateTime),bars:document.querySelectorAll('.history-column').length})`)
  if (evidence.monthlyHistory.group !== 'Monthly' || !evidence.monthlyHistory.folded || evidence.monthlyHistory.dates.length !== evidence.monthlyHistory.bars || evidence.monthlyHistory.dates.some(date=>!/^\d{4}-\d{2}$/.test(date))) throw new Error(`monthly graph/table mismatch: ${JSON.stringify(evidence.monthlyHistory)}`)
  await navigate(390)
  await call('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-reduced-motion', value: 'reduce' }] })
  evidence.reducedMotion = await evalJS(`({requested:matchMedia('(prefers-reduced-motion: reduce)').matches,animated:document.getAnimations().filter(animation=>animation.effect?.getComputedTiming().duration>0).length,transitions:[...document.querySelectorAll('*')].filter(element=>{const style=getComputedStyle(element);return style.transitionDuration.split(',').some(value=>parseFloat(value)>0)}).length,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('account-reduced-motion-390.png')
  if (!evidence.reducedMotion.requested || evidence.reducedMotion.animated || evidence.reducedMotion.transitions || evidence.reducedMotion.overflow) throw new Error(`reduced-motion acceptance failed: ${JSON.stringify(evidence.reducedMotion)}`)
  await call('Emulation.setEmulatedMedia', { features: [] })
  await navigate(1440)
  await evalJS(`document.querySelector('.connect-clipp').click()`)
  evidence.connectionEntry = await evalJS(`({hash:location.hash,open:document.querySelector('#connect-dialog')?.matches(':modal')===true,section:!!document.querySelector('#connection-setup'),url:document.querySelector('#discovery-url')?.value,setupAfterHistory:!!(document.querySelector('#history-title')?.closest('section').compareDocumentPosition(document.querySelector('#connection-setup'))&Node.DOCUMENT_POSITION_FOLLOWING),instructions:document.querySelector('.setup-instructions summary')?.innerText,steps:document.querySelectorAll('.setup-instructions .setup-steps li').length,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('connection-setup-1440.png')
  if (!evidence.connectionEntry.open || !evidence.connectionEntry.setupAfterHistory || evidence.connectionEntry.url !== `${base}/v1/relay` || evidence.connectionEntry.steps !== 3 || evidence.connectionEntry.overflow) throw new Error(`Connect Clipp did not open the contextual setup beside history: ${JSON.stringify(evidence.connectionEntry)}`)
  await navigate(390)
  await evalJS(`document.querySelector('.connect-clipp').click()`)
  evidence.connectionEntry.phone = await evalJS(`({open:document.querySelector('#connect-dialog')?.matches(':modal')===true,visible:document.querySelector('#discovery-url').getBoundingClientRect().width>0,copyVisible:(()=>{const b=document.querySelector('[data-copy]').getBoundingClientRect();return b.width>0&&b.height>=44})(),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('connection-setup-390.png')
  if (!evidence.connectionEntry.phone.open || !evidence.connectionEntry.phone.visible || !evidence.connectionEntry.phone.copyVisible || evidence.connectionEntry.phone.overflow) throw new Error(`phone connection setup is unreachable: ${JSON.stringify(evidence.connectionEntry.phone)}`)
  await call('Emulation.setDeviceMetricsOverride', { width:390, height:844, deviceScaleFactor:1, mobile:true })
  await call('Page.navigate', { url:`${base}/?history_rows=14` }); await ready(); await wait(250)
  evidence.fullHistoryPhone = await evalJS(`({width:innerWidth,height:innerHeight,scrollWidth:document.documentElement.scrollWidth,rows:document.querySelectorAll('.history-table tbody tr').length,currentVisible:!![...document.querySelectorAll('.history-table tr')].find(row=>row.textContent.includes('current week')),chartVisible:getComputedStyle(document.querySelector('.history-chart-scroll')).display!=='none',dates:[...document.querySelectorAll('.history-table tbody th time')].map(x=>x.dateTime)})`)
  await snap('account-history-all-390.png')
  if (evidence.fullHistoryPhone.width!==390 || evidence.fullHistoryPhone.height!==844 || evidence.fullHistoryPhone.scrollWidth>390 || (evidence.fullHistoryPhone.rows<13 || evidence.fullHistoryPhone.rows>14) || !evidence.fullHistoryPhone.currentVisible || !evidence.fullHistoryPhone.chartVisible || evidence.fullHistoryPhone.dates[0] <= evidence.fullHistoryPhone.dates.at(-1)) throw new Error(`all history phone acceptance failed: ${JSON.stringify(evidence.fullHistoryPhone)}`)
  await navigate(390)
  await evalJS(`document.querySelector('.workspace-menu summary').focus()`)
  await call('Input.dispatchKeyEvent', { type:'rawKeyDown', key:'Enter', code:'Enter', windowsVirtualKeyCode:13 }); await call('Input.dispatchKeyEvent', { type:'keyUp', key:'Enter', code:'Enter', windowsVirtualKeyCode:13 })
  evidence.keyboard.open = await evalJS(`({open:document.querySelector('.workspace-menu').open,focus:document.activeElement.tagName,key:'Enter'})`)
  if (!evidence.keyboard.open.open) {
    await call('Input.dispatchKeyEvent', { type:'rawKeyDown', key:' ', code:'Space', windowsVirtualKeyCode:32 }); await call('Input.dispatchKeyEvent', { type:'keyUp', key:' ', code:'Space', windowsVirtualKeyCode:32 })
    evidence.keyboard.open = await evalJS(`({open:document.querySelector('.workspace-menu').open,focus:document.activeElement.tagName,key:'Space'})`)
  }
  await call('Input.dispatchKeyEvent', { type: 'keyDown', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 }); await call('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Escape', code: 'Escape', windowsVirtualKeyCode: 27 })
  evidence.keyboard.escape = await evalJS(`({open:document.querySelector('.workspace-menu').open,focus:document.activeElement.matches('.workspace-menu summary')})`)
  if (!evidence.keyboard.open.open || evidence.keyboard.escape.open || !evidence.keyboard.escape.focus) throw new Error(`keyboard menu acceptance failed: ${JSON.stringify(evidence.keyboard)}`)
  evidence.menuLayout = {};
  for (const width of [320, 390, 768]) {
    await navigate(width)
    const layout = await evalJS(`(()=>{const m=document.querySelector('.workspace-menu');m.open=true;const nav=m.querySelector('nav');const items=[...nav.children].filter(e=>e.getBoundingClientRect().height>0).map(e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,right:r.right,bottom:r.bottom}});const b=nav.querySelector('[data-theme-toggle]');return {items,visible:b.getBoundingClientRect().width>0}})()`)
    if (!layout.visible || layout.items.some((r,i)=>r.x<0||r.right>width||(i>0&&r.y<layout.items[i-1].bottom-1))) throw new Error(`menu is not a single visible column at ${width}px: ${JSON.stringify(layout)}`)
    evidence.menuLayout[width] = layout
  }
  evidence.keyboard.logout = await evalJS(`({action:document.querySelector('.workspace-menu form')?.getAttribute('action'),method:document.querySelector('.workspace-menu form')?.getAttribute('method'),csrf:!!document.querySelector('.workspace-menu form input[name="csrf"]'),label:document.querySelector('.workspace-menu form button')?.textContent.trim()})`)
  if (evidence.keyboard.logout.action !== '/auth/logout' || evidence.keyboard.logout.method !== 'post' || !evidence.keyboard.logout.csrf || evidence.keyboard.logout.label !== 'Log out') throw new Error(`menu logout form failed: ${JSON.stringify(evidence.keyboard.logout)}`)
  // Simulate 150% browser zoom on a 390px screen as a 260 CSS-pixel viewport
  // rendered at 1.5x. The override is disposable and does not touch Chrome prefs.
  await call('Emulation.setDeviceMetricsOverride', { width:260, height:1000, deviceScaleFactor:1.5, mobile:false })
  await call('Page.navigate', { url:`${base}/` }); await ready(); await wait(250)
  evidence.enlargedText = await evalJS(`({devicePixelRatio,viewport:innerWidth,scroll:document.documentElement.scrollWidth,client:document.documentElement.clientWidth,account:document.body.innerText.includes('Your account'),bodyMin:getComputedStyle(document.body).minWidth,bodyWidth:document.body.getBoundingClientRect().width,workspaceWidth:document.querySelector('.workspace-main').getBoundingClientRect().width,pageWidth:document.querySelector('main.page').getBoundingClientRect().width,cardWidth:document.querySelector('.quota-card').getBoundingClientRect().width,brandSuffix:getComputedStyle(document.querySelector('.mobile-brand .brand-product')).display,header:document.querySelector('.workspace-bar').getBoundingClientRect().width})`)
  await snap('account-enlarged-text.png')
  if (evidence.enlargedText.devicePixelRatio !== 1.5 || evidence.enlargedText.viewport !== 260 || evidence.enlargedText.scroll > evidence.enlargedText.client || !evidence.enlargedText.account) throw new Error(`150% zoom reflow failed: ${JSON.stringify(evidence.enlargedText)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: true }); await call('Page.navigate', { url: `${base}/` }); await wait(700)
  let dom = await call('DOM.getDocument', { depth: 0 }); let html = await call('DOM.getOuterHTML', { nodeId: dom.root.nodeId })
  evidence.noScript = { account: html.outerHTML.includes('Your account'), allowance: html.outerHTML.includes('Your allowance'), menu: html.outerHTML.includes('workspace-menu'), menuLinks: (html.outerHTML.match(/class="workspace-menu"[\s\S]*?<a /g) || []).length, styles: html.outerHTML.includes('/assets/portal.css'), connection: html.outerHTML.includes('id="connection-setup"'), setupInstructions: html.outerHTML.includes('View setup instructions for Clipp desktop, Android, and Chrome extension'), appAuthorization: html.outerHTML.includes('each Clipp app needs its own authorization') }
  await snap('account-no-script.png')
  if (!evidence.noScript.account || !evidence.noScript.allowance || !evidence.noScript.menu || evidence.noScript.menuLinks < 1 || !evidence.noScript.styles || !evidence.noScript.connection || !evidence.noScript.setupInstructions || !evidence.noScript.appAuthorization) throw new Error(`no-script acceptance failed: ${JSON.stringify(evidence.noScript)}`)
  const root = await call('DOM.getDocument', { depth: 0 })
  const menu = await call('DOM.querySelector', { nodeId: root.root.nodeId, selector: '.workspace-menu summary' })
  await call('DOM.focus', { nodeId: menu.nodeId }); await call('Input.dispatchKeyEvent', { type:'keyDown',key:'Enter',code:'Enter',windowsVirtualKeyCode:13 }); await call('Input.dispatchKeyEvent', { type:'keyUp',key:'Enter',code:'Enter',windowsVirtualKeyCode:13 })
  dom = await call('DOM.getDocument', { depth: 0 }); html = await call('DOM.getOuterHTML', { nodeId: dom.root.nodeId })
  if (!/<details[^>]*class="workspace-menu"[^>]*open/.test(html.outerHTML)) {
    await call('Input.dispatchKeyEvent', { type:'rawKeyDown',key:' ',code:'Space',windowsVirtualKeyCode:32 }); await call('Input.dispatchKeyEvent', { type:'keyUp',key:' ',code:'Space',windowsVirtualKeyCode:32 })
    dom = await call('DOM.getDocument', { depth: 0 }); html = await call('DOM.getOuterHTML', { nodeId: dom.root.nodeId })
  }
  evidence.noScript.nativeMenuOpens = /<details[^>]*class="workspace-menu"[^>]*open/.test(html.outerHTML)
  if (!evidence.noScript.nativeMenuOpens) throw new Error('native disclosure menu did not open without JavaScript')
  await call('Emulation.setScriptExecutionDisabled', { value: false })
  const adminNavigate = async (width, path = '/admin') => {
    await call('Emulation.setDeviceMetricsOverride', { width, height: viewportHeight(width), deviceScaleFactor: 1, mobile: width < 640 })
    await call('Page.navigate', { url: `${base}${path}` }); await ready(); await wait(1100)
  }
  evidence.admin = {}
  await adminNavigate(1440)
  evidence.admin.desktop = await evalJS(`({heading:document.querySelector('h1')?.innerText,pendingFinding:document.querySelector('.admin-finding')?.innerText,accounts:document.querySelector('.accounts-pane')?.innerText.includes('acceptance-01@example.test'),nextAccounts:[...document.querySelectorAll('a')].some(a=>a.innerText.includes('Next accounts')),quotaPlans:document.querySelector('#quota-plans')?.getAttribute('href')==='/admin/plans',workspace:document.body.classList.contains('workspace-page'),relayHost:document.querySelector('.workspace-relay')?.innerText,legacyPlans:!!document.querySelector('.plans-pane'),footer:!!document.querySelector('.site-footer'),scroll:document.documentElement.scrollWidth,client:document.documentElement.clientWidth})`)
  await snap('admin-list-1440.png', false)
  if (!evidence.admin.desktop.accounts || !evidence.admin.desktop.nextAccounts || !evidence.admin.desktop.quotaPlans || !evidence.admin.desktop.workspace || evidence.admin.desktop.relayHost !== 'localhost:18443' || evidence.admin.desktop.legacyPlans || evidence.admin.desktop.footer || !evidence.admin.desktop.pendingFinding.includes('across the service') || evidence.admin.desktop.scroll > evidence.admin.desktop.client) throw new Error(`desktop admin list acceptance failed: ${JSON.stringify(evidence.admin.desktop)}`)
  await adminNavigate(1440, '/admin#quota-plans')
  evidence.admin.legacyPlanAnchor = await evalJS(`({hash:location.hash,link:document.querySelector('#quota-plans')?.getAttribute('href'),visible:document.querySelector('#quota-plans')?.getBoundingClientRect().top<innerHeight,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('admin-legacy-quota-anchor-1440.png', false)
  if (evidence.admin.legacyPlanAnchor.hash !== '#quota-plans' || evidence.admin.legacyPlanAnchor.link !== '/admin/plans' || !evidence.admin.legacyPlanAnchor.visible || evidence.admin.legacyPlanAnchor.overflow) throw new Error(`legacy quota-plan URL did not reach the dedicated library link: ${JSON.stringify(evidence.admin.legacyPlanAnchor)}`)
  await adminNavigate(1440, '/admin?status=Pending&email=acceptance-53%40example.test&limit=1')
  evidence.admin.offPageSearch = await evalJS(`({matches:document.body.innerText.includes('acceptance-53@example.test'),count:document.querySelector('.accounts-pane')?.innerText.includes('1 matching Relay Accounts'),plan:document.querySelector('.accounts-pane')?.innerText.includes('Baseline'),allowance:document.querySelector('.accounts-pane')?.innerText.includes('1 GiB / week')})`)
  if (!evidence.admin.offPageSearch.matches || !evidence.admin.offPageSearch.count || !evidence.admin.offPageSearch.plan || !evidence.admin.offPageSearch.allowance) throw new Error(`off-page filtered account acceptance failed: ${JSON.stringify(evidence.admin.offPageSearch)}`)
  const selectedURL = await evalJS(`document.querySelector('.review-link').href`)
  const selectedPath = new URL(selectedURL).pathname + new URL(selectedURL).search
  await adminNavigate(1440, selectedPath)
  evidence.admin.desktopDetail = await evalJS(`({account:document.body.innerText.includes('acceptance-53@example.test'),editor:!!document.querySelector('.account-editor form[action^="/admin/accounts/"]'),split:getComputedStyle(document.querySelector('.accounts-pane')).display!=='none',overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('admin-detail-1440.png', false)
  if (!evidence.admin.desktopDetail.account || !evidence.admin.desktopDetail.editor || !evidence.admin.desktopDetail.split || evidence.admin.desktopDetail.overflow) throw new Error(`desktop admin detail acceptance failed: ${JSON.stringify(evidence.admin.desktopDetail)}`)
  const selectedAccount = new URL(selectedURL).searchParams.get('selected')
  for (const width of [1440, 768, 390]) {
    await adminNavigate(width, selectedPath)
    await wait(2000)
    await evalJS(`(()=>{const form=document.querySelector('#deny-account-access');if(!form)throw new Error('deny account access form missing');form.querySelector('[name=revision]').value='999999';form.querySelector('[name=reason]').value='policy_enforcement';if(!form.checkValidity())throw new Error('stale account review form is invalid');form.requestSubmit()})()`)
    await wait(500); await ready(); await wait(150)
    const mutationResponse = responses.filter(x => x.url.includes(`/admin/accounts/${selectedAccount}`)).at(-1)
    evidence.failures[`conflict${width}`] = await evalJS(`({title:document.querySelector('h1')?.innerText,link:document.querySelector('.message-action a')?.getAttribute('href'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
    await snap(`failure-409-${width}.png`, false)
    await snap(`retry-409-${width}.png`, false)
    if (mutationResponse?.status !== 409 || !evidence.failures[`conflict${width}`].title?.includes('information has changed') || !evidence.failures[`conflict${width}`].link?.includes(`selected=${selectedAccount}`) || !evidence.failures[`conflict${width}`].link?.includes('view=detail') || evidence.failures[`conflict${width}`].overflow) throw new Error(`409 recovery at ${width}px failed: ${JSON.stringify({response:mutationResponse,lastResponses:responses.slice(-8),page:evidence.failures[`conflict${width}`]})}`)
  }
  await adminNavigate(1440, '/admin?email=%25&limit=1')
  evidence.admin.literalPunctuation = await evalJS(`({empty:document.querySelector('.accounts-pane')?.innerText.includes('0 matching Relay Accounts'),recovery:document.querySelector('.accounts-pane')?.innerText.includes('Clear filters')})`)
  if (!evidence.admin.literalPunctuation.empty || !evidence.admin.literalPunctuation.recovery) throw new Error(`literal punctuation/empty recovery acceptance failed: ${JSON.stringify(evidence.admin.literalPunctuation)}`)
  const phoneListPath = '/admin?status=Pending&email=acceptance-53%40example.test&limit=1'
  await adminNavigate(390, phoneListPath)
  evidence.admin.phoneList = await evalJS(`({reviewVisible:(()=>{const r=document.querySelector('.review-link')?.getBoundingClientRect();return !!r&&r.width>0&&r.height>=44})(),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('admin-list-390.png', false)
  if (!evidence.admin.phoneList.reviewVisible || evidence.admin.phoneList.overflow) throw new Error(`phone admin list acceptance failed: ${JSON.stringify(evidence.admin.phoneList)}`)
  const reviewURL = await evalJS(`document.querySelector('.review-link').href`)
  await call('Page.navigate', { url: reviewURL }); await ready(); await wait(250)
  evidence.admin.phoneDetail = await evalJS(`({focused:getComputedStyle(document.querySelector('.accounts-pane')).display==='none',back:!!document.querySelector('.mobile-back'),action:!!document.querySelector('.account-editor form[action^="/admin/accounts/"]'),account:document.body.innerText.includes('acceptance-53@example.test'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('admin-detail-390.png', false)
  if (!evidence.admin.phoneDetail.focused || !evidence.admin.phoneDetail.back || !evidence.admin.phoneDetail.action || !evidence.admin.phoneDetail.account || evidence.admin.phoneDetail.overflow) throw new Error(`phone admin detail acceptance failed: ${JSON.stringify(evidence.admin.phoneDetail)}`)
  const back = await evalJS(`document.querySelector('.mobile-back').getAttribute('href')`)
  if (!back.includes('status=Pending') || !back.includes('acceptance-53')) throw new Error(`phone Back discarded list context: ${back}`)
  await adminNavigate(390, back)
  evidence.admin.phoneBack = await evalJS(`({restored:location.search.includes('status=Pending')&&location.search.includes('acceptance-53'),review:!!document.querySelector('.review-link'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  if (!evidence.admin.phoneBack.restored || !evidence.admin.phoneBack.review || evidence.admin.phoneBack.overflow) throw new Error(`phone Back acceptance failed: ${JSON.stringify(evidence.admin.phoneBack)}`)
  await adminNavigate(768)
  evidence.admin.tablet = await evalJS(`({heading:document.querySelector('h1')?.innerText,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('admin-list-768.png', false)
  if (evidence.admin.tablet.overflow) throw new Error(`tablet admin list overflow: ${JSON.stringify(evidence.admin.tablet)}`)
  await adminNavigate(768, selectedPath)
  evidence.admin.tabletDetail = await evalJS(`({account:document.body.innerText.includes('acceptance-53@example.test'),editor:!!document.querySelector('.account-editor form[action^="/admin/accounts/"]'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
  await snap('admin-detail-768.png', false)
  if (!evidence.admin.tabletDetail.account || !evidence.admin.tabletDetail.editor || evidence.admin.tabletDetail.overflow) throw new Error(`tablet admin detail acceptance failed: ${JSON.stringify(evidence.admin.tabletDetail)}`)

  evidence.plans = { viewports: {} }
  for (const width of [1440, 768, 390, 320]) {
    await adminNavigate(width, '/admin/plans?limit=2')
    const result = await evalJS(`({heading:document.querySelector('h1')?.innerText,rows:document.querySelectorAll('.quota-plan').length,create:!!document.querySelector('form.plan-form[action="/admin/plans"]'),archiveReview:!!document.querySelector('.archive-plan-form input[name="confirm"][required]'),next:!!document.querySelector('.plan-library-page a[href*="after="]'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,scroll:document.documentElement.scrollWidth,client:document.documentElement.clientWidth,numeric:getComputedStyle(document.querySelector('.quota-plan-value strong')||document.body).fontVariantNumeric})`)
    evidence.plans.viewports[width] = result
    await snap(`plans-${width}.png`, false)
    if (result.heading !== 'Quota plans' || !result.rows || !result.create || !result.archiveReview || result.overflow || result.numeric !== 'tabular-nums') throw new Error(`quota plan library ${width}px acceptance failed: ${JSON.stringify(result)}`)
  }
  await adminNavigate(390, '/admin/plans')
  await call('Emulation.setScriptExecutionDisabled', { value: true })
  const planDOM = await call('DOM.getDocument', { depth: 0 })
  const planHTML = (await call('DOM.getOuterHTML', { nodeId: planDOM.root.nodeId })).outerHTML
  evidence.plans.noScript = { list: planHTML.includes('class="quota-plan-list"'), create: planHTML.includes('name="operation_proof"'), archiveReview: planHTML.includes('name="confirm" value="yes" required'), stylesheet: planHTML.includes('/assets/portal.css') }
  await snap('plans-no-script-390.png', false)
  if (!evidence.plans.noScript.list || !evidence.plans.noScript.create || !evidence.plans.noScript.archiveReview || !evidence.plans.noScript.stylesheet) throw new Error(`no-script quota plan forms failed: ${JSON.stringify(evidence.plans.noScript)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })

  await adminNavigate(1440, '/admin/plans')
  // Let the account's per-subject request bucket refill after the many admin
  // navigation checks above so this form test observes creation behavior.
  await wait(2200)
  const createPlanName = `Browser zero ${Date.now()}`
  evidence.plans.createForm = await evalJS(`(()=>{const f=document.querySelector('form.plan-form[action="/admin/plans"]');f.elements.name.value=${JSON.stringify(createPlanName)};f.elements.weekly_bytes.value='0';f.elements.weekly_bytes_unit.value='B';f.elements.sessions.value='0';f.elements.reason.value='routine_administration';return {valid:f.checkValidity(),name:f.elements.name.value,bytes:f.elements.weekly_bytes.value,unit:f.elements.weekly_bytes_unit.value,sessions:f.elements.sessions.value,reason:f.elements.reason.value,operation:f.elements.operation_id.value,proof:f.elements.operation_proof.value.length}})()`)
  if (!evidence.plans.createForm.valid) throw new Error(`quota plan form did not pass native validation: ${JSON.stringify(evidence.plans.createForm)}`)
  await evalJS(`document.querySelector('form.plan-form[action="/admin/plans"]').requestSubmit()`)
  for (let i = 0; i < 50; i++) {
    if (await evalJS(`document.body.innerText.includes(${JSON.stringify(createPlanName)})`)) break
    await wait(100)
  }
  evidence.plans.zeroCreation = await evalJS(`(()=>{const card=document.querySelector('.plan-created');return {url:location.pathname,query:location.search,visible:card?.innerText.includes(${JSON.stringify(createPlanName)}),zeroBytes:card?.innerText.includes('0 bytes'),zeroSessions:card?.innerText.includes('Relay Sessions')&&card?.innerText.includes('0'),heading:card?.querySelector('h2')?.innerText}})()`)
  await snap('plans-created-zero-1440.png', false)
  if (evidence.plans.zeroCreation.url !== '/admin/plans' || !evidence.plans.zeroCreation.query.includes('created=') || !evidence.plans.zeroCreation.visible || !evidence.plans.zeroCreation.zeroBytes || !evidence.plans.zeroCreation.zeroSessions || evidence.plans.zeroCreation.heading !== 'Plan created') throw new Error(`zero plan creation/reachability failed: ${JSON.stringify({result:evidence.plans.zeroCreation,form:evidence.plans.createForm,responses:responses.filter(x=>x.url.includes('/admin/plans')).slice(-8),body:(await evalJS('document.body.innerText')).slice(0,700)})}`)
  const archiveForm = await evalJS(`(()=>{const card=document.querySelector('.plan-created');const f=card?.querySelector('.archive-plan-form');return f?{action:f.action,revision:f.elements.revision.value,id:f.action.split('/').at(-2)}:null})()`)
  if (!archiveForm) throw new Error('new quota plan has no archive review form')
  await evalJS(`(()=>{const card=document.querySelector('.plan-created');card.querySelector('[name=confirm]').checked=true;card.querySelector('.archive-plan-form').requestSubmit()})()`)
  for (let i = 0; i < 50; i++) {
    if (await evalJS(`document.querySelector('.plan-created h2')?.innerText==='Plan archived'`)) break
    await wait(100)
  }
  evidence.plans.archive = await evalJS(`(()=>{const card=document.querySelector('.plan-created');return {url:location.pathname,query:location.search,archived:card?.innerText.includes('Archived'),noNewAction:!card?.querySelector('.archive-plan-form'),state:card?.querySelector('.badge')?.innerText,heading:card?.querySelector('h2')?.innerText}})()`)
  await snap('plans-archived-1440.png', false)
  if (evidence.plans.archive.url !== '/admin/plans' || !evidence.plans.archive.query.includes('created=') || !evidence.plans.archive.archived || !evidence.plans.archive.noNewAction || evidence.plans.archive.heading !== 'Plan archived') throw new Error(`reviewed plan archive failed: ${JSON.stringify(evidence.plans.archive)}`)

  evidence.adminMutations = {}
  const pendingAccountID = '66000000-0000-4000-8000-000000000001'
  const activeAccountID = '66000000-0000-4000-8000-000000000002'
  const browserPlan02 = '67000000-0000-4000-8000-000000000002'
  const browserPlan03 = '67000000-0000-4000-8000-000000000003'
  const pendingReview = `/admin?selected=${pendingAccountID}&view=detail&status=Pending&email=acceptance-01%40example.test&limit=50`
  await adminNavigate(1440, pendingReview)
  evidence.adminMutations.pending = await evalJS(`(()=>{const f=document.querySelector('#approve-account');const s=f?.querySelector('#approval-plan');const p=f?.querySelector('#approval-plan-preview');return {status:document.querySelector('.account-editor .badge')?.innerText,form:!!f,onlyApproval:!document.querySelector('#assign-plan')&&!document.querySelector('#quota-override'),plan:s?.value,planCount:s?.options.length,preview:p?.innerText,summary:s?.selectedOptions[0]?.dataset.summary,previewMatches:p?.innerText===s?.selectedOptions[0]?.dataset.summary,action:f?.querySelector('[name="action"]')?.value,planField:f?.querySelector('[name="plan_id"]')?.value,reason:f?.querySelector('[name="reason"]')?.value,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth}})()`)
  if (evidence.adminMutations.pending.status !== 'Pending' || !evidence.adminMutations.pending.form || !evidence.adminMutations.pending.onlyApproval || !evidence.adminMutations.pending.planCount || !evidence.adminMutations.pending.previewMatches || evidence.adminMutations.pending.action !== 'approve' || evidence.adminMutations.pending.planField !== '67000000-0000-4000-8000-000000000001' || evidence.adminMutations.pending.overflow) throw new Error(`pending account approval form/preview failed: ${JSON.stringify(evidence.adminMutations.pending)}`)
  await evalJS(`(()=>{const s=document.querySelector('#approval-plan');s.value=${JSON.stringify(browserPlan02)};s.dispatchEvent(new Event('change',{bubbles:true}))})()`)
  evidence.adminMutations.pending.changedPreview = await evalJS(`(()=>{const s=document.querySelector('#approval-plan');return {plan:s.value,expected:s.selectedOptions[0]?.dataset.summary,preview:document.querySelector('#approval-plan-preview')?.innerText}})()`)
  if (evidence.adminMutations.pending.changedPreview.plan !== browserPlan02 || evidence.adminMutations.pending.changedPreview.expected !== evidence.adminMutations.pending.changedPreview.preview) throw new Error(`approval plan preview did not follow selected plan data: ${JSON.stringify(evidence.adminMutations.pending.changedPreview)}`)
  await snap('admin-approval-pending-1440.png', false)
  await call('Emulation.setScriptExecutionDisabled', { value: true })
  const pendingDOM = await call('DOM.getDocument', { depth: 0 })
  const approveButton = await call('DOM.querySelector', { nodeId: pendingDOM.root.nodeId, selector: '#approve-account button' })
  if (!approveButton.nodeId) throw new Error('no-script approval action missing')
  responses.length = 0
  requests.length = 0
  await clickNode(approveButton.nodeId); await wait(500); await ready(); await wait(200)
  evidence.adminMutations.pending.submission = { response:requests.filter(x=>x.redirectURL?.includes(`/admin/accounts/${pendingAccountID}`)&&x.redirectStatus===303).at(-1)?.redirectStatus, status:await evalJS(`document.querySelector('.account-editor .badge')?.innerText`), selectedPlan:await evalJS(`document.querySelector('.account-editor')?.innerText.includes('Browser plan 02')`), finding:await evalJS(`document.querySelector('.admin-finding')?.innerText`) }
  await snap('admin-approval-result-no-script-1440.png', false)
  if (evidence.adminMutations.pending.submission.response !== 303 || evidence.adminMutations.pending.submission.status !== 'Active' || !evidence.adminMutations.pending.submission.selectedPlan) throw new Error(`no-script approval did not persist its selected plan: ${JSON.stringify(evidence.adminMutations.pending.submission)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })

  await adminNavigate(390, `/admin?selected=${pendingAccountID}&view=detail&status=Active&email=acceptance-01%40example.test&limit=50`)
  evidence.adminMutations.active = await evalJS(`(()=>({assignment:!!document.querySelector('#assign-plan'),override:!!document.querySelector('#quota-override'),separate:!document.querySelector('#assign-plan #weekly-override')&&!document.querySelector('#quota-override #assignment-plan'),assignmentPreview:document.querySelector('#assignment-plan-preview')?.innerText,planSummary:document.querySelector('#assignment-plan')?.selectedOptions[0]?.dataset.summary,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth}))()`)
  if (!evidence.adminMutations.active.assignment || !evidence.adminMutations.active.override || !evidence.adminMutations.active.separate || evidence.adminMutations.active.assignmentPreview !== evidence.adminMutations.active.planSummary || evidence.adminMutations.active.overflow) throw new Error(`active plan assignment and override forms are not separate/previewed: ${JSON.stringify(evidence.adminMutations.active)}`)
  await evalJS(`(()=>{const s=document.querySelector('#assignment-plan');s.value=${JSON.stringify(browserPlan03)};s.dispatchEvent(new Event('change',{bubbles:true}))})()`)
  evidence.adminMutations.active.changedPreview = await evalJS(`(()=>({plan:document.querySelector('#assignment-plan').value,summary:document.querySelector('#assignment-plan').selectedOptions[0]?.dataset.summary,preview:document.querySelector('#assignment-plan-preview')?.innerText}))()`)
  if (evidence.adminMutations.active.changedPreview.plan !== browserPlan03 || evidence.adminMutations.active.changedPreview.summary !== evidence.adminMutations.active.changedPreview.preview) throw new Error(`replacement plan preview did not follow actual plan: ${JSON.stringify(evidence.adminMutations.active.changedPreview)}`)
  await snap('admin-assignment-override-390.png', false)

  await call('Emulation.setScriptExecutionDisabled', { value: true })
  const assignmentDOM = await call('DOM.getDocument', { depth: 0 })
  const assignButton = await call('DOM.querySelector', { nodeId: assignmentDOM.root.nodeId, selector: '#assign-plan button' })
  responses.length = 0
  requests.length = 0
  await clickNode(assignButton.nodeId); await wait(500); await ready(); await wait(150)
  evidence.adminMutations.active.assignmentSubmission = { response:requests.filter(x=>x.redirectURL?.includes(`/admin/accounts/${pendingAccountID}`)&&x.redirectStatus===303).at(-1)?.redirectStatus, assigned:await evalJS(`document.querySelector('.account-editor')?.innerText.includes('Browser plan 03')`) }
  if (evidence.adminMutations.active.assignmentSubmission.response !== 303 || !evidence.adminMutations.active.assignmentSubmission.assigned) throw new Error(`replacement plan form did not save the selected assignment: ${JSON.stringify(evidence.adminMutations.active.assignmentSubmission)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })

  await adminNavigate(390, `/admin?selected=${pendingAccountID}&view=detail&status=Active&email=acceptance-01%40example.test&limit=50`)
  const quotaPreview = await evalJS(`(()=>({weekly:document.querySelector('#quota-override [name=weekly_bytes_override]')?.value,weeklyUnit:document.querySelector('#quota-override [name=weekly_bytes_override_unit]')?.value,sessions:document.querySelector('#quota-override [name=sessions_override]')?.value,usageWarning:document.querySelector('#quota-override')?.innerText.includes('do not reset Recorded Relay Usage'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth}))()`)
  if (quotaPreview.weekly !== '' || quotaPreview.sessions !== '' || !quotaPreview.usageWarning || quotaPreview.overflow) throw new Error(`quota override form defaults or explanation failed: ${JSON.stringify(quotaPreview)}`)
  await evalJS(`(()=>{const f=document.querySelector('#quota-override');f.querySelector('[name="weekly_bytes_override"]').value='12345';f.querySelector('[name="weekly_bytes_override_unit"]').value='B';f.querySelector('[name="sessions_override"]').value='0';f.querySelector('[name="reason"]').value='policy_enforcement';f.querySelector('[name="weekly_bytes_override"]').focus()})()`)
  // The controls remain keyboard reachable in their documented order; Enter
  // activates the named save button after the safe quantity fields.
  for (let i=0;i<4;i++){await call('Input.dispatchKeyEvent',{type:'rawKeyDown',key:'Tab',code:'Tab',windowsVirtualKeyCode:9});await call('Input.dispatchKeyEvent',{type:'keyUp',key:'Tab',code:'Tab',windowsVirtualKeyCode:9})}
  evidence.adminMutations.overrideKeyboard = await evalJS(`({focused:document.activeElement?.tagName,button:document.activeElement?.innerText,form:document.activeElement?.form?.id})`)
  if (evidence.adminMutations.overrideKeyboard.form !== 'quota-override' || !evidence.adminMutations.overrideKeyboard.button?.includes('Save quota overrides')) throw new Error(`keyboard tab order did not reach the quota action: ${JSON.stringify(evidence.adminMutations.overrideKeyboard)}`)
  responses.length = 0
  requests.length = 0
  await call('Input.dispatchKeyEvent',{type:'keyDown',key:'Enter',code:'Enter',windowsVirtualKeyCode:13,nativeVirtualKeyCode:13});await call('Input.dispatchKeyEvent',{type:'char',key:'Enter',code:'Enter',text:'\r',unmodifiedText:'\r',windowsVirtualKeyCode:13,nativeVirtualKeyCode:13});await call('Input.dispatchKeyEvent',{type:'keyUp',key:'Enter',code:'Enter',windowsVirtualKeyCode:13,nativeVirtualKeyCode:13});await wait(500);await ready();await wait(150)
  evidence.adminMutations.overrideSubmission = await evalJS(`(()=>{const summary=document.querySelector('.account-editor')?.innerText||'';return {weekly:document.querySelector('#quota-override [name=weekly_bytes_override]')?.value,unit:document.querySelector('#quota-override [name=weekly_bytes_override_unit]')?.value,sessions:document.querySelector('#quota-override [name=sessions_override]')?.value,accountSummary:summary,effective:summary.includes('Effective weekly allowance\\n12.06 KiB')&&summary.includes('Effective Relay Session limit\\n0')}})()`); evidence.adminMutations.overrideSubmission.requests=requests.filter(x=>x.url?.includes(`/admin/accounts/${pendingAccountID}`)||x.redirectURL?.includes(`/admin/accounts/${pendingAccountID}`)); evidence.adminMutations.overrideSubmission.responses=responses.filter(x=>x.url.includes(`/admin/accounts/${pendingAccountID}`)); evidence.adminMutations.overrideSubmission.response=evidence.adminMutations.overrideSubmission.requests.filter(x=>x.redirectURL?.includes(`/admin/accounts/${pendingAccountID}`)&&x.redirectStatus===303).at(-1)?.redirectStatus
  await snap('admin-quota-exact-zero-390.png', false)
  if (evidence.adminMutations.overrideSubmission.response !== 303 || evidence.adminMutations.overrideSubmission.weekly !== '12345' || evidence.adminMutations.overrideSubmission.unit !== 'B' || evidence.adminMutations.overrideSubmission.sessions !== '0' || !evidence.adminMutations.overrideSubmission.effective) throw new Error(`exact byte and explicit-zero override did not persist via keyboard: ${JSON.stringify(evidence.adminMutations.overrideSubmission)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: true })
  await evalJS(`(()=>{const f=document.querySelector('#quota-override');f.querySelector('[name="weekly_bytes_override"]').value='';f.querySelector('[name="sessions_override"]').value='';f.querySelector('[name="reason"]').value='support_correction'})()`)
  const clearDOM = await call('DOM.getDocument', { depth: 0 })
  const clearButton = await call('DOM.querySelector', { nodeId: clearDOM.root.nodeId, selector: '#quota-override button' })
  responses.length = 0
  requests.length = 0
  await clickNode(clearButton.nodeId); await wait(500); await ready(); await wait(150)
  evidence.adminMutations.overrideClear = { response:requests.filter(x=>x.redirectURL?.includes(`/admin/accounts/${pendingAccountID}`)&&x.redirectStatus===303).at(-1)?.redirectStatus, weekly:await evalJS(`document.querySelector('#quota-override [name=weekly_bytes_override]')?.value`), sessions:await evalJS(`document.querySelector('#quota-override [name=sessions_override]')?.value`), effective:await evalJS(`(()=>{const facts=[...document.querySelectorAll('.review-facts > div')].map(node=>node.innerText);return facts.some(text=>text.includes('Effective weekly allowance')&&text.includes('1 MiB'))&&facts.some(text=>text.includes('Effective Relay Session limit')&&text.includes('3'))})()`) }
  await snap('admin-quota-cleared-no-script-390.png', false)
  if (evidence.adminMutations.overrideClear.response !== 303 || evidence.adminMutations.overrideClear.weekly !== '' || evidence.adminMutations.overrideClear.sessions !== '' || !evidence.adminMutations.overrideClear.effective) throw new Error(`blank override fields did not clear back to assigned plan: ${JSON.stringify(evidence.adminMutations.overrideClear)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })

  await adminNavigate(390, `/admin?selected=${pendingAccountID}&view=detail&accounts_after=66000000-0000-4000-8000-000000000003&status=Active&email=acceptance-01%40example.test&limit=50`)
  await evalJS(`(()=>{const f=document.querySelector('#assign-plan');f.querySelector('[name="revision"]').value='999999';f.requestSubmit()})()`)
  await wait(450); await ready(); await wait(100)
  evidence.adminMutations.staleContext = { response:responses.filter(x=>x.url.includes(`/admin/accounts/${pendingAccountID}`)&&x.status===409).at(-1)?.status, link:await evalJS(`document.querySelector('.message-action a')?.getAttribute('href')`), heading:await evalJS(`document.querySelector('h1')?.innerText`), overflow:await evalJS(`document.documentElement.scrollWidth>document.documentElement.clientWidth`) }
  await snap('admin-stale-quota-context-390.png', false)
  if (evidence.adminMutations.staleContext.response !== 409 || !evidence.adminMutations.staleContext.link?.includes('accounts_after=66000000-0000-4000-8000-000000000003') || !evidence.adminMutations.staleContext.link?.includes('status=Active') || !evidence.adminMutations.staleContext.link?.includes('email=acceptance-01') || evidence.adminMutations.staleContext.overflow) throw new Error(`stale quota edit did not return with safe account list context: ${JSON.stringify(evidence.adminMutations.staleContext)}`)

  evidence.adminMutations.accessActions = { states: {}, transitions: {} }
  const inspectAccessState = async (width, accountID, expectedStatus, allowed, absent, held = false) => {
    await adminNavigate(width, `/admin?selected=${accountID}&view=detail&status=${expectedStatus}&email=${encodeURIComponent(accountID === '66000000-0000-4000-8000-000000000004' ? 'acceptance-04@example.test' : accountID === '66000000-0000-4000-8000-000000000005' ? 'acceptance-05@example.test' : 'acceptance-02@example.test')}&limit=50`)
    const state = await evalJS(`(()=>{const e=document.querySelector('.account-editor');return {status:e?.querySelector('.badge')?.innerText,actions:[...e.querySelectorAll('.access-action')].map(f=>({id:f.id,action:f.querySelector('[name="action"]')?.value,title:f.querySelector('legend')?.innerText,effect:f.querySelector('fieldset > p')?.innerText,reason:!!f.querySelector('[name="reason"]'),button:f.querySelector('button')?.innerText,method:f.method})),generic:!!e?.querySelector('#account-access-action'),hold:e?.innerText.includes('Recovery Review Hold'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth}})()`)
    evidence.adminMutations.accessActions.states[`${expectedStatus}-${width}-${held ? 'held' : 'ordinary'}`] = state
    await snap(`admin-access-${expectedStatus.toLowerCase()}-${width}${held ? '-held' : ''}.png`, false)
    if (state.status !== expectedStatus || state.generic || state.overflow || state.hold !== held || allowed.some(id=>!state.actions.some(action=>action.id===id&&action.reason&&action.method==='post')) || absent.some(id=>state.actions.some(action=>action.id===id))) throw new Error(`state-specific ${expectedStatus} access actions failed: ${JSON.stringify(state)}`)
    for (const form of state.actions.filter(action=>['deny-account-access','suspend-account-access','revoke-account-access'].includes(action.id))) {
      if (!form.effect.includes(state.actions.find(action=>action.id===form.id)?.button?.replace(/^Confirm (suspension|denial|credential revocation) of /,'').replace(/^Confirm credential revocation for /,'')) || !form.reason) throw new Error(`access-closing form did not name its target/effect/reason: ${JSON.stringify(form)}`)
    }
    if (held && !state.actions.find(action=>action.id==='review-recovery-hold')?.effect.includes('canonical status and quota assignments stay unchanged')) throw new Error(`Recovery Review Hold is not described as independent of status/quotas: ${JSON.stringify(state)}`)
    if (!held && state.actions.some(action=>action.id==='review-recovery-hold')) throw new Error(`ordinary ${expectedStatus} account unexpectedly offered Recovery Review Hold: ${JSON.stringify(state)}`)
  }
  await inspectAccessState(1440, '66000000-0000-4000-8000-000000000005', 'Pending', ['deny-account-access','revoke-account-access'], ['suspend-account-access','reactivate-account-access','return-account-review'])
  await inspectAccessState(1440, activeAccountID, 'Active', ['suspend-account-access','deny-account-access','revoke-account-access'], ['reactivate-account-access','return-account-review'])
  await inspectAccessState(768, activeAccountID, 'Active', ['suspend-account-access','deny-account-access','revoke-account-access'], ['reactivate-account-access','return-account-review'])
  await inspectAccessState(390, '66000000-0000-4000-8000-000000000003', 'Suspended', ['reactivate-account-access','deny-account-access','revoke-account-access'], ['suspend-account-access','return-account-review'])
  await inspectAccessState(390, '66000000-0000-4000-8000-000000000004', 'Denied', ['return-account-review','revoke-account-access','review-recovery-hold'], ['suspend-account-access','reactivate-account-access','deny-account-access'], true)

  const applyAccessAction = async (actionID, accountID, action, expectedStatus, label) => {
    const actionForm = await call('DOM.querySelector', { nodeId:(await call('DOM.getDocument',{depth:0})).root.nodeId, selector:`#${actionID}` })
    if (!actionForm.nodeId) throw new Error(`${label} action form missing: ${actionID}`)
    const button = await call('DOM.querySelector', { nodeId:(await call('DOM.getDocument',{depth:0})).root.nodeId, selector:`#${actionID} button` })
    requests.length = 0; responses.length = 0
    await clickNode(button.nodeId); await wait(450); await ready(); await wait(120)
    const result = await evalJS(`(()=>({status:document.querySelector('.account-editor .badge')?.innerText,forms:[...document.querySelectorAll('.access-action')].map(f=>f.id),email:document.querySelector('.account-editor .account-email')?.innerText,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth}))()`)
    const redirect = requests.filter(x=>x.redirectURL?.includes(`/admin/accounts/${accountID}`)&&x.redirectStatus===303).at(-1)
    evidence.adminMutations.accessActions.transitions[label] = {response:redirect?.redirectStatus,status:result.status,forms:result.forms,email:result.email,overflow:result.overflow}
    await snap(`admin-access-${label}.png`, false)
    if (redirect?.redirectStatus!==303 || result.status!==expectedStatus || result.overflow) throw new Error(`${label} access transition failed: ${JSON.stringify(evidence.adminMutations.accessActions.transitions[label])}`)
    return result
  }

  await call('Emulation.setScriptExecutionDisabled', { value: true })
  await adminNavigate(390, `/admin?selected=${pendingAccountID}&view=detail&status=Active&email=acceptance-01%40example.test&limit=50`)
  const transitionStart = await evalJS(`(()=>({status:document.querySelector('.account-editor .badge')?.innerText,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth}))()`)
  if (transitionStart.status !== 'Active' || transitionStart.overflow) throw new Error(`access transition fixture was not an active account at phone width: ${JSON.stringify(transitionStart)}`)
  await applyAccessAction('suspend-account-access', pendingAccountID, 'suspend', 'Suspended', 'suspend')
  if (!evidence.adminMutations.accessActions.transitions.suspend.forms.includes('reactivate-account-access') || !evidence.adminMutations.accessActions.transitions.suspend.forms.includes('deny-account-access')) throw new Error(`suspension did not expose the appropriate follow-up controls: ${JSON.stringify(evidence.adminMutations.accessActions.transitions.suspend)}`)
  await applyAccessAction('reactivate-account-access', pendingAccountID, 'reactivate', 'Active', 'reactivate')
  await applyAccessAction('deny-account-access', pendingAccountID, 'deny', 'Denied', 'deny')
  if (!evidence.adminMutations.accessActions.transitions.deny.forms.includes('return-account-review')) throw new Error(`denial did not offer return-to-review: ${JSON.stringify(evidence.adminMutations.accessActions.transitions.deny)}`)
  await applyAccessAction('return-account-review', pendingAccountID, 'review', 'Pending', 'return-to-review')
  await applyAccessAction('revoke-account-access', pendingAccountID, 'revoke', 'Pending', 'revoke')

  await adminNavigate(390, '/admin?selected=66000000-0000-4000-8000-000000000004&view=detail&status=Denied&email=acceptance-04%40example.test&limit=50')
  const holdButton = await call('DOM.querySelector', { nodeId:(await call('DOM.getDocument',{depth:0})).root.nodeId, selector:'#review-recovery-hold button' })
  if (!holdButton.nodeId) throw new Error('explicit Recovery Review Hold action missing without scripts')
  requests.length = 0; responses.length = 0
  await clickNode(holdButton.nodeId); await wait(450); await ready(); await wait(120)
  evidence.adminMutations.accessActions.transitions.clearRecoveryHold = { response:requests.filter(x=>x.redirectURL?.includes('/admin/accounts/66000000-0000-4000-8000-000000000004')&&x.redirectStatus===303).at(-1)?.redirectStatus, status:await evalJS(`document.querySelector('.account-editor .badge')?.innerText`), stillHeld:await evalJS(`document.querySelector('.account-editor')?.innerText.includes('Recovery Review Hold')`), reviewForm:!!(await call('DOM.querySelector',{nodeId:(await call('DOM.getDocument',{depth:0})).root.nodeId,selector:'#return-account-review'})).nodeId }
  await snap('admin-recovery-hold-cleared-no-script-390.png', false)
  if (evidence.adminMutations.accessActions.transitions.clearRecoveryHold.response!==303 || evidence.adminMutations.accessActions.transitions.clearRecoveryHold.status!=='Denied' || evidence.adminMutations.accessActions.transitions.clearRecoveryHold.stillHeld || !evidence.adminMutations.accessActions.transitions.clearRecoveryHold.reviewForm) throw new Error(`Recovery Review Hold did not clear independently of canonical status: ${JSON.stringify(evidence.adminMutations.accessActions.transitions.clearRecoveryHold)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })

  // Exercise production HTTP failure handlers and their native GET recovery links.
  const absentSelection = '66000000-0000-4000-8000-000000000099'
  evidence.failures.missing = {}
  for (const width of [1440, 768, 390]) {
    await adminNavigate(width, `/admin?selected=${absentSelection}&view=detail`)
    const result = await evalJS(`({width:innerWidth,height:innerHeight,title:document.querySelector('h1')?.innerText,link:document.querySelector('.message-action a')?.getAttribute('href'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
    evidence.failures.missing[width] = result
    await snap(`failure-404-${width}.png`, false)
    if (!result.title?.includes('could not be found') || result.link !== '/' || result.overflow) throw new Error(`404 recovery at ${width}px failed: ${JSON.stringify(result)}`)
  }
  evidence.failures.missingDesktop = evidence.failures.missing[1440]
  evidence.failures.missingPhone = evidence.failures.missing[390]
  await call('Emulation.setScriptExecutionDisabled', { value: true })
  await call('Page.navigate', { url: `${base}/admin?selected=${absentSelection}&view=detail` }); await wait(400)
  const noScriptErrorDOM = await call('DOM.getDocument', { depth: 0 })
  const noScriptErrorHTML = await call('DOM.getOuterHTML', { nodeId: noScriptErrorDOM.root.nodeId })
  evidence.failures.noScriptMissing = { recoveryLink: noScriptErrorHTML.outerHTML.includes('href="/"'), styled: noScriptErrorHTML.outerHTML.includes('/assets/portal.css'), noForm: !noScriptErrorHTML.outerHTML.includes('<form') }
  await snap('failure-404-no-script-390.png', false)
  if (!evidence.failures.noScriptMissing.recoveryLink || !evidence.failures.noScriptMissing.styled || !evidence.failures.noScriptMissing.noForm) throw new Error(`no-script 404 recovery failed: ${JSON.stringify(evidence.failures.noScriptMissing)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })

  const allowlistPath = process.env.ADMIN_ALLOWLIST_FILE
  if (!allowlistPath) throw new Error('ADMIN_ALLOWLIST_FILE is required for forbidden-page acceptance')
  await writeFile(allowlistPath, JSON.stringify({ revision: 2, emails: [] }) + '\n')
  evidence.failures.forbidden = {}
  for (const width of [1440, 768, 390]) {
    await adminNavigate(width)
    const result = await evalJS(`({width:innerWidth,height:innerHeight,title:document.querySelector('h1')?.innerText,link:document.querySelector('.message-action a')?.getAttribute('href'),signIn:[...document.querySelectorAll('.message-action a')].some(a=>a.href.includes('/auth/login')),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
    evidence.failures.forbidden[width] = result
    await snap(`restricted-403-${width}.png`, false)
    if (!result.title?.includes('not available') || result.link !== '/' || result.signIn || result.overflow) throw new Error(`403 recovery at ${width}px failed: ${JSON.stringify(result)}`)
  }
  evidence.failures.forbiddenPhone = evidence.failures.forbidden[390]

  await call('Network.clearBrowserCookies')
  evidence.failures.expiredSignIn = {}
  for (const width of [1440, 768, 390]) {
    await adminNavigate(width)
    const result = await evalJS(`({width:innerWidth,height:innerHeight,title:document.querySelector('h1')?.innerText,link:document.querySelector('.message-action a')?.getAttribute('href'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
    evidence.failures.expiredSignIn[width] = result
    await snap(`failure-401-${width}.png`, false)
    if (result.title !== 'Sign-in expired' || result.link !== '/auth/login' || result.overflow) throw new Error(`401 recovery at ${width}px failed: ${JSON.stringify(result)}`)
  }

  evidence.security = { viewports: {} }
  await call('Page.navigate', { url: `${base}/auth/login` }); await ready(); await wait(300)
  if (!(await evalJS(`document.body.innerText.includes('Your account')`))) throw new Error('fixture did not restore owner session for security acceptance')
  await adminNavigate(1440, '/account/security')
  await evalJS(`document.querySelector('form[action="/auth/logout"] button').click()`)
  await wait(300)
  evidence.security.portalLogout = await evalJS(`({url:location.pathname+location.search,destination:document.body.innerText.includes('You have signed out of this portal.'),grantsRemain:document.body.innerText.includes('Clipp Login Grants and relay access remain active.')})`)
  if (!evidence.security.portalLogout.destination || !evidence.security.portalLogout.grantsRemain) throw new Error(`portal-only logout destination failed: ${JSON.stringify(evidence.security.portalLogout)}`)
  await call('Page.navigate', { url: `${base}/auth/login` }); await ready(); await wait(300)
  if (!(await evalJS(`document.body.innerText.includes('Your account')`))) throw new Error('fixture did not restore owner session after portal-only logout')
  for (const width of [1440, 768, 390, 320]) {
    await adminNavigate(width, '/account/security')
    await snap(`security-${width}.png`, false)
    await evalJS(`document.querySelector('.security-review summary').click()`)
    const result = await evalJS(`({heading:document.querySelector('h1')?.innerText,account:document.body.innerText.includes('workspace-a@example.test'),grantCount:document.body.innerText.includes('Active Login Grants:'),portalScope:document.body.innerText.includes('Only this browser')&&document.body.innerText.includes('Your Clipp Login Grants, Relay Access Tokens and live Relay Sessions remain active'),revokeScope:document.body.innerText.includes('All access for this Relay Account')&&document.body.innerText.includes('end all Portal Sessions')&&document.body.innerText.includes('Your account status and quota assignments do not change'),review:document.body.innerText.includes('revoke every Login Grant'),checkbox:!!document.querySelector('input[name="confirm"][required]'),deletion:!!document.querySelector('form[action="/auth/delete"]'),deletionScope:document.body.innerText.includes('Permanent account removal')&&document.querySelector('.danger-details.security-action')?.textContent.includes('Relay Account Deletion is irreversible')&&document.querySelector('.danger-details.security-action')?.textContent.includes('Retained Quota Usage for the current week may be imported'),deleteConfirmation:!!document.querySelector('input[name="confirm_email"][required][pattern]'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth,scrollWidth:document.documentElement.scrollWidth,clientWidth:document.documentElement.clientWidth})`)
    evidence.security.viewports[width] = result
    if (!result.account || !result.grantCount || !result.portalScope || !result.revokeScope || !result.review || !result.checkbox || !result.deletion || !result.deletionScope || !result.deleteConfirmation || result.overflow) throw new Error(`security view ${width}px acceptance failed: ${JSON.stringify(result)}`)
  }
  await adminNavigate(1440, '/account/security')
  await call('Emulation.setScriptExecutionDisabled', { value: true })
  let securityDOM = await call('DOM.getDocument', { depth: 0 })
  const revokeSummary = await call('DOM.querySelector', { nodeId: securityDOM.root.nodeId, selector: '.security-review summary' })
  await clickNode(revokeSummary.nodeId)
  securityDOM = await call('DOM.getDocument', { depth: 0 })
  const unchecked = await call('DOM.querySelector', { nodeId: securityDOM.root.nodeId, selector: 'input[name="confirm"]' })
  const revokeButton = await call('DOM.querySelector', { nodeId: securityDOM.root.nodeId, selector: 'form[action="/auth/revoke"] button' })
  const uncheckedState = await call('DOM.describeNode', { nodeId: unchecked.nodeId })
  const noScriptHTML = (await call('DOM.getOuterHTML', { nodeId: securityDOM.root.nodeId })).outerHTML
  evidence.security.noScript = { reviewVisible: noScriptHTML.includes('I understand this signs workspace-a@example.test out everywhere.'), required: uncheckedState.node.attributes.includes('required'), deletionRecovery: noScriptHTML.includes('Signing in does not resubmit the deletion.') && noScriptHTML.includes('Retained Quota Usage for the current week may be imported') }
  await call('DOM.focus', { nodeId: revokeButton.nodeId })
  await call('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 })
  await call('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 })
  await wait(150)
  evidence.security.noScript.uncheckedPrevented = (await evalJS(`location.pathname === '/account/security'`))
  if (!evidence.security.noScript.reviewVisible || !evidence.security.noScript.required || !evidence.security.noScript.deletionRecovery || !evidence.security.noScript.uncheckedPrevented) throw new Error(`JavaScript-disabled security review failed: ${JSON.stringify(evidence.security.noScript)}`)
  securityDOM = await call('DOM.getDocument', { depth: 0 })
  const checked = await call('DOM.querySelector', { nodeId: securityDOM.root.nodeId, selector: 'input[name="confirm"]' })
  await clickNode(checked.nodeId)
  evidence.security.noScript.checkedAfterUserClick = await evalJS(`document.querySelector('input[name="confirm"]').checked`)
  const checkedButton = await call('DOM.querySelector', { nodeId: securityDOM.root.nodeId, selector: 'form[action="/auth/revoke"] button' })
  await clickNode(checkedButton.nodeId)
  await wait(350)
  evidence.security.noScript.revocationDestination = await evalJS(`({url:location.pathname+location.search,body:document.body.innerText.slice(0,500),accurate:document.body.innerText.includes('Physical connection closure may still be completing.'),completed:document.body.innerText.includes('Account-wide revocation completed.')})`)
  evidence.security.noScript.revocationDestination.response = responses.filter(r => r.url.includes('/auth/revoke')).slice(-1)[0]
  await snap('security-revocation-result-no-script.png', false)
  await call('Emulation.setScriptExecutionDisabled', { value: false })
  if (!evidence.security.noScript.revocationDestination.completed || !evidence.security.noScript.revocationDestination.accurate) throw new Error(`JavaScript-disabled revocation result failed: ${JSON.stringify(evidence.security.noScript.revocationDestination)}`)

  // Reauthenticate after account-wide revocation, then use native form
  // validation and submission for the deletion path with scripts disabled.
  await call('Page.navigate', { url: `${base}/auth/login` }); await ready(); await wait(450)
  if (!(await evalJS(`document.body.innerText.includes('Your account')`))) throw new Error('fixture did not restore owner after account-wide revocation')
  await adminNavigate(390, '/account/security')
  const deleteSummaryDOM = await call('DOM.getDocument', { depth: 0 })
  const deleteSummary = await call('DOM.querySelector', { nodeId: deleteSummaryDOM.root.nodeId, selector: '.danger-details summary' })
  await clickNode(deleteSummary.nodeId)
  if (!(await evalJS(`document.querySelector('.danger-details').open`))) throw new Error('native account deletion disclosure did not open')
  evidence.security.deletion = await evalJS(`(()=>{const input=document.querySelector('input[name="confirm_email"]');const form=input?.form;const submit=form?.querySelector('button[type="submit"],button:not([type])');if(!input||!form||!submit)return {present:false};const emptyValid=input.checkValidity();input.value='someone-else@example.test';const wrongValid=input.checkValidity();input.value='workspace-a@example.test';const exactValid=input.checkValidity();return {present:true,required:input.required,pattern:input.pattern,emptyValid,wrongValid,exactValid,button:submit.innerText,overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth}})()`)
  await snap('security-delete-confirmation-390.png', false)
  if (!evidence.security.deletion.present || !evidence.security.deletion.required || evidence.security.deletion.emptyValid || evidence.security.deletion.wrongValid || !evidence.security.deletion.exactValid || evidence.security.deletion.overflow) throw new Error(`email-naming confirmation failed: ${JSON.stringify(evidence.security.deletion)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: true })
  let deleteDOM = await call('DOM.getDocument', { depth: 0 })
  const deleteHTML = (await call('DOM.getOuterHTML', { nodeId: deleteDOM.root.nodeId })).outerHTML
  evidence.security.deletion.noScript = { required: deleteHTML.includes('name="confirm_email"') && deleteHTML.includes(' required'), pendingCopy: deleteHTML.includes('Deletion may take time') || deleteHTML.includes('deletion may take time') }
  await snap('security-delete-confirmation-no-script.png', false)
  if (!evidence.security.deletion.noScript.required) throw new Error(`no-script deletion confirmation unavailable: ${JSON.stringify(evidence.security.deletion.noScript)}`)
  const deleteButton = await call('DOM.querySelector', { nodeId: deleteDOM.root.nodeId, selector: 'form[action="/auth/delete"] button' })
  await call('DOM.focus', { nodeId: deleteButton.nodeId })
  await call('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 })
  await call('Input.dispatchKeyEvent', { type: 'keyUp', key: 'Enter', code: 'Enter', windowsVirtualKeyCode: 13 })
  await wait(350)
  evidence.security.deletion.emptySubmitBlocked = await evalJS(`location.pathname === '/account/security'`)
  if (!evidence.security.deletion.emptySubmitBlocked) throw new Error('empty deletion confirmation submitted without an account name')
  await evalJS(`document.querySelector('input[name="confirm_email"]').value=''`)
  const deleteInput = await call('DOM.querySelector', { nodeId: deleteDOM.root.nodeId, selector: 'input[name="confirm_email"]' })
  await clickNode(deleteInput.nodeId)
  for (const ch of 'workspace-a@example.test') await call('Input.insertText', { text: ch })
  const submittedDelete = await call('DOM.querySelector', { nodeId: deleteDOM.root.nodeId, selector: 'form[action="/auth/delete"] button' })
  await clickNode(submittedDelete.nodeId)
  await wait(500)
  evidence.security.deletion.pending = await evalJS(`({url:location.pathname,heading:document.querySelector('h1')?.innerText,notComplete:document.body.innerText.includes('not complete yet'),continues:document.body.innerText.includes('even if you close this page'),statusLink:document.querySelector('.message-sheet a.button')?.getAttribute('href')||document.querySelector('.welcome a.button')?.getAttribute('href')})`)
  evidence.security.deletion.pending.response = responses.filter(r => r.url.includes('/auth/delete')).slice(-1)[0]
  await snap('security-delete-pending-no-script.png', false)
  if (evidence.security.deletion.pending.url !== '/auth/delete' || evidence.security.deletion.pending.heading !== 'Deletion pending' || !evidence.security.deletion.pending.notComplete || !evidence.security.deletion.pending.continues || !evidence.security.deletion.pending.statusLink?.startsWith('/auth/deletion/') || evidence.security.deletion.pending.response?.status !== 202) throw new Error(`deletion pending result was inaccurate or not read-only: ${JSON.stringify(evidence.security.deletion.pending)}`)
  evidence.security.deletion.viewports = {}
  for (const width of [1440, 768, 390]) {
    await call('Emulation.setDeviceMetricsOverride', { width, height: viewportHeight(width), deviceScaleFactor: 1, mobile: width < 640 })
    const result = await evalJS(`({width:innerWidth,height:innerHeight,heading:document.querySelector('h1')?.innerText,notComplete:document.body.innerText.includes('not complete yet'),continues:document.body.innerText.includes('even if you close this page'),statusLink:document.querySelector('.welcome a.button')?.getAttribute('href'),overflow:document.documentElement.scrollWidth>document.documentElement.clientWidth})`)
    evidence.security.deletion.viewports[width] = result
    await snap(`deletion-pending-${width}.png`, false)
    if (result.heading !== 'Deletion pending' || !result.notComplete || !result.continues || !result.statusLink?.startsWith('/auth/deletion/') || result.overflow) throw new Error(`deletion pending ${width}px acceptance failed: ${JSON.stringify(result)}`)
  }
  await call('Page.navigate', { url: `${base}${evidence.security.deletion.pending.statusLink}` }); await ready(); await wait(200)
  evidence.security.deletion.status = await evalJS(`({heading:document.querySelector('h1')?.innerText,notComplete:document.body.innerText.includes('not complete yet'),status:location.pathname})`)
  if (evidence.security.deletion.status.heading !== 'Deletion pending' || !evidence.security.deletion.status.notComplete) throw new Error(`status GET changed deletion outcome: ${JSON.stringify(evidence.security.deletion.status)}`)
  await call('Emulation.setScriptExecutionDisabled', { value: false })
  await writeFile(join(artifacts, 'acceptance.json'), JSON.stringify(evidence, null, 2) + '\n')
  console.log(JSON.stringify({ artifacts, evidence }, null, 2))
} finally {
  try { cdp?.close() } catch {}
  chromeProc.kill('SIGTERM')
  await new Promise(r => setTimeout(r, 300))
  await rm(profile, { recursive: true, force: true })
}
