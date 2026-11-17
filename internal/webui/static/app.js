'use strict';

const $ = id => document.getElementById(id);
let state = null;
let currentPage = 'overview';
let loadedForms = false;
let polling = false;
let playerRefresh = false;
let lastPlayerRefresh = 0;
let teamMembers = [];
let teamLoaded = false;
let deathSaving = false;
let notificationSignature = '';

function notice(message, error = false) {
  $('notice').textContent = message;
  $('notice').className = 'notice' + (error ? ' error' : '');
}

async function request(path, body, method = 'POST') {
  const options = { method, headers: { 'Content-Type': 'application/json' } };
  if (method !== 'GET') options.body = JSON.stringify(body ?? {});

  const response = await fetch(path, options);
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || 'Не удалось выполнить запрос');
  return result.data;
}

async function perform(task, message = 'Изменения сохранены и применены.') {
  try {
    const value = await task();
    if (message) notice(message);
    await refresh();
    return value;
  } catch (error) {
    notice(error.message, true);
    return null;
  }
}

function action(input) { return request('/api/action', input); }

function switchPage(page) {
  currentPage = page;
  document.querySelectorAll('.page').forEach(section => section.classList.toggle('hidden', section.id !== page));
  document.querySelectorAll('nav button').forEach(button => button.classList.toggle('active', button.dataset.page === page));
  $('page-title').textContent = {overview:'Обзор', players:'Игроки', devices:'Устройства', settings:'Настройки', notifications:'Уведомления Rust+',map:'Карта'}[page];
  if (page === 'players' && state?.phase === 'running' && state.settings.bm_server) refreshPlayers();
}

document.querySelectorAll('nav button').forEach(button => button.addEventListener('click', () => switchPage(button.dataset.page)));

function fill(form, values) {
  if (form.dataset.dirty === 'true') return;
  for (const [key, value] of Object.entries(values)) {
    const field = form.elements.namedItem(key);
    if (!field) continue;
    if (field.type === 'checkbox') field.checked = Boolean(value);
    else {
      if (field.tagName === 'SELECT' && !Array.from(field.options).some(option => option.value === String(value))) {
        field.add(new Option(value, value));
      }
      field.value = value ?? '';
    }
  }
}

document.querySelectorAll('form').forEach(form => form.addEventListener('input', () => { form.dataset.dirty = 'true'; }));

function formData(form) {
  return Object.fromEntries(new FormData(form));
}

function button(text, callback, className = 'secondary small') {
  const element = document.createElement('button');
  element.type = 'button';
  element.className = className;
  element.textContent = text;
  element.addEventListener('click', callback);
  return element;
}

function row(title, detail, actions = []) {
  const element = document.createElement('div');
  element.className = 'list-row';
  const info = document.createElement('div');
  const heading = document.createElement('div');
  heading.className = 'row-title';
  heading.textContent = title;
  const description = document.createElement('div');
  description.className = 'row-detail';
  description.textContent = detail;
  info.append(heading, description);
  const controls = document.createElement('div');
  controls.className = 'row-actions';
  controls.append(...actions);
  element.append(info, controls);
  return element;
}

function list(target, rows, empty) {
  if (!rows.length) {
    const message = document.createElement('p');
    message.className = 'empty';
    message.textContent = empty;
    target.replaceChildren(message);
  } else target.replaceChildren(...rows);
}

function renderPlayers() {
  if (!state) return;
  const tracked = new Set(state.bot.players.map(player => player.id));
  const search = $('player-search').value.toLowerCase();
  const players = state.players.filter(player => `${player.name} ${player.id}`.toLowerCase().includes(search));

  list($('server-players'), players.map(player => {
    const control = tracked.has(player.id)
      ? button('✓', () => notice('Этот игрок уже отслеживается.'))
      : button('+', () => perform(() => action({action:'track_add', id:player.id, name:player.name})), 'plus');
    control.setAttribute('aria-label', tracked.has(player.id) ? `${player.name} уже отслеживается` : `Отслеживать ${player.name}`);
    return row(player.name || 'Без имени', `BM ID ${player.id}`, [control]);
  }), state.phase === 'running' ? 'Игроков нет или список ещё не обновлён.' : 'Запустите бота и обновите список.');

  list($('tracked-players'), state.bot.players.map(player => row(player.name || player.id,
    `${player.online ? '● Онлайн' : '○ Не в списке онлайн'} · BM ID ${player.id}`,
    [button('Убрать', () => perform(() => action({action:'track_delete', id:player.id})), 'text-button danger')]
  )), 'Пока никого не отслеживаем. Добавьте игрока кнопкой +.');

  $('tracked-total').textContent = state.bot.players.length;
}

