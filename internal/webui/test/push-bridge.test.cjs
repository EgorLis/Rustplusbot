const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const {EventEmitter} = require('node:events');

test('the FCM bridge filters other senders and forwards raw pairing JSON without rounding IDs',async()=>{
  const lines=[];
  const body='{"type":"entity","entityId":18446744073709551614}';
  class Client extends EventEmitter {
    async connect(){
      this.emit('connect');
      this.emit('ON_DATA_RECEIVED',{from:'other-app',appData:[{key:'body',value:'secret from other app'}]});
      this.emit('ON_DATA_RECEIVED',{from:'976529667804',persistentId:'one',appData:[{key:'body',value:body}]});
    }
  }
  const context=vm.createContext({
    require:name=>name==='node:fs'?{readFileSync:()=>JSON.stringify({fcm_credentials:{gcm:{androidId:'123',securityToken:'private'}}})}:Client,
    console:{},
    process:{argv:['node','client','config'],stdout:{write:line=>lines.push(JSON.parse(line))},on(){},exit(){throw new Error('unexpected exit');}}
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname,'../push-bridge.cjs'),'utf8'),context);
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(lines.length,2);
  assert.equal(lines[1].payload,body);
  assert.equal(JSON.stringify(lines).includes('private'),false);
});
