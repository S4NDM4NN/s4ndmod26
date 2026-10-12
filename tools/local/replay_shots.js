// Play a replay in headless Chromium and screenshot it: node replay_shots.js NAME TAG SECONDS EVERY_MS [&player=N]
// Needs T (output dir with shots/), playwright-core (npm i playwright-core) and a Playwright headless shell in ~/.cache/ms-playwright.
// Browser console output (including game/cgame prints) is written to $T/log_TAG.txt.
const { chromium } = require('playwright-core'); const fs=require('fs'), path=require('path');
(async()=>{
  const [name,tag,secs,every]=[process.argv[2],process.argv[3],+process.argv[4]||30,+process.argv[5]||500];
  const q=process.argv[6]||'';
  const exe = fs.readdirSync(path.join(process.env.HOME,'.cache/ms-playwright')).filter(d=>d.startsWith('chromium_headless_shell'))[0];
  const b=await chromium.launch({executablePath:path.join(process.env.HOME,'.cache/ms-playwright',exe,'chrome-headless-shell-linux64/chrome-headless-shell'),args:['--no-sandbox','--use-angle=swiftshader','--use-gl=angle','--enable-unsafe-swiftshader','--ignore-gpu-blocklist','--enable-webgl']});
  const p=await (await b.newContext({viewport:{width:960,height:600}})).newPage();
  const log=fs.createWriteStream(path.join(process.env.T,'log_'+tag+'.txt'));
  const t0=Date.now();
  p.on('console',m=>log.write(((Date.now()-t0)/1000).toFixed(1)+' '+m.text()+'\n'));
  await p.goto('http://localhost:8123/play/?replay='+name+q);
  for(let i=0;i*every<secs*1000;i++){ await p.waitForTimeout(every); await p.screenshot({path:path.join(process.env.T,'shots',tag+'_'+String(i).padStart(3,'0')+'.png')}); }
  await b.close(); log.end();
})();