function render() {
  const names = {stopped:'Остановлен', starting:'Подключение…', running:state.runtime.connected ? 'Подключён' : 'Переподключение…', stopping:'Остановка…', error:'Ошибка запуска'};
  $('status').textContent = names[state.phase] || state.phase;
  $('status').className = 'badge' + (state.runtime.connected ? ' connected' : state.phase === 'error' ? ' error' : '');
  $('start').disabled = ['starting','running','stopping'].includes(state.phase);
  $('stop').disabled = !['starting','running'].includes(state.phase);
  $('server-name').textContent = state.runtime.server || 'Подключение не установлено';
  $('connection-note').textContent = state.error || (state.runtime.chat_error ? `Rust+ подключён. Чат: сервер не подтвердил отправку (${state.runtime.chat_error}).` : '') || (state.phase === 'starting' ? 'Проверяем соединение. Первая попытка ограничена одной минутой.' : state.runtime.connected ? 'Rust+ подключён · изменения устройств и списков применяются сразу.' : 'Настройте сервер, затем запустите бота.');
  $('online-count').textContent = state.phase === 'running' && state.settings.bm_server ? state.players.length : '—';
  $('tracked-count').textContent = state.bot.players.length;
  $('device-count').textContent = state.bot.alarms.length + Number(Boolean(state.bot.bt1)) + Number(Boolean(state.bot.bt2));
  $('death-status').textContent = state.runtime.death_running ? 'Отслеживание включено' : state.bot.death?.enabled ? 'Включится при запуске бота' : 'Выключен';
  $('death-status').className = 'badge' + (state.runtime.death_running ? ' connected' : '');

  if (!loadedForms) {
    fill($('settings-form'), state.settings);
    loadedForms = true;
  }

  for (const slot of [1,2]) {
    const device = state.bot['bt'+slot];
    fill($('switch-form-'+slot), device || {id:'',name:''});
    const power = state.runtime.switches?.[String(slot)];
    $('switch-state-'+slot).textContent = !device ? 'Не привязан' : !state.runtime.connected ? 'Нет связи' : power === undefined ? 'Не проверен' : power ? 'Включён' : 'Выключен';
    document.querySelectorAll(`[data-power][data-slot="${slot}"]`).forEach(button => { button.disabled = !device || !state.runtime.connected; });
  }

  fill($('death-form'), state.bot.death ? {id:state.bot.death.steam_id, interval:state.bot.death.interval_sec, sound:state.bot.death.sound || 'none', enabled:state.bot.death.enabled} : {id:state.settings.player_id, interval:15, sound:'none', enabled:false});
  $('player-token-note').textContent = state.settings.has_player_token ? 'Токен сохранён. Оставьте поле пустым, чтобы сохранить его.' : 'Нужен токен из уведомления привязки сервера.';
  $('bm-token-note').textContent = state.settings.has_bm_token ? 'Токен сохранён. Пустое поле не изменит его.' : 'Токен хранится только в вашем конфиге.';
  $('mute-alarms').textContent = state.bot.muted ? 'Включить звуки' : 'Выключить звуки';

  list($('alarm-list'), state.bot.alarms.map(alarm => row(alarm.name, `ID ${alarm.id} · ${alarm.sound || 'без звука'}${alarm.message ? ' · '+alarm.message : ''}`, [
    button('Изменить', () => {
      const form = $('alarm-form');
      delete form.dataset.dirty;
      fill(form, alarm);
      form.dataset.dirty = 'true';
      form.elements.name.focus();
    }),
    button('Удалить', () => { if (confirm(`Убрать аларм «${alarm.name}»?`)) perform(() => action({action:'alarm_delete', id:alarm.id})); }, 'text-button danger')
  ])), 'Алармы ещё не добавлены.');

  renderPlayers();
  renderTeam();
  renderNotifications();
  if (typeof RustMap !== 'undefined') RustMap.update(state);
  const logs = $('logs');
  const nearBottom = logs.scrollHeight - logs.scrollTop - logs.clientHeight < 50;
  logs.textContent = state.logs || 'Журнал пока пуст.';
  if (nearBottom) logs.scrollTop = logs.scrollHeight;
}

