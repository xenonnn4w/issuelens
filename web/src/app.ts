import {
  createIcons, ArrowLeft, ArrowRight, ArrowUpRight, BookOpen, ChartNoAxesCombined,
  Check, CheckCircle2, ChevronsUpDown, CircleAlert, Clock3, Copy, Download, Eye,
  EyeOff, FileCode2, FlaskConical, GitBranch as Github, History, House, KeyRound, Layers2,
  Menu, PanelLeftClose, Plus, RefreshCw, Save, ScanSearch, ShieldCheck,
  Sparkles, Trash2, X,
} from 'lucide';
import { Clerk } from '@clerk/clerk-js';
import { ClerkUI } from '@clerk/ui/entry';

const iconSet = {
  ArrowLeft, ArrowRight, ArrowUpRight, BookOpen, ChartNoAxesCombined,
  Check, CheckCircle2, ChevronsUpDown, CircleAlert, Clock3, Copy, Download, Eye,
  EyeOff, FileCode2, FlaskConical, Github, History, House, KeyRound, Layers2,
  Menu, PanelLeftClose, Plus, RefreshCw, Save, ScanSearch, ShieldCheck,
  Sparkles, Trash2, X,
};
type Evidence = { id: string; kind: string; title: string; url: string; text: string };
type RunEvent = { kind: string; message: string; time: string };
type Finding = { claim: string; evidence_ids: string[]; confidence: string };
type InvestigationReport = { summary: string; findings: Finding[]; questions: string[]; limitations: string[]; draft: string };
type Run = {
  id: string; created: string; repo: string; number: number; url: string; title: string;
  status: 'queued' | 'running' | 'ready' | 'failed'; mode: 'ai' | 'retrieval';
  events: RunEvent[]; evidence: Evidence[]; report: InvestigationReport | null; draft: string;
  approved: boolean; commit?: string; error?: string; recovered_from?: string;
};
type Config = { ai: boolean; github: boolean; model: string; deployment: 'local' | 'server'; clerk_publishable_key: string; shared_key_next_at: string };
type Usage = { total: number; ready: number; approved: number; failed: number; github_requests: number; tool_calls: number; input_tokens: number; output_tokens: number };
type Connections = { openai_api_key: string; github_token: string };
class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly code?: string, readonly retryAt?: string) { super(message); }
}

