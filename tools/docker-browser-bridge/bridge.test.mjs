import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isTokenURL, tokenSucceeded, validatedCookie } from './bridge.mjs';

// 使用虚构账号和固定假值，覆盖消息域验证信息保留及错误账号拒绝。
const fixture = [
  { name: 'unb', value: '123' }, { name: '_m_h5_tk', value: 'fake' },
  { name: '_m_h5_tk_enc', value: 'fake' }, { name: 'cookie2', value: 'fake' }
];
test('only exact official token endpoint is accepted', () => {
  assert.ok(isTokenURL('https://h5api.m.goofish.com/h5/mtop.taobao.idlemessage.pc.login.token/1.0/?a=b'));
  assert.ok(!isTokenURL('https://h5api.m.goofish.com.evil.invalid/h5/mtop.taobao.idlemessage.pc.login.token/1.0/'));
  assert.ok(!isTokenURL('https://h5api.m.goofish.com/punish'));
});
test('HTTP success alone does not prove official login', () => {
  assert.ok(!tokenSucceeded({ ret: ['FAIL_SYS_USER_VALIDATE'], data: { accessToken: 'fake' } }));
  assert.ok(!tokenSucceeded({ ret: ['SUCCESS::ok'], data: {} }));
  assert.ok(tokenSucceeded({ ret: ['SUCCESS::ok'], data: { accessToken: 'fake' } }));
});
test('interface host-only verification cookie is retained', () => {
  assert.match(validatedCookie([...fixture, { name: 'x5sec', value: 'fake-interface' }], fixture, '123'), /x5sec=fake-interface/);
});
test('wrong or ambiguous account never produces an import', () => {
  assert.throws(() => validatedCookie(fixture, [], '456'), /ACCOUNT_MISMATCH/);
  assert.throws(() => validatedCookie(fixture, [{ name: 'unb', value: '456' }], '123'), /ACCOUNT_MISMATCH/);
});
test('incomplete or injected cookie fails closed', () => {
  assert.throws(() => validatedCookie(fixture.slice(0, 2), [], '123'), /INCOMPLETE_SESSION/);
  assert.throws(() => validatedCookie([...fixture, { name: 'extra', value: 'bad;injected=yes' }], [], '123'), /INVALID_COOKIE/);
});