async function refresh() {
  if (polling) return;
  polling = true;
  try {
    state = await request('/api/state', null, 'GET');
    render();
  } catch (error) { notice('Панель недоступна: ' + error.message, true); }
  finally { polling = false; }
}

async function refreshPlayers() {
  if (playerRefresh) return;
  playerRefresh = true;
  $('refresh-players').disabled = true;
  try {
    await request('/api/players', {});
    lastPlayerRefresh = Date.now();
    $('players-updated').textContent = 'Обновлено: ' + new Date().toLocaleTimeString('ru-RU');
    await refresh();
  } catch (error) { notice(error.message, true); }
  finally { playerRefresh = false; $('refresh-players').disabled = false; }
}

$('player-search').addEventListener('input', renderPlayers);
$('refresh-players').addEventListener('click', refreshPlayers);
$('start').addEventListener('click', () => perform(() => request('/api/start', {}), 'Бот запускается. Ход подключения виден в журнале.'));
$('stop').addEventListener('click', () => perform(() => request('/api/stop', {}), 'Бот остановлен. Панель продолжает работать.'));
$('save-all').addEventListener('click', () => perform(() => action({action:'save'})));
$('copy-logs').addEventListener('click', () => perform(async () => { await navigator.clipboard.writeText(state?.logs || ''); }, 'Журнал скопирован.'));

$('settings-form').addEventListener('submit', event => {
  event.preventDefault();
  const form = event.currentTarget;
  perform(async () => {
    await request('/api/settings', formData(form), 'PUT');
    form.elements.player_token.value = '';
    form.elements.bm_token.value = '';
    delete form.dataset.dirty;
    loadedForms = false;
  }, 'Настройки сохранены. Если бот работал, новое подключение запущено автоматически.');
});

$('track-form').addEventListener('submit', event => {
  event.preventDefault();
  const form = event.currentTarget;
  perform(async () => { await action({action:'track_add', ...formData(form)}); form.reset(); });
});

for (const slot of [1,2]) {
  $('switch-form-'+slot).addEventListener('submit', event => {
    event.preventDefault();
    const form = event.currentTarget;
    perform(async () => { await action({action:'switch_set', slot, ...formData(form)}); delete form.dataset.dirty; });
  });
}

document.querySelectorAll('.unbind').forEach(button => button.addEventListener('click', () => perform(async () => {
  const slot = Number(button.dataset.slot);
  await action({action:'switch_set', slot, id:''});
  delete $('switch-form-'+slot).dataset.dirty;
})));

document.querySelectorAll('[data-power]').forEach(button => button.addEventListener('click', () => perform(() => action({action:'switch_value', slot:Number(button.dataset.slot), value:button.dataset.power === 'on'}), 'Сервер подтвердил новое состояние переключателя.')));

$('alarm-form').addEventListener('submit', event => {
  event.preventDefault();
  const form = event.currentTarget;
  perform(async () => { await action({action:'alarm_save', ...formData(form)}); form.reset(); delete form.dataset.dirty; });
});
$('mute-alarms').addEventListener('click', () => perform(() => action({action:'alarm_mute', value:!state.bot.muted})));

async function saveDeathWatch(form, playerName = '') {
  if (deathSaving || !form.reportValidity()) return;

  const data = formData(form);
  const enabled = form.elements.enabled.checked;
  const payload = {action:'death_save', ...data, interval:Number(data.interval), enabled};
  const message = enabled
    ? `Выбран ${playerName || data.id}. ${state?.phase === 'running' ? 'Death-watch включён.' : 'Отслеживание включится при запуске бота.'}`
    : 'Death-watch выключен. Настройки сохранены.';

  deathSaving = true;
  form.querySelector('[type="submit"]').disabled = true;
  const feedback = $('death-feedback');
  feedback.className = '';
  feedback.textContent = 'Сохраняем и применяем death-watch…';
  renderTeam();

  try {
    await action(payload);
    delete form.dataset.dirty;
    feedback.textContent = message;
    notice(message);
    await refresh();
  } catch (error) {
    feedback.className = 'form-error';
    feedback.textContent = `Не удалось применить: ${error.message}`;
    notice(error.message, true);
  } finally {
    deathSaving = false;
    form.querySelector('[type="submit"]').disabled = false;
    renderTeam();
  }
}

