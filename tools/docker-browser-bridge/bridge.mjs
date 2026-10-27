// 独立接入验证工具：只观察用户本人登录，不执行滑块、不发送消息。
import { readFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { pathToFileURL } from 'node:url';

// 固定本机目的地，禁止把浏览器会话转发到外部服务器。
const backend = 'http://127.0.0.1:19288';
const debugBase = 'http://127.0.0.1:19389';

// 校验官方消息登录接口，排除验证页面和相似域名。
export function isTokenURL(value) {
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && url.hostname === 'h5api.m.goofish.com'
      && url.pathname === '/h5/mtop.taobao.idlemessage.pc.login.token/1.0/';
  } catch { return false; }
}

// 成功必须同时包含业务成功状态与真正的消息凭据；不输出凭据。
export function tokenSucceeded(value) {
  return Array.isArray(value?.ret) && value.ret.some(x => String(x).startsWith('SUCCESS::'))
    && typeof value?.data?.accessToken === 'string' && value.data.accessToken.length > 0;
}

// 两个来源按消息接口优先合并；账号身份冲突或缺失时拒绝写入。
export function validatedCookie(interfaceCookies, homeCookies, accountID) {
  const all = [...interfaceCookies, ...homeCookies];
  const identities = new Set(all.filter(c => c.name === 'unb').map(c => c.value));
  if (identities.size !== 1 || !identities.has(accountID)) throw new Error('ACCOUNT_MISMATCH');
  const selected = new Map();
  for (const cookie of all) {
    if (!selected.has(cookie.name)) selected.set(cookie.name, cookie.value);
  }
  for (const name of ['unb', '_m_h5_tk', '_m_h5_tk_enc', 'cookie2']) {
    if (!selected.get(name)) throw new Error('INCOMPLETE_SESSION');
  }
  for (const [name, value] of selected) {
    if (/[;\r\n]/.test(name + value)) throw new Error('INVALID_COOKIE');
  }
  return [...selected].map(([name, value]) => `${name}=${value}`).join('; ');
}

