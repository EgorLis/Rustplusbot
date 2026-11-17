(function(root) {
  'use strict';

  function project(point, geometry) {
    if (!Number.isFinite(point.x) || !Number.isFinite(point.y) || !geometry.world_size || geometry.width <= 2*geometry.ocean_margin || geometry.height <= 2*geometry.ocean_margin) return null;
    // ocean_margin is in image pixels; team coordinates are metres from the south-west corner.
    return {
      x: geometry.ocean_margin + point.x / geometry.world_size * (geometry.width-2*geometry.ocean_margin),
      y: geometry.height-geometry.ocean_margin - point.y / geometry.world_size * (geometry.height-2*geometry.ocean_margin)
    };
  }

  function columnLabel(index) {
    let label = '';
    for (let number = index + 1; number > 0; number = Math.floor((number-1)/26)) {
      label = String.fromCharCode(65+(number-1)%26) + label;
    }
    return label;
  }

  function gridLayout(geometry) {
    const size = geometry.world_size;
    if (!Number.isFinite(size) || size <= 0) return null;
    // Rust client grid sizing: https://commits.facepunch.com/504506
    const cells = Math.max(1,Math.floor(size*7/1024));
    return {cells,cellSize:size/cells};
  }

  function gridReference(point, geometry) {
    const layout = gridLayout(geometry);
    if (!layout || !Number.isFinite(point.x) || !Number.isFinite(point.y) || point.x < 0 || point.y < 0 || point.x > geometry.world_size || point.y > geometry.world_size) return null;
    const column = Math.min(layout.cells-1,Math.floor(point.x/layout.cellSize));
    const row = Math.min(layout.cells-1,Math.floor((geometry.world_size-point.y)/layout.cellSize));
    return columnLabel(column)+row;
  }

  function shortTimestamp(value) {
    if (!value) return 'Время неизвестно';
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return 'Время неизвестно';
    const pad = number => String(number).padStart(2, '0');
    return `${pad(date.getDate())}.${pad(date.getMonth() + 1)} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
  }

  if (typeof module !== 'undefined' && module.exports) { module.exports = {project,columnLabel,gridLayout,gridReference,shortTimestamp}; return; }

  const get = id => document.getElementById(id);
  const svg = get('rust-map');
  const layer = get('map-markers');
  const gridLayer = get('map-grid');
  let geometry = null;
  let snapshot = null;
  let imageKey = '';
  let view = null;
  let drag = null;
  let loading = false;
  let gridSignature = '';
  const namespace = 'http://www.w3.org/2000/svg';

  function shape(tag, attributes, text) {
    const element = document.createElementNS(namespace,tag);
    for (const [name,value] of Object.entries(attributes)) element.setAttribute(name,String(value));
    if (text !== undefined) element.textContent = text;
    return element;
  }

  function position(point) {
    const result = project(point,geometry);
    if (!result || result.x < 0 || result.y < 0 || result.x > geometry.width || result.y > geometry.height) return null;
    return result;
  }

  function drawGrid() {
    const enabled = get('map-grid-enabled').checked;
    const signature = geometry && view ? [geometry.key,geometry.width,geometry.height,geometry.ocean_margin,geometry.world_size,view.w,view.h,enabled].join('/') : '';
    if (signature === gridSignature) return;
    gridSignature = signature;

    gridLayer.replaceChildren();
    if (!geometry || !view || !enabled) return;

    const {cells} = gridLayout(geometry);
    const margin = geometry.ocean_margin;
    const dx = (geometry.width-2*margin)/cells;
    const dy = (geometry.height-2*margin)/cells;
    // Retain the world grid while panning; SVG clips it without rebuilding its labels.
    const left = 0;
    const right = geometry.width;
    const top = 0;
    const bottom = geometry.height;
    const lineStyle = {stroke:'#e3e9d8','stroke-opacity':.46,'stroke-width':.7,'vector-effect':'non-scaling-stroke'};

    // Continue the pitch through the image's ocean padding without giving it land-cell labels.
    for (let column = Math.ceil((left-margin)/dx); column <= Math.floor((right-margin)/dx); column++) {
      const x = margin+column*dx;
      gridLayer.append(shape('line',{x1:x,y1:top,x2:x,y2:bottom,...lineStyle}));
    }
    for (let row = Math.ceil((top-margin)/dy); row <= Math.floor((bottom-margin)/dy); row++) {
      const y = margin+row*dy;
      gridLayer.append(shape('line',{x1:left,y1:y,x2:right,y2:y,...lineStyle}));
    }

    const fontSize = Math.min(view.w/85,Math.min(dx,dy)*.32);
    for (let column = Math.max(0,Math.floor((left-margin)/dx)); column < Math.min(cells,Math.ceil((right-margin)/dx)); column++) {
      for (let row = Math.max(0,Math.floor((top-margin)/dy)); row < Math.min(cells,Math.ceil((bottom-margin)/dy)); row++) {
        gridLayer.append(shape('text',{
          x:margin+column*dx+dx*.06,
          y:margin+row*dy+fontSize*1.15,
          fill:'#ecf0df','fill-opacity':.86,'font-size':fontSize,'font-family':'Segoe UI, sans-serif',
          'paint-order':'stroke',stroke:'#14201c','stroke-width':fontSize*.14
        },columnLabel(column)+row));
      }
    }
  }

  function drawMarkers() {
    layer.replaceChildren();
    if (!geometry || !view) return;
    const radius = view.w / 160;
    const fontSize = view.w / 70;

    for (const member of snapshot.map.team) {
      const p = position(member);
      if (!p) continue;
      const group = shape('g',{});
      group.append(shape('circle',{cx:p.x,cy:p.y,r:radius,fill:member.online ? '#b2e38f' : '#9babb3',stroke:'#11181a','stroke-width':radius/3}));
      group.append(shape('text',{x:p.x+radius*1.6,y:p.y+fontSize/3,fill:'#f6fff1','font-size':fontSize,'paint-order':'stroke',stroke:'#11181a','stroke-width':fontSize/4},member.name));
      group.append(shape('title',{},`${member.name} · ${gridReference(member,geometry) || 'за пределами сетки'} · ${member.alive ? 'жив' : 'мёртв'} · ${member.id}`));
      layer.append(group);
    }

    for (const death of snapshot.map.deaths) {
      const p = position(death);
      if (!p) continue;
      const group = shape('g',{});
      group.append(shape('circle',{cx:p.x,cy:p.y,r:radius*1.4,fill:'#592d2a',stroke:'#ff9682','stroke-width':radius/3}));
      group.append(shape('text',{x:p.x,y:p.y+fontSize*.35,fill:'#ffc1ad','font-size':fontSize*1.2,'text-anchor':'middle'},'×'));
      group.append(shape('text', {
        x: p.x + radius * 1.9,
        y: p.y + fontSize * .3,
        fill: '#ffd1c3',
        'font-size': fontSize * .8,
        'font-family': 'Segoe UI, sans-serif',
        'paint-order': 'stroke',
        stroke: '#11181a',
        'stroke-width': fontSize * .22,
        'stroke-linejoin': 'round'
      }, `${death.name || death.player_id || 'Неизвестный игрок'} · ${shortTimestamp(death.at)}`));
      group.append(shape('title',{},`${death.name} · ${gridReference(death,geometry) || 'за пределами сетки'} · ${new Date(death.at).toLocaleString('ru-RU')} · ${death.estimated ? 'последняя известная позиция' : 'позиция Rust+ при обнаружении смерти'}`));
      layer.append(group);
    }
  }

  function applyView() {
    svg.setAttribute('viewBox',`${view.x} ${view.y} ${view.w} ${view.h}`);
    drawGrid();
    drawMarkers();
  }

  function reset() {
    if (!geometry) return;
    view = {x:0,y:0,w:geometry.width,h:geometry.height};
    applyView();
  }

  function zoom(factor, x = .5, y = .5) {
    if (!geometry || !view) return;
    const w = Math.max(geometry.width/8,Math.min(geometry.width,view.w/factor));
    const h = w*geometry.height/geometry.width;
    view.x = Math.max(0,Math.min(geometry.width-w,view.x+(view.w-w)*x));
    view.y = Math.max(0,Math.min(geometry.height-h,view.y+(view.h-h)*y));
    view.w = w;
    view.h = h;
    applyView();
  }

  function update(data) {
    if (!data.map) return;
    snapshot = data;
    get('load-map').disabled = loading || !data.runtime.connected;
    get('map-message').textContent = loading ? 'Загружаем изображение карты с сервера…' : data.map.error || (data.map.loaded ? 'Карта Rust+ загружена. Наведите курсор на метку для подробностей.' : 'Подключите бота и нажмите «Загрузить карту Rust+».');

    if (data.map.loaded) {
      geometry = data.map;
      if (imageKey !== geometry.key) {
        imageKey = geometry.key;
        const image = get('map-image');
        image.setAttribute('href','/api/map/image?key='+encodeURIComponent(imageKey));
        image.setAttribute('width',geometry.width);
        image.setAttribute('height',geometry.height);
        reset();
      } else drawMarkers();
    } else {
      geometry = null;
      imageKey = '';
      get('map-image').removeAttribute('href');
      layer.replaceChildren();
      gridLayer.replaceChildren();
      gridSignature = '';
      get('map-grid-position').textContent = '';
    }

    const watched = new Set(data.bot.map_watch || []);
    const deathID = data.bot.death?.enabled ? data.bot.death.steam_id : '';
    list(get('map-team'),data.map.team.map(member => {
      const checkbox = document.createElement('input');
      checkbox.type = 'checkbox';
      checkbox.checked = watched.has(member.id) || member.id === deathID;
      checkbox.disabled = member.id === deathID;
      checkbox.setAttribute('aria-label',`Метки смерти: ${member.name}`);
      checkbox.addEventListener('change',() => perform(() => action({action:'map_watch',id:member.id,enabled:checkbox.checked}),'Выбор игроков для меток смерти сохранён.'));
      return row(member.name,`${member.online ? 'Онлайн' : 'Оффлайн'} · ${member.id}${member.id === deathID ? ' · Death-watch' : ''}`,[checkbox]);
    }),'Состав команды появится после подключения.');

    list(get('map-deaths'),data.map.deaths.map(death => row(death.name,
      `${shortTimestamp(death.at)} · ${death.x === null || death.y === null ? 'координаты недоступны' : death.estimated ? 'последняя известная позиция' : 'позиция при обнаружении смерти'}`
    )),'Новых смертей выбранных игроков пока не наблюдали.');
  }

  get('load-map').addEventListener('click',() => perform(async () => {
    loading = true;
    get('load-map').disabled = true;
    get('map-message').textContent = 'Загружаем изображение карты с сервера…';
    try {
      await request('/api/map/load',{});
      imageKey = '';
    } finally { loading = false; get('load-map').disabled = false; }
  },'Карта Rust+ загружена.'));
  get('map-zoom-in').addEventListener('click',() => zoom(1.5));
  get('map-zoom-out').addEventListener('click',() => zoom(1/1.5));
  get('map-reset').addEventListener('click',reset);
  get('map-grid-enabled').addEventListener('change',drawGrid);
  svg.addEventListener('wheel',event => {
    if (!geometry) return;
    event.preventDefault();
    const rect = svg.getBoundingClientRect();
    zoom(event.deltaY < 0 ? 1.2 : 1/1.2,(event.clientX-rect.left)/rect.width,(event.clientY-rect.top)/rect.height);
  },{passive:false});
  svg.addEventListener('pointerdown',event => {
    if (!view) return;
    drag = {x:event.clientX,y:event.clientY,vx:view.x,vy:view.y};
    svg.setPointerCapture(event.pointerId);
  });
  svg.addEventListener('pointermove',event => {
    if (!view || !geometry) return;

    const transform = svg.getScreenCTM();
    if (transform) {
      const cursor = svg.createSVGPoint();
      cursor.x = event.clientX;
      cursor.y = event.clientY;
      const pixel = cursor.matrixTransform(transform.inverse());
      const world = {
        x:(pixel.x-geometry.ocean_margin)/(geometry.width-2*geometry.ocean_margin)*geometry.world_size,
        y:(geometry.height-geometry.ocean_margin-pixel.y)/(geometry.height-2*geometry.ocean_margin)*geometry.world_size
      };
      const cell = gridReference(world,geometry);
      get('map-grid-position').textContent = cell ? `Клетка ${cell} · X ${Math.round(world.x)}, Y ${Math.round(world.y)}` : 'За пределами игровой сетки';
    }

    if (!drag) return;
    const rect = svg.getBoundingClientRect();
    view.x = Math.max(0,Math.min(geometry.width-view.w,drag.vx-(event.clientX-drag.x)*view.w/rect.width));
    view.y = Math.max(0,Math.min(geometry.height-view.h,drag.vy-(event.clientY-drag.y)*view.h/rect.height));
    applyView();
  });
  svg.addEventListener('pointerup',() => {drag=null;});
  svg.addEventListener('pointercancel',() => {drag=null;});
  root.RustMap = {update,project,gridLayout,gridReference};
})(globalThis);