async function selectDeathTarget(member) {
  const form = $('death-form');
  form.elements.namedItem('id').value = member.id;
  form.elements.enabled.checked = true;
  form.dataset.dirty = 'true';

  await saveDeathWatch(form, member.name);
}

function renderTeam() {
  if (!teamLoaded) return;

  list($('team-list'), teamMembers.map(member => {
    const selected = state?.bot.death?.enabled && state.bot.death.steam_id === member.id;
    const control = button(selected ? 'Отслеживается' : 'Отслеживать', () => selectDeathTarget(member));
    control.disabled = deathSaving || selected;

    return row(member.name, `${member.online ? 'Онлайн' : 'Оффлайн'} · ${member.alive ? 'Жив' : 'Мёртв'} · ${member.id}`, [control]);
  }), 'В команде нет участников.');
}

$('death-form').addEventListener('submit', event => {
  event.preventDefault();
  saveDeathWatch(event.currentTarget);
});

$('refresh-team').addEventListener('click', () => perform(async () => {
  teamMembers = await action({action:'team'});
  teamLoaded = true;
  renderTeam();
}, 'Состав команды обновлён.'));

$('probe-form').addEventListener('submit', event => {
  event.preventDefault();
  const id = formData(event.currentTarget).id;
  perform(async () => {
    const info = await action({action:'device_info', id});
    $('probe-result').textContent = `ID: ${info.id}\nТип: ${info.type}\nСостояние: ${info.value ? 'включено' : 'выключено'}\nВместимость: ${info.capacity}\nЗащита: ${info.protected ? 'есть' : 'нет'}`;
    $('probe-result').classList.remove('hidden');
  }, 'Данные устройства получены.');
});

function openPairing() { $('pairing-dialog').showModal(); }

function renderNotifications() {
  const inbox = state?.notifications;
  if (!inbox) return;

  $('listener-status').textContent = inbox.connected ? 'Слушаем Rust+' : inbox.running ? 'Подключение…' : 'Остановлен';
  $('listener-status').className = 'badge' + (inbox.connected ? ' connected' : '');
  $('listener-start').disabled = inbox.running;
  $('listener-stop').disabled = !inbox.running;
  $('listener-error').textContent = inbox.error || '';

  const signature = JSON.stringify(inbox.events);
  if (signature === notificationSignature) return;
  notificationSignature = signature;

  const cards = inbox.events.map(event => {
    const card = document.createElement('article');
    card.className = 'card';
    const title = document.createElement('h2');
    title.textContent = event.name || (event.kind === 'server' ? 'Новый сервер' : 'Новое устройство');
    const detail = document.createElement('p');
    detail.textContent = `${event.kind === 'server' ? 'Сервер' : 'Устройство '+event.entity_id} · ${new Date(event.received).toLocaleString('ru-RU')}${event.applied ? ' · Уже применено' : ''}`;
    card.append(title, detail);

    const controls = document.createElement('div');
    controls.className = 'notification-controls';
    if (event.kind === 'server') {
      const select = document.createElement('select');
      select.setAttribute('aria-label','Совпадение BattleMetrics');
      select.add(new Option('Не использовать BattleMetrics для этого сервера',''));
      for (const match of event.matches || []) select.add(new Option(`${match.name} · ${match.ip}:${match.port} · BM ${match.id}`,match.id));
      if (event.matches?.length === 1) select.value = event.matches[0].id;
      const info = document.createElement('p');
      info.textContent = event.searching ? 'Ищем точное совпадение в BattleMetrics…' : event.search_error || (event.matches?.length ? 'Проверьте совпадение сервера перед применением.' : 'Точное совпадение не найдено. Можно указать BattleMetrics ID вручную.');
      const manual = document.createElement('input');
      manual.placeholder = 'BattleMetrics ID вручную (необязательно)';
      manual.setAttribute('aria-label','BattleMetrics ID вручную');
      manual.inputMode = 'numeric';
      controls.append(select,manual,button('Применить сервер',() => perform(async () => {
        await request('/api/notifications/apply',{id:event.id,bm_id:manual.value.trim() || select.value});
        loadedForms=false;
        delete $('settings-form').dataset.dirty;
      },'Настройки сервера сохранены. Работающий бот переподключается.'),'primary'),button('Повторить поиск',() => perform(() => request('/api/notifications/search',{id:event.id}),'Поиск BattleMetrics завершён.')));
      card.append(info);
    } else {
      const select = document.createElement('select');
      select.setAttribute('aria-label','Назначение устройства');
      if (event.entity_type === '2' || /alarm/i.test(event.entity_type)) select.add(new Option('Добавить как Smart Alarm','alarm'));
      else if (event.entity_type === '1' || /switch/i.test(event.entity_type)) {
        select.add(new Option(`BT1${state.bot.bt1 ? ' — заменить текущую привязку' : ''}`,'bt1'));
        select.add(new Option(`BT2${state.bot.bt2 ? ' — заменить текущую привязку' : ''}`,'bt2'));
      }
      const apply = button('Добавить устройство',() => perform(async () => {
        await request('/api/notifications/apply',{id:event.id,target:select.value});
        for (const slot of [1,2]) delete $('switch-form-'+slot).dataset.dirty;
      }),'primary');
      apply.disabled = select.options.length === 0;
      controls.append(select,apply);
      if (event.server_id !== state.settings.server_id) {
        const note = document.createElement('p');
        note.textContent = 'Устройство относится к другому серверу. Сначала примените его сервер.';
        card.append(note);
        apply.disabled = true;
      }
    }
    card.append(controls);
    return card;
  });
  list($('notification-list'),cards,'Пока нет уведомлений. Привяжите сервер или устройство в игре.');
}

