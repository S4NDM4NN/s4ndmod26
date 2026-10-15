// Local stand-in for the web container: serves the wasm client from $S/play, a demo pak ($DEMOPAK), the mod pk3 from $W,
// and replays from $T/replays (NAME.rpl + NAME.txt).  Run: S=.. T=.. W=.. DEMOPAK=.. node replay_server.js  (port 8123)
const http=require('http'),fs=require('fs'),path=require('path');
const S=process.env.S, T=process.env.T, W=process.env.W;
const PLAY=path.join(S,'play');
// the mod pk3 is versioned: build_wasm.sh writes $S/dl/s4ndmod26_local.pk3 and latest.txt (names the current one)
const DL={'/downloads/main/demopak0.pk3':process.env.DEMOPAK,'/downloads/s4ndmod26/latest.txt':path.join(S,'dl/latest.txt')};
try{fs.readdirSync(path.join(S,'dl')).filter(f=>/\.pk3$/.test(f)).forEach(f=>{DL['/downloads/s4ndmod26/'+f]=path.join(S,'dl',f);});}catch(e){}
const types={'.html':'text/html','.js':'text/javascript','.wasm':'application/wasm','.txt':'text/plain'};
function send(req,res,file,extra){fs.stat(file,(err,st)=>{if(err||!st.isFile()){res.writeHead(404);res.end('nf');return;}
const h=Object.assign({'Content-Type':types[path.extname(file)]||'application/octet-stream','Accept-Ranges':'bytes'},extra||{});
const m=/bytes=(\d*)-(\d*)/.exec(req.headers.range||'');
if(m){const start=m[1]?parseInt(m[1]):0,end=m[2]?Math.min(parseInt(m[2]),st.size-1):st.size-1;h['Content-Range']=`bytes ${start}-${end}/${st.size}`;h['Content-Length']=end-start+1;res.writeHead(206,h);fs.createReadStream(file,{start,end}).pipe(res);}
else{h['Content-Length']=st.size;res.writeHead(200,h);fs.createReadStream(file).pipe(res);}});}
http.createServer((req,res)=>{const p=new URL(req.url,'http://x').pathname;
const iso={'Cross-Origin-Opener-Policy':'same-origin','Cross-Origin-Embedder-Policy':'require-corp','Cache-Control':'no-store'};
if(p==='/version.txt'){res.end('local');return;}
if(p.startsWith('/play/')){send(req,res,path.join(PLAY,p==='/play/'?'index.html':p.slice(6)),iso);return;}
if(DL[p]){send(req,res,DL[p],{'Cross-Origin-Resource-Policy':'same-origin'});return;}
if(p.startsWith('/downloads/s4ndmod26/replays/')){send(req,res,path.join(T,'replays',path.basename(p)),{'Cross-Origin-Resource-Policy':'same-origin'});return;}
res.writeHead(404);res.end('nf '+p);}).listen(8123,()=>console.log('listening'));
