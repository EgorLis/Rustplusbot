const {test} = require('node:test');
const assert = require('node:assert/strict');
const {project,columnLabel,gridLayout,gridReference,shortTimestamp} = require('../static/map.js');

const geometry = {width:3125,height:3125,ocean_margin:500,world_size:4250};

test('death labels show a short local date and time with leading zeros', () => {
  assert.equal(shortTimestamp(new Date(2026, 10, 17, 18, 7).toISOString()), '17.11 18:07');
  assert.equal(shortTimestamp(new Date(2026, 0, 2, 0, 5).toISOString()), '02.01 00:05');
  assert.equal(shortTimestamp('invalid'), 'Время неизвестно');
  assert.equal(shortTimestamp(null), 'Время неизвестно');
});

test('world coordinates use pixel ocean margins and invert the vertical axis',()=>{
  assert.deepEqual(project({x:0,y:0},geometry),{x:500,y:2625});
  assert.deepEqual(project({x:4250,y:4250},geometry),{x:2625,y:500});
  assert.deepEqual(project({x:2125,y:2125},geometry),{x:1562.5,y:1562.5});
});

test('grid cells follow the Rust client map-size formula rather than fixed 146.3 metres',()=>{
  assert.equal(gridLayout({world_size:3500}).cells,23);
  assert.deepEqual(gridLayout({world_size:4500}),{cells:30,cellSize:150});
  assert.equal(gridLayout({world_size:5000}).cells,34);
  assert.equal(gridLayout({world_size:0}),null);
});

test('column labels continue after Z and rows start at the north edge',()=>{
  assert.equal(columnLabel(0),'A');
  assert.equal(columnLabel(25),'Z');
  assert.equal(columnLabel(26),'AA');
  assert.equal(columnLabel(33),'AH');
  const g = {...geometry,world_size:5000};
  assert.equal(gridReference({x:0,y:5000},g),'A0');
  assert.equal(gridReference({x:5000,y:0},g),'AH33');
  assert.equal(gridReference({x:150,y:4999},g),'B0');
  assert.equal(gridReference({x:100,y:4800},g),'A1');
  assert.equal(gridReference({x:-1,y:2500},g),null);
});

test('image pixel padding does not change the in-game grid reference',()=>{
  const point = {x:1000,y:3000};
  assert.equal(gridReference(point,geometry),gridReference(point,{...geometry,width:4500,height:4500,ocean_margin:1000}));
});

test('missing and invalid coordinates do not draw misleading markers at zero',()=>{
  assert.equal(project({x:null,y:null},geometry),null);
  assert.equal(project({x:Infinity,y:1},geometry),null);
  assert.equal(project({x:1,y:1},{...geometry,world_size:0}),null);
});