$('listener-start').addEventListener('click',() => perform(() => request('/api/listener/start',{}),'Слушатель запущен. Привяжите сервер или устройство к Rust+ в игре.'));
$('listener-stop').addEventListener('click',() => perform(() => request('/api/listener/stop',{}),'Слушатель остановлен. Сам бот продолжает работать.'));

$('import-server').addEventListener('click', openPairing);
$('import-device').addEventListener('click', openPairing);
$('close-pairing').addEventListener('click', () => $('pairing-dialog').close());
$('cancel-pairing').addEventListener('click', () => $('pairing-dialog').close());
$('pairing-form').addEventListener('submit', event => {
  event.preventDefault();
  perform(async () => {
    const parsed = await request('/api/pairing', formData(event.currentTarget));
    if (parsed.kind === 'server') {
      switchPage('settings');
      const form = $('settings-form');
      for (const key of ['server_id','player_id','player_token']) form.elements[key].value = parsed[key];
      form.dataset.dirty = 'true';
    } else {
      if (parsed.server_id && parsed.server_id.includes('-') && parsed.server_id !== state.settings.server_id) throw new Error('Устройство относится к другому серверу. Сначала измените сервер в настройках.');
      switchPage('devices');
      const isAlarm = parsed.type === '2' || /alarm/i.test(parsed.type);
      const form = isAlarm ? $('alarm-form') : $('switch-form-'+(!state.bot.bt1 ? 1 : 2));
      form.elements.id.value = parsed.id;
      form.elements.name.value = parsed.name || (isAlarm ? 'Smart Alarm' : 'Smart Switch');
      form.dataset.dirty = 'true';
    }
    $('pairing-dialog').close();
    $('pairing-form').reset();
  }, 'Форма заполнена из уведомления. Проверьте поля и сохраните настройки.');
});

request('/api/sounds', null, 'GET').then(sounds => {
  document.querySelectorAll('.sound-select').forEach(select => sounds.forEach(name => select.add(new Option(name,name))));
}).catch(error => notice(error.message, true));

refresh();
setInterval(refresh, 2000);
setInterval(() => {
  if (currentPage === 'players' && state?.phase === 'running' && state.settings.bm_server && Date.now()-lastPlayerRefresh > 30000) refreshPlayers();
}, 5000);
