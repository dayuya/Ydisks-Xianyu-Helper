// 仅重启实验浏览器，比较非敏感身份是否保持；不重启 Ydisks，不更改登录状态。
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { connect } from './bridge.mjs';
const run = promisify(execFile);

// 会话只在当前进程内比较，禁止打印或保存明文。
async function snapshot() {
  const response = await fetch('http://127.0.0.1:19389/json/version', { signal: AbortSignal.timeout(3000) });
  const info = await response.json();
  const cdp = await connect(info.webSocketDebuggerUrl);
  try {
    const result = await cdp.send('Storage.getCookies');
    const cookies = result.cookies.filter(c => c.domain === 'goofish.com' || c.domain.endsWith('.goofish.com'));
    return { identity: cookies.find(c => c.name === 'unb')?.value, session: cookies.find(c => c.name === 'cookie2')?.value, count: cookies.length };
  } finally { cdp.close(); }
}

async function main() {
  const before = await snapshot();
  if (!before.identity || !before.session) throw new Error('NO_LOGIN_TO_VERIFY');
  await run('docker', ['compose', '-f', 'tools/docker-browser-bridge/compose.yml', 'restart', 'browser'], { windowsHide: true });
  const deadline = Date.now() + 90000;
  let after;
  while (Date.now() < deadline) {
    try { after = await snapshot(); break; } catch { await new Promise(resolve => setTimeout(resolve, 2000)); }
  }
  if (!after) throw new Error('BROWSER_NOT_READY');
  const passed = before.identity === after.identity && before.session === after.session;
  console.log(JSON.stringify({ browserRestarted: true, identityPreserved: before.identity === after.identity, sessionPreserved: before.session === after.session, cookieCount: after.count, passed }));
  if (!passed) process.exitCode = 1;
}
main().catch(() => { console.error(JSON.stringify({ failed: 'PERSISTENCE_CHECK_FAILED' })); process.exitCode = 1; });