function $<T extends HTMLElement = HTMLElement>(selector: string): T {
  const element = document.querySelector<T>(selector);
  if (!element) throw new Error(`Missing UI element: ${selector}`);
  return element;
}
const esc = (value: unknown): string => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c] ?? c));
const errorMessage = (error: unknown): string => error instanceof Error ? error.message : 'Unexpected error';
let current: Run | null = null, timer: number | undefined, dirty = false, runs: Run[] = [], config: Config | null = null, toastTimer: number | undefined;
let clerk: Clerk | null = null, activeUserID = '', authMode = '', syncPending = false, sessionReady = false;
const connectionStorage = () => `issue-lens-connections:${activeUserID}`;
let personalConnections: Connections = {openai_api_key: '', github_token: ''};
function icons() { createIcons({ icons: iconSet }); }
function loadConnections() {
  try {
    const saved = JSON.parse(sessionStorage.getItem(connectionStorage()) || '{}');
    personalConnections = {openai_api_key: saved.openai_api_key || '', github_token: saved.github_token || ''};
  } catch { personalConnections = {openai_api_key: '', github_token: ''}; }
  $<HTMLInputElement>('#openai-key').value = personalConnections.openai_api_key;
  $<HTMLInputElement>('#github-token').value = personalConnections.github_token;
}
function updateConnections() {
  if (!config) return;
  const personalAI = Boolean(personalConnections.openai_api_key);
  const personalGitHub = Boolean(personalConnections.github_token);
  const ai = personalAI || config.ai;
  const next = config.shared_key_next_at ? new Date(config.shared_key_next_at) : null;
  const sharedNote = next && next.getTime() > Date.now()
    ? `Shared AI key available at ${next.toLocaleTimeString()}`
    : 'Shared AI key · one investigation per hour';
  $('#connection').textContent = ai ? config.model : 'Retrieval mode';
  $('#mode-note').textContent = personalAI ? 'Your OpenAI key · no shared-key limit' : config.ai ? sharedNote : 'AI unavailable · Add an OpenAI key in Connections';
  $('#new-github-status').textContent = personalGitHub ? 'Your token' : config.github ? 'Server token' : 'Public access';
  $('#new-ai-status').textContent = personalAI ? 'Your key' : config.ai ? 'Shared key' : 'Key required';
  $('#provider-status').innerHTML = `<div class="provider"><div><h2>GitHub</h2><p>Public issues, discussion, and commit-pinned repository code.</p></div><span class="badge">${personalGitHub ? 'Your token' : config.github ? 'Server token' : 'Unauthenticated access'}</span></div><div class="provider"><div><h2>AI provider</h2><p>${esc(config.model)} · ${esc(personalAI ? 'Your key has no shared-key limit' : config.ai ? sharedNote : 'Add your key for AI investigations')}</p></div><span class="badge">${personalAI ? 'Your key' : config.ai ? 'Shared key' : 'Key required'}</span></div><div class="provider"><div><h2>Workspace storage</h2><p>SQLite · Investigation history and reviewed responses</p></div><span class="badge">Available</span></div>`;
}
async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const token = path === '/config' ? null : await clerk?.session?.getToken();
  if (path !== '/config' && !token) throw new ApiError('Sign in to continue.', 401);
  const response = await fetch('/api' + path, {...options, headers: {'Content-Type':'application/json', ...(token ? {Authorization:`Bearer ${token}`} : {}), ...options.headers}});
  const data: { detail?: unknown; code?: unknown; retry_at?: unknown } = response.headers.get('content-type')?.includes('application/json') ? await response.json() : {};
  if (response.status === 401) throw new ApiError('Your session expired. Sign in again.', 401);
  if (!response.ok) throw new ApiError(
    typeof data.detail === 'string' ? data.detail : 'The request could not be completed. Please try again.',
    response.status,
    typeof data.code === 'string' ? data.code : undefined,
    typeof data.retry_at === 'string' ? data.retry_at : undefined,
  );
  return data as T;
}
function notice(text: string) {
  clearTimeout(toastTimer);
  const element = $('#notice');
  element.classList.remove('notice-limit');
  element.setAttribute('role', 'status');
  element.textContent = text;
  element.hidden = !text;
  if (text) toastTimer = setTimeout(() => element.hidden = true, 12000);
}
function showSharedKeyLimit(retryAt?: string) {
  clearTimeout(toastTimer);
  const element = $('#notice');
  element.replaceChildren();
  element.classList.add('notice-limit');
  element.setAttribute('role', 'alert');
  const content = document.createElement('div');
  const title = document.createElement('strong');
  title.textContent = 'Shared AI run limit reached';
  const description = document.createElement('p');
  const next = retryAt ? new Date(retryAt) : null;
  description.textContent = `You get one shared AI run per hour. Add your own OpenAI API key for more runs without this limit.${next && !Number.isNaN(next.getTime()) ? ` The shared key is available again at ${next.toLocaleTimeString([], {hour:'numeric', minute:'2-digit'})}.` : ''}`;
  const action = document.createElement('a');
  action.href = '#connections';
  action.textContent = 'Add API key';
  action.onclick = () => notice('');
  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'notice-close';
  close.setAttribute('aria-label', 'Dismiss message');
  close.title = 'Dismiss message';
  close.innerHTML = '<i data-lucide="x"></i>';
  close.onclick = () => notice('');
  content.append(title, description, action);
  element.append(content, close);
  element.hidden = false;
  icons();
}
function showRequestError(error: unknown) {
  if (error instanceof ApiError && error.status === 429 && error.code === 'shared_key_rate_limited') {
    showSharedKeyLimit(error.retryAt);
    api<Config>('/config').then(loaded => { config = loaded; updateConnections(); }).catch(() => {});
    return;
  }
  notice(errorMessage(error));
}
function route() {
  if (location.pathname.startsWith('/sign-')) return;
  const name = location.hash.slice(1).split('/')[0] || 'home';
  const view = ['home','new','investigate','usage','connections','docs'].includes(name) ? name : 'home';
  if (!activeUserID && !['home','docs'].includes(view)) { requireSession(); return; }
  document.querySelectorAll<HTMLElement>('.page-view').forEach(p=>p.hidden=p.id!=='view-'+view);
  document.querySelectorAll<HTMLElement>('[data-view]').forEach(a=>{const active=a.dataset.view===(view==='new'?'investigate':view);a.classList.toggle('active',active);a.setAttribute('aria-current',active?'page':'false');});
  document.body.classList.remove('nav-open');
  if(activeUserID && (view==='usage' || view==='home')) refreshUsage().catch(e=>notice(e.message));
  const runId=location.hash.split('/')[1];
  if(view==='investigate' && runId && current?.id!==runId) select(runId);
}
function requireSession(destination = location.hash || '#new') {
  if (activeUserID) return true;
  sessionStorage.setItem('issue-lens-return', destination);
  location.assign('/sign-in');
  return false;
}
window.addEventListener('hashchange',route);
function tab(name: string) {
  document.querySelectorAll<HTMLElement>('[data-tab]').forEach(b => b.setAttribute('aria-selected', String(b.dataset.tab === name)));
  document.querySelectorAll<HTMLElement>('[role=tabpanel]').forEach(p => p.hidden = p.id !== 'panel-' + name);
}
document.querySelectorAll<HTMLElement>('[data-tab]').forEach(b => b.onclick = () => tab(b.dataset.tab || 'report'));
async function loadHistory() {
  runs = await api<Run[]>('/runs');
  $('#count').textContent = String(runs.length);
  $('#history-count').textContent = String(runs.length);
  $('#history').innerHTML = runs.map(r => `<button data-run="${esc(r.id)}" class="${current?.id === r.id ? 'selected' : ''}">${esc(r.title)}<small>${esc(r.repo)} · ${esc(r.status)}</small></button>`).join('');
  $('#recent').innerHTML = runs.length ? runs.slice(0,4).map(r=>`<button class="recent-item" data-run="${esc(r.id)}"><span>${esc(r.repo)} · #${esc(r.url.split('/').at(-1))}</span><p>${esc(r.title)}</p><small>${esc(r.status.toUpperCase())} / ${esc(r.mode==='ai'?'AI INVESTIGATION':'RETRIEVAL')}</small></button>`).join('') : '<div class="recent-empty"><h3>Your first investigation starts here.</h3><p>Open New investigation to collect repository evidence and prepare a response.</p></div>';
  $('#activity-total').textContent=runs.length+' runs';
  $('#repository-activity').innerHTML=runs.slice(0,4).map(r=>`<div class="activity-line"><i data-lucide="${r.status==='ready'?'check-circle-2':r.status==='failed'?'circle-alert':'clock-3'}"></i><div><p>${esc(r.title)}</p><small>${esc(r.repo)} · ${esc(new Date(r.created).toLocaleDateString())}</small></div><span class="badge">${esc(r.status)}</span></div>`).join('') || '<p class="muted">No repository activity yet.</p>';
  document.querySelectorAll<HTMLElement>('[data-run]').forEach(b => b.onclick = () => { if (b.dataset.run) void select(b.dataset.run); });
  icons();
}
function canLeave() { return !dirty || confirm('Discard unsaved response edits?'); }
async function select(id: string) {
  if (!canLeave()) return;
  dirty = false; clearTimeout(timer); notice('');
  try { current = await api<Run>('/runs/' + id); location.hash='investigate/'+id; render(); await loadHistory(); poll(); }
  catch (e) { notice(errorMessage(e)); }
}
function render() {
  if (!current) return;
  $('#empty').hidden = true; $('#workspace').hidden = false;
  $('#repo').textContent = current.repo + ' / #' + current.number;
  $('#title').textContent = current.title;
  $<HTMLAnchorElement>('#issue-link').href = current.url;
  $('#run-status').textContent = (current.mode === 'ai' ? 'AI' : 'Retrieval') + ' · ' + current.status;
  $('#source-count').textContent = String(current.evidence.length);
  const report = current.report;
  if (current.status === 'failed') notice(current.error || 'Investigation failed');
  const evidence = current.evidence;
  $('#panel-report').innerHTML = report ? `<p class="summary">${esc(report.summary)}</p><div class="section-heading"><h3>Findings</h3><span class="badge">${report.findings.length} observations</span></div>${report.findings.map(f => `<div class="finding"><p>${esc(f.claim)}</p><span class="badge">${esc(f.confidence)} confidence</span>${f.evidence_ids.map(id => {const e = evidence.find(e => e.id === id); return e ? `<a class="citation" href="${esc(e.url)}" target="_blank" rel="noopener noreferrer">[${esc(id)}]</a>` : '';}).join('')}</div>`).join('')}<div class="section-heading"><h3>Open questions</h3></div><ul class="question-list">${report.questions.map(q => `<li>${esc(q)}</li>`).join('')}</ul><div class="limitations"><h3>Limits of this investigation</h3><ul>${report.limitations.map(q => `<li>${esc(q)}</li>`).join('')}</ul></div>` : `<div class="${current.status === 'failed' ? 'muted' : 'working'}">${current.status === 'failed' ? 'Investigation stopped. Collected evidence remains available. Submit the URL again to retry.' : esc(current.events.at(-1)?.message || 'Waiting for an available worker...')}</div>`;
  $('#panel-sources').innerHTML = current.evidence.map(e => `<details class="source"><summary><span class="badge">${esc(e.id)} · ${esc(e.kind)}</span> ${esc(e.title)}</summary><pre>${esc(e.text)}</pre><a href="${esc(e.url)}" target="_blank" rel="noopener noreferrer">Open source on GitHub</a></details>`).join('') || '<p class="muted">No evidence collected yet.</p>';
  $('#panel-activity').innerHTML = current.events.map(e => `<div class="event"><time>${esc(new Date(e.time).toLocaleTimeString())}</time><div><b>${esc(e.kind)}</b>${esc(e.message)}</div></div>`).join('');
  if (!dirty) $<HTMLTextAreaElement>('#draft').value = current.draft || '';
  if (current.status === 'failed' && !report) $('#panel-report').innerHTML = `<div class="limitations"><h3>Investigation stopped</h3><p>${esc(current.error || 'The investigation failed.')}</p><p>Collected evidence remains available in the Evidence tab. Submit the issue URL again to retry.</p></div>`;
  if (current.status === 'failed' && current.evidence.length && (config?.ai || personalConnections.openai_api_key)) {
    const recovery = document.createElement('button');
    recovery.textContent = 'Generate report from saved evidence';
    recovery.onclick = async () => {
      recovery.disabled = true;
      try { const data = await api<Run>('/runs/' + current!.id + '/recover', {method:'POST',body:JSON.stringify({openai_api_key:personalConnections.openai_api_key || undefined})}); dirty = false; clearTimeout(timer); current = data; location.hash = 'investigate/' + data.id; render(); await loadHistory(); poll(); }
      catch(e) {showRequestError(e); recovery.disabled = false;}
    };
    $('#panel-report').append(recovery);
  }
  $<HTMLTextAreaElement>('#draft').disabled = !report;
  $<HTMLButtonElement>('#save').disabled = !report;
  $<HTMLButtonElement>('#approve').disabled = !report;
  $<HTMLButtonElement>('#copy').disabled = !current.approved || dirty;
  $('#approval').textContent = current.approved && !dirty ? 'Approved locally' : 'Unreviewed';
  $('#snapshot').textContent = current.commit ? 'SNAPSHOT ' + current.commit.slice(0,12) : current.recovered_from ? 'Recovered from saved evidence · Original source links preserved' : 'Evidence is saved locally';
  $('#export').hidden = !report;
  icons();
}
function poll() {
  if (!current || !['queued','running'].includes(current.status)) return;
  const id = current.id;
  timer = setTimeout(async () => {
    try { const data = await api<Run>('/runs/' + id); if (current?.id !== id) return; current = data; render(); await loadHistory(); if (!['queued','running'].includes(data.status)) await refreshUsage(); poll(); }
    catch (e) { notice(errorMessage(e)); }
  }, 1400);
}
$("#start").addEventListener('submit', async event => {
  event.preventDefault(); if (!requireSession() || !canLeave()) return;
  $<HTMLButtonElement>('#investigate').disabled = true; notice('');
  try { const data = await api<Run>('/runs', {method:'POST',body:JSON.stringify({url:$<HTMLInputElement>('#url').value.trim(),mode:$<HTMLInputElement>('#baseline').checked ? 'retrieval' : 'auto',openai_api_key:personalConnections.openai_api_key || undefined,github_token:personalConnections.github_token || undefined})}); dirty = false; clearTimeout(timer); current = data; location.hash='investigate/'+data.id; tab('report'); render(); await loadHistory(); poll(); }
  catch(e) { showRequestError(e); } finally { $<HTMLButtonElement>('#investigate').disabled = false; }
});
function newRun() {if (!canLeave()) return; dirty=false;current=null;clearTimeout(timer);$('#workspace').hidden=true;$('#empty').hidden=false;$<HTMLInputElement>('#url').value='';location.hash='new';notice('');setTimeout(()=>$<HTMLInputElement>('#url').focus(),0);loadHistory().catch(e=>notice(errorMessage(e)));}
$('#new').onclick = () => { if (requireSession('#new')) newRun(); };
$('#empty-new').onclick = () => { if (requireSession('#new')) newRun(); };
$('#open-investigate').onclick = () => { if (requireSession('#new')) newRun(); };
$('#collapse').onclick=()=>{if(innerWidth<=640){document.body.classList.toggle('nav-open');}else{document.body.classList.toggle('collapsed');}};
$('#mobile-menu').onclick=()=>document.body.classList.toggle('nav-open');
$('#workspace-menu').onclick=()=>{location.hash='connections';};
$("#connection-form").addEventListener('submit', event => {
  event.preventDefault();
  personalConnections = {openai_api_key: $<HTMLInputElement>('#openai-key').value.trim(), github_token: $<HTMLInputElement>('#github-token').value.trim()};
  try {
    if (personalConnections.openai_api_key || personalConnections.github_token) sessionStorage.setItem(connectionStorage(), JSON.stringify(personalConnections));
    else sessionStorage.removeItem(connectionStorage());
  } catch { notice('Browser storage is unavailable. Keys will work until this page reloads.'); updateConnections(); return; }
  updateConnections();
  notice('Connections saved for this browser tab.');
});
$('#clear-connections').onclick = () => {
  try { sessionStorage.removeItem(connectionStorage()); } catch {}
  personalConnections = {openai_api_key: '', github_token: ''};
  $<HTMLInputElement>('#openai-key').value = $<HTMLInputElement>('#github-token').value = '';
  updateConnections();
  notice('Browser-tab connections cleared.');
};
document.querySelectorAll<HTMLElement>('[data-toggle-secret]').forEach(button => button.onclick = () => {
  const field = $<HTMLInputElement>('#' + button.dataset.toggleSecret);
  const visible = field.type === 'password';
  field.type = visible ? 'text' : 'password';
  button.setAttribute('aria-pressed', String(visible));
  button.innerHTML = `<i data-lucide="${visible ? 'eye-off' : 'eye'}"></i>`;
  icons();
});
$('#draft').oninput = () => {dirty=true;$<HTMLButtonElement>('#copy').disabled=true;$('#approval').textContent='Unsaved edits';};
async function save(approved: boolean) {
  if (!current) return;
  $<HTMLButtonElement>('#save').disabled = $<HTMLButtonElement>('#approve').disabled = true;
  try { current = await api<Run>('/runs/' + current.id + '/review',{method:'PATCH',body:JSON.stringify({draft:$<HTMLTextAreaElement>('#draft').value,approved})}); dirty=false;render();await loadHistory();await refreshUsage();notice(approved ? 'Response approved. You can now copy it for GitHub.' : 'Draft saved.'); }
  catch(e) {notice(errorMessage(e));} finally {$<HTMLButtonElement>('#save').disabled = $<HTMLButtonElement>('#approve').disabled = !current?.report;}
}
$('#save').onclick = () => save(false); $('#approve').onclick = () => save(true);
$('#copy').onclick = async () => {
  try { if (!current) return; await navigator.clipboard.writeText(current.draft); notice('Approved response copied.'); } catch {notice('Clipboard unavailable. Select the response text to copy it manually.');}
};
$('#export').onclick = async event => {
  event.preventDefault();
  if (!current) return;
  try {
    const token = await clerk?.session?.getToken();
    if (!token) throw new Error('Sign in to download the report.');
    const response = await fetch(`/api/runs/${current.id}/export`, {headers:{Authorization:`Bearer ${token}`}});
    if (!response.ok) throw new Error('Could not download the report.');
    const url = URL.createObjectURL(await response.blob());
    const link = document.createElement('a'); link.href = url; link.download = `issue-${current.number}-report.md`; link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (error) { notice(errorMessage(error)); }
};
window.addEventListener('beforeunload',e=>{if(dirty){e.preventDefault();e.returnValue='';}});
async function refreshUsage() {
  const u=await api<Usage>('/usage');
  $('#usage-preview').innerHTML=`<div class="usage-mini"><div><strong>${u.total}</strong><span>Investigations</span></div><div><strong>${u.ready}</strong><span>Completed</span></div></div>`;
  const stats=[['Investigations',u.total],['Completed',u.ready],['Approved drafts',u.approved],['Failed',u.failed],['GitHub requests',u.github_requests],['Tool operations',u.tool_calls],['Input tokens',u.input_tokens],['Output tokens',u.output_tokens]];
  $('#usage-stats').innerHTML=stats.map(([label,n])=>`<div class="stat"><span>${label}</span><strong>${Number(n).toLocaleString()}</strong></div>`).join('');
  $('#usage-table').innerHTML='<div class="table-wrap"><table><thead><tr><th>Investigation</th><th>Mode</th><th>Status</th><th>Created</th></tr></thead><tbody>'+runs.map(r=>`<tr><td><a href="#investigate/${esc(r.id)}">${esc(r.title)}</a></td><td data-label="Mode">${esc(r.mode)}</td><td data-label="Status">${esc(r.status)}</td><td data-label="Created">${esc(new Date(r.created).toLocaleDateString())}</td></tr>`).join('')+'</tbody></table></div>';
}
$('#refresh-usage').onclick=()=>refreshUsage().catch(e=>notice(errorMessage(e)));
async function boot() {
  icons();
  document.body.classList.toggle('auth-required', location.pathname.startsWith('/sign-'));
  try {
    config = await api<Config>('/config');
    if (!config.clerk_publishable_key) throw new Error('Clerk is not configured on this server.');
    clerk = new Clerk(config.clerk_publishable_key);
    await clerk.load({ui:{ClerkUI}, signInUrl:'/sign-in', signUpUrl:'/sign-up'});
    clerk.addListener(() => { void syncSession(); });
    await syncSession();
  } catch (error) { $('#auth-error').textContent = errorMessage(error); }
}
let syncing = false;
async function syncSession() {
  if (!clerk) return;
  if (syncing) { syncPending = true; return; }
  const id = clerk.user?.id || '';
  if (id === activeUserID && sessionReady) return;
  syncing = true;
  sessionReady = true;
  clearTimeout(timer);
  current = null; runs = []; dirty = false; personalConnections = {openai_api_key:'', github_token:''};
  activeUserID = id;
  const authPage = location.pathname.startsWith('/sign-');
  document.body.classList.toggle('auth-required', !id && authPage);
  if (id) {
    unmountAuth();
    $('#auth-screen').hidden = true;
    clerk.mountUserButton($<HTMLDivElement>('#user-button'));
    if (authPage) {
      const destination = sessionStorage.getItem('issue-lens-return') || '#home';
      sessionStorage.removeItem('issue-lens-return');
      history.replaceState(null, '', '/' + destination);
    }
    loadConnections(); updateConnections(); route();
    try { await loadHistory(); await refreshUsage(); } catch (error) { notice(errorMessage(error)); }
  } else {
    $('#auth-screen').hidden = !authPage;
    clerk.unmountUserButton($<HTMLDivElement>('#user-button'));
    $<HTMLDivElement>('#user-button').replaceChildren();
    $('#recent').innerHTML = '<div class="recent-empty"><h3>Start your first investigation.</h3><p>Sign in when you are ready to investigate a GitHub issue.</p></div>';
    $('#repository-activity').textContent = 'Your repository activity appears here after sign-in.';
    $('#activity-total').textContent = '';
    $('#history').replaceChildren();
    $('#count').textContent = $('#history-count').textContent = '0';
    $('#usage-preview').textContent = 'Sign in to view your usage.';
    if (authPage) showAuth(); else route();
  }
  syncing = false;
  if (syncPending) { syncPending = false; void syncSession(); }
}
function unmountAuth() {
  if (!clerk) return;
  const node = $<HTMLDivElement>('#auth-component');
  if (authMode === 'signup') clerk.unmountSignUp(node);
  if (authMode === 'signin') clerk.unmountSignIn(node);
  authMode = '';
  node.replaceChildren();
}
function showAuth() {
  if (!clerk || activeUserID || !location.pathname.startsWith('/sign-')) return;
  const mode = location.pathname.startsWith('/sign-up') ? 'signup' : 'signin';
  if (mode === authMode) return;
  unmountAuth();
  const auth = $<HTMLDivElement>('#auth-component');
  const signup = mode === 'signup';
  authMode = mode;
  $('#auth-title').textContent = signup ? 'Create your account' : 'Sign in to Issue Lens';
  const destination = '/' + (sessionStorage.getItem('issue-lens-return') || '#home');
  if (signup) clerk.mountSignUp(auth, {routing:'path', path:'/sign-up', signInUrl:'/sign-in', forceRedirectUrl:destination});
  else clerk.mountSignIn(auth, {routing:'path', path:'/sign-in', signUpUrl:'/sign-up', forceRedirectUrl:destination});
}
window.addEventListener('hashchange', showAuth);
void boot();
