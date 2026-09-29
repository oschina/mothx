// 回归测试:任务输入区的供应商选择器(报告 bug 5)。
//
// Desktop 之前把 ACP providers/list 投影里的 apiKeyConfigured 直接丢掉,
// 于是没填密钥的供应商和常用供应商一起堆在选择器里。现在 Desktop 只渲染
// ACP 投影声明为已配置的供应商,并按投影顺序(默认供应商 → 最近使用 →
// 目录优先级)排列;它自己不判断可用性,也不自己排序。
import assert from 'node:assert/strict';
import test from 'node:test';

import { providerIsConfigured, visibleConfigOptions } from './composer.ts';
import { state, type SessionConfigOptionShape } from './state.ts';

const handlers = new Map<string, (params: unknown) => unknown>();
const previousWindow = (globalThis as { window?: unknown }).window;
(globalThis as { window?: unknown }).window = {
  mothx: {
    isDesktop: true,
    platform: 'linux',
    acp: {
      invoke: async (method: string, params?: unknown) => ({ ok: true, result: handlers.get(method)?.(params) ?? {} }),
      notify: () => undefined,
      respond: () => undefined,
      cancelReverse: () => undefined,
      getState: async () => ({ state: 'ready', workspace: '' }),
      restart: async () => ({ ok: true, result: {} }),
      onEvent: () => () => undefined,
    },
    desktop: { logDiagnostic: () => undefined, storeGet: async () => state.store, storeSet: async (patch: Record<string, unknown>) => Object.assign(state.store, patch) },
  },
};

function providerOption(values: string[]): SessionConfigOptionShape {
  return {
    type: 'select',
    id: 'provider',
    name: 'Provider',
    currentValue: values[0] || '',
    options: values.map((value) => ({ value, name: value, description: '' })),
  };
}

function catalog(providers: { name: string; apiKeyConfigured: boolean }[], defaultProvider: string): unknown {
  return { providers, defaultProvider, defaultModel: '', models: [] };
}

test.after(() => {
  (globalThis as { window?: unknown }).window = previousWindow;
});

test('the task picker hides providers without a configured key', async () => {
  handlers.set('mothx/manage/providers/list', () =>
    catalog(
      [
        { name: 'openai', apiKeyConfigured: true },
        { name: 'never-configured', apiKeyConfigured: false },
        { name: 'local-llm', apiKeyConfigured: false },
      ],
      'openai',
    ),
  );
  try {
    const { refreshDraftConfigOptions } = await import('./composer.ts');
    state.connection = { state: 'ready', workspace: '', agentCapabilities: { _meta: { 'mothx.dev': { features: ['manageProviders'] } } } };
    await refreshDraftConfigOptions();

    const providerOptionValue = state.draftConfigOptions.find((option) => option.id === 'provider');
    const values = (providerOptionValue?.options || []).map((choice) => choice.value);
    assert.deepEqual(values, ['openai'], 'only a configured provider may reach the new-task picker');
    assert.equal(providerIsConfigured('openai'), true);
    assert.equal(providerIsConfigured('local-llm'), false);

    // 会话内的选择器走同一个过滤与顺序。
    const sessionOptions = visibleConfigOptions([providerOption(['local-llm', 'openai', 'never-configured'])]);
    assert.deepEqual(sessionOptions[0].options?.map((choice) => choice.value), ['openai']);
  } finally {
    handlers.delete('mothx/manage/providers/list');
    state.draftConfigOptions = [];
    state.connection = { state: 'idle', workspace: '' };
  }
});

test('the task picker keeps the ACP-projected provider order', async () => {
  // 投影顺序:默认供应商置顶,其次是最近用过的,未使用的排在后面。
  handlers.set('mothx/manage/providers/list', () =>
    catalog(
      [
        { name: 'default-provider', apiKeyConfigured: true },
        { name: 'recently-used', apiKeyConfigured: true },
        { name: 'never-used', apiKeyConfigured: true },
      ],
      'default-provider',
    ),
  );
  try {
    const { refreshDraftConfigOptions } = await import('./composer.ts');
    state.connection = { state: 'ready', workspace: '', agentCapabilities: { _meta: { 'mothx.dev': { features: ['manageProviders'] } } } };
    await refreshDraftConfigOptions();

    const draft = state.draftConfigOptions.find((option) => option.id === 'provider');
    assert.deepEqual(
      draft?.options?.map((choice) => choice.value),
      ['default-provider', 'recently-used', 'never-used'],
      'the draft picker must keep the Runtime-projected order',
    );

    const sessionOptions = visibleConfigOptions([providerOption(['never-used', 'recently-used', 'default-provider'])]);
    assert.deepEqual(
      sessionOptions[0].options?.map((choice) => choice.value),
      ['default-provider', 'recently-used', 'never-used'],
      'a session picker must show the same providers first as the next-task picker',
    );
  } finally {
    handlers.delete('mothx/manage/providers/list');
    state.draftConfigOptions = [];
    state.connection = { state: 'idle', workspace: '' };
  }
});

test('an older runtime without the usage projection keeps every provider', async () => {
  handlers.set('mothx/manage/providers/list', () =>
    // 旧运行时没有 apiKeyConfigured 字段:不得凭空隐藏供应商。
    catalog([{ name: 'openai' }, { name: 'other' }] as { name: string; apiKeyConfigured: boolean }[], 'openai'),
  );
  try {
    const { refreshDraftConfigOptions } = await import('./composer.ts');
    state.connection = { state: 'ready', workspace: '', agentCapabilities: { _meta: { 'mothx.dev': { features: ['manageProviders'] } } } };
    await refreshDraftConfigOptions();
    const draft = state.draftConfigOptions.find((option) => option.id === 'provider');
    assert.deepEqual(draft?.options?.map((choice) => choice.value), ['openai', 'other']);
    assert.equal(providerIsConfigured('other'), true);
  } finally {
    handlers.delete('mothx/manage/providers/list');
    state.draftConfigOptions = [];
    state.connection = { state: 'idle', workspace: '' };
  }
});

test('a runtime that projects no configured provider never empties the menu', () => {
  handlers.set('mothx/manage/providers/list', () => catalog([{ name: 'openai', apiKeyConfigured: false }], 'openai'));
  state.connection = { state: 'ready', workspace: '', agentCapabilities: { _meta: { 'mothx.dev': { features: ['manageProviders'] } } } };
  return import('./composer.ts')
    .then(async ({ refreshDraftConfigOptions, visibleConfigOptions: visible }) => {
      await refreshDraftConfigOptions();
      const draft = state.draftConfigOptions.find((option) => option.id === 'provider');
      assert.ok((draft?.options?.length || 0) > 0, 'the picker must always offer something to select');
      const only = visible([providerOption(['openai'])]);
      assert.deepEqual(only[0].options?.map((choice) => choice.value), ['openai']);
    })
    .finally(() => {
      handlers.delete('mothx/manage/providers/list');
      state.draftConfigOptions = [];
      state.connection = { state: 'idle', workspace: '' };
    });
});