// CDP 连接只保留本进程所需请求；关闭与超时均释放挂起调用。
export async function connect(url) {
  const address = new URL(url);
  address.hostname = '127.0.0.1';
  address.port = '19389';
  const socket = new WebSocket(address);
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', () => reject(new Error('CDP_CONNECT_FAILED')), { once: true });
  });
  let nextID = 0;
  const pending = new Map();
  const listeners = new Set();
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.id) {
      const request = pending.get(message.id);
      if (!request) return;
      clearTimeout(request.timer);
      pending.delete(message.id);
      if (message.error) request.reject(new Error('CDP_COMMAND_FAILED'));
      else request.resolve(message.result);
    } else {
      for (const listener of listeners) listener(message);
    }
  });
  socket.addEventListener('close', () => {
    for (const request of pending.values()) {
      clearTimeout(request.timer);
      request.reject(new Error('CDP_CLOSED'));
    }
    pending.clear();
  });
  return {
    send(method, params = {}) {
      return new Promise((resolve, reject) => {
        const id = ++nextID;
        const timer = setTimeout(() => { pending.delete(id); reject(new Error('CDP_TIMEOUT')); }, 10000);
        pending.set(id, { resolve, reject, timer });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
    listen(callback) { listeners.add(callback); },
    close() { socket.close(); }
  };
}

// 管理端仅接收白名单字段；失败正文可能含敏感信息，因此不打印。
async function localRequest(path, session, method = 'GET', body) {
  const response = await fetch(backend + path, {
    method, redirect: 'error', signal: AbortSignal.timeout(15000),
    headers: { 'Content-Type': 'application/json', ...(session ? { Cookie: session } : {}) },
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  if (!response.ok) throw new Error(`LOCAL_API_${response.status}`);
  return { data: await response.json(), session: response.headers.getSetCookie().map(v => v.split(';')[0]).join('; ') };
}

// 从本机现有部署读取管理密码，秘密只保留在进程内，不写日志或报告。
async function adminSession() {
  const text = await readFile(new URL('../../.env', import.meta.url), 'utf8');
  const password = text.split(/\r?\n/).find(v => v.startsWith('XIANYU_ADMIN_PASSWORD='))?.split('=').slice(1).join('=').replace(/^"|"$/g, '');
  if (!password) throw new Error('ADMIN_CONFIG_MISSING');
  const result = await localRequest('/api/v1/session/login', '', 'POST', { username: 'admin', password });
  if (!result.session) throw new Error('ADMIN_LOGIN_FAILED');
  return result.session;
}

// 只读取指定账号非敏感版本，供现有 API 原子冲突检查使用，不读取数据库凭据。
function revision(accountID) {
  if (!/^\d+$/.test(accountID)) throw new Error('INVALID_ACCOUNT');
  const value = Number(execFileSync('docker', ['exec', 'ydisks-xianyu-helper-postgres-1', 'psql', '-U', 'xianyu', '-d', 'xianyu', '-Atc', `SELECT last_refresh_at FROM cookies WHERE id='${accountID}'`], { encoding: 'utf8', windowsHide: true }).trim());
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error('REVISION_UNAVAILABLE');
  return value;
}

// 等待人工登录产生的官方成功响应；一次成功最多更新一次，超时不改变账号。
async function main() {
  const mode = process.argv[2] || 'check';
  const accountID = process.argv[3];
  if (!['check', 'watch', 'apply'].includes(mode)) throw new Error('INVALID_MODE');
  const info = await (await fetch(debugBase + '/json/version')).json();
  const browser = await connect(info.webSocketDebuggerUrl);
  if (mode === 'check') {
    try {
      const result = await browser.send('Storage.getCookies');
      console.log(JSON.stringify({ cdp: true, browser: info.Browser, cookieRead: Array.isArray(result.cookies) }));
    } finally { browser.close(); }
    return;
  }
  browser.close();
  if (!/^\d+$/.test(accountID || '')) throw new Error('INVALID_ACCOUNT');
  const expectedRevision = mode === 'apply' ? revision(accountID) : 0;
  const pages = await (await fetch(debugBase + '/json/list')).json();
  const targets = pages.filter(p => p.type === 'page' && p.webSocketDebuggerUrl);
  if (targets.length !== 1) throw new Error('KEEP_ONE_BROWSER_TAB');
  const page = await connect(targets[0].webSocketDebuggerUrl);
  const candidates = new Set();
  let finished = false;
  let processing = false;
  let timer;
  const done = new Promise((resolve, reject) => {
    timer = setTimeout(() => reject(new Error('WAITING_FOR_MANUAL_LOGIN_TIMEOUT')), 15 * 60 * 1000);
    page.listen(message => {
      if (message.method === 'Network.responseReceived' && isTokenURL(message.params.response.url)) candidates.add(message.params.requestId);
      if (message.method !== 'Network.loadingFinished' || !candidates.delete(message.params.requestId) || finished || processing) return;
      processing = true;
      (async () => {
        const response = await page.send('Network.getResponseBody', { requestId: message.params.requestId });
        const body = JSON.parse(response.base64Encoded ? Buffer.from(response.body, 'base64').toString('utf8') : response.body);
        if (!tokenSucceeded(body)) { console.log(JSON.stringify({ officialTokenSuccess: false, waitingForUser: true })); return; }
        const api = await page.send('Network.getCookies', { urls: ['https://h5api.m.goofish.com/'] });
        const home = await page.send('Network.getCookies', { urls: ['https://www.goofish.com/'] });
        const value = validatedCookie(api.cookies, home.cookies, accountID);
        finished = true;
        console.log(JSON.stringify({ officialTokenSuccess: true, accountMatches: true, hasVerificationCookie: [...api.cookies, ...home.cookies].some(c => c.name === 'x5sec') }));
        if (mode === 'apply') {
          const session = await adminSession();
          const result = await localRequest(`/api/v1/accounts/${accountID}`, session, 'PUT', { value, last_refresh_at: expectedRevision });
          if (result.data.success !== true) throw new Error('UPDATE_NOT_CONFIRMED');
          console.log(JSON.stringify({ ydisksSessionUpdated: true, runtimeCheckRequired: true }));
        }
        resolve();
      })().catch(reject).finally(() => { processing = false; });
    });
  });
  try {
    await page.send('Network.enable');
    console.log(JSON.stringify({ watching: true, mode, deadlineMinutes: 15 }));
    await done;
  } finally { clearTimeout(timer); page.close(); }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    const safe = /^(ACCOUNT_MISMATCH|INCOMPLETE_SESSION|INVALID_COOKIE|INVALID_ACCOUNT|INVALID_MODE|REVISION_UNAVAILABLE|KEEP_ONE_BROWSER_TAB|WAITING_FOR_MANUAL_LOGIN_TIMEOUT|ADMIN_CONFIG_MISSING|ADMIN_LOGIN_FAILED|UPDATE_NOT_CONFIRMED|CDP_[A-Z_]+|LOCAL_API_\d+)$/.test(error.message);
    console.error(JSON.stringify({ failed: safe ? error.message : 'CHECK_FAILED_NO_SENSITIVE_DETAILS' }));
    process.exitCode = 1;
  });
}
