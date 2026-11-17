'use strict';

// Use the existing registered FCM device. No registration or credentials are printed.
const fs = require('node:fs');
const send = value => process.stdout.write(JSON.stringify(value) + '\n');
console.log = () => {};
console.error = () => {};

let client;
process.on('uncaughtException', () => { send({event:'error', message:'FCM listener failed; verify registration and network access.'}); process.exit(1); });
process.on('unhandledRejection', () => { send({event:'error', message:'FCM connection failed; verify registration and network access.'}); process.exit(1); });
process.on('SIGTERM', () => { client?.destroy(); process.exit(0); });

async function main() {
  const Client = require(process.argv[1]);
  const config = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'));
  const credentials = config.fcm_credentials?.gcm;
  if (!credentials?.androidId || !credentials?.securityToken) {
    send({event:'error', message:'Saved FCM credentials are missing. Complete Rust+ registration first.'});
    process.exit(1);
  }

  client = new Client(String(credentials.androidId), String(credentials.securityToken), []);
  client.on('connect', () => send({event:'connected'}));
  client.on('disconnect', () => send({event:'disconnected'}));
  client.on('ON_DATA_RECEIVED', data => {
    if (String(data.from) !== '976529667804') return;
    const body = data.appData?.find(entry => entry.key === 'body')?.value;
    // Keep the body as a string: parsing it in JS could round numeric 64-bit IDs.
    if (typeof body === 'string') send({event:'notification', key:data.persistentId || '', payload:body});
  });

  await client.connect();
}

main();
