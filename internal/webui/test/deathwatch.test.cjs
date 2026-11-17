const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

async function page(failSave = false) {
  class Element {
    constructor(tagName = 'DIV') {
      Object.assign(this, { tagName, value: '', checked: false, dataset: {}, children: [], options: [], textContent: '', valid: true });
      this.classList = { add() {}, remove() {}, toggle() {} };
      this.fields = {};
      this.elements = { namedItem: name => this.fields[name] || null };
      this.submit = { disabled: false };
    }
    addEventListener() {}
    setAttribute() {}
    append(...elements) { this.children.push(...elements); }
    replaceChildren(...elements) { this.children = elements; }
    querySelector() { return this.submit; }
    reportValidity() { return this.valid; }
  }

  const elements = new Map();
  const get = id => {
    if (!elements.has(id)) elements.set(id, new Element());
    return elements.get(id);
  };
  const form = get('death-form');
  for (const [name, value] of Object.entries({ id: '76561198101153601', interval: '15', sound: 'none', enabled: '' })) {
    const field = new Element('INPUT');
    field.value = value;
    field.type = name === 'enabled' ? 'checkbox' : 'text';
    form.fields[name] = field;
    form.elements[name] = field;
  }

  const state = {
    phase: 'running', error: '', logs: '',
    settings: { player_id: '76561198101153601' },
    runtime: { connected: true, switches: {}, death_running: false },
    bot: { alarms: [], players: [], death: { steam_id: '76561198101153601', interval_sec: 15, sound: 'none', enabled: false } },
    players: []
  };
  const actions = [];
  const context = vm.createContext({
    document: { getElementById: get, querySelectorAll: () => [], createElement: tag => new Element(tag.toUpperCase()) },
    FormData: class {
      constructor(form) { this.form = form; }
      *[Symbol.iterator]() {
        for (const [key, field] of Object.entries(this.form.fields)) {
          if (field.type !== 'checkbox' || field.checked) yield [key, field.type === 'checkbox' ? 'on' : field.value];
        }
      }
    },
    setInterval() {},
    fetch: async (url, options) => {
      if (url === '/api/action') {
        const data = JSON.parse(options.body);
        actions.push(data);
        if (failSave) return { ok: false, json: async () => ({ error: 'disk write failed' }) };
        state.bot.death = { steam_id: data.id, interval_sec: data.interval, sound: data.sound, enabled: data.enabled };
        state.runtime.death_running = data.enabled;
      }
      return { ok: true, json: async () => ({ data: url === '/api/state' ? structuredClone(state) : url === '/api/sounds' ? [] : null }) };
    }
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../static/app.js'), 'utf8'), context);
  await new Promise(resolve => setImmediate(resolve));
  return { context, form, get, actions, state };
}

test('selecting a team member saves and enables death-watch in one click', async () => {
  const p = await page();
  await vm.runInContext('selectDeathTarget({id:"76561198101153603",name:"Teammate"})', p.context);
  assert.equal(p.actions.length, 1);
  assert.equal(p.actions[0].id, '76561198101153603');
  assert.equal(p.actions[0].enabled, true);
  assert.equal(p.form.elements.enabled.checked, true);
  assert.equal(p.state.runtime.death_running, true);
  assert.match(p.get('death-feedback').textContent, /Teammate/);
});

test('normal form submission can still disable death-watch', async () => {
  const p = await page();
  await vm.runInContext('saveDeathWatch($("death-form"))', p.context);
  assert.equal(p.actions[0].enabled, false);
  assert.equal(p.state.bot.death.enabled, false);
});

test('a failed save stays visibly unsaved and re-enables the button', async () => {
  const p = await page(true);
  await vm.runInContext('selectDeathTarget({id:"76561198101153603",name:"Teammate"})', p.context);
  assert.equal(p.state.bot.death.enabled, false);
  assert.equal(p.form.dataset.dirty, 'true');
  assert.equal(p.form.submit.disabled, false);
  assert.match(p.get('death-feedback').textContent, /disk write failed/);
});
