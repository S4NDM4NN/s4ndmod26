// Serves infra/web/nginx/html for local checks, doing what nginx does for it: the header and footer are server-side includes
// (`ssi on`), so a plain file server shows pages without them.  Handles the include directive (with ?args), if/else/endif on
// $arg_x, and /api/ -> the status API.  Usage: node serve_html.js [port] [api-port]
const http = require('http'), fs = require('fs'), path = require('path');
const ROOT = path.resolve(__dirname, '../../infra/web/nginx/html');
const PORT = +process.argv[2] || 8080, API = +process.argv[3] || 18090;
const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.txt': 'text/plain' };

function expand(html, args, depth) {
  if (depth > 5) return '<!-- include too deep -->';
  // includes first, then conditionals evaluated against this request's args
  html = html.replace(/<!--#\s*include\s+virtual="([^"]+)"\s*-->/g, (_, u) => {
    const [p, q] = u.split('?');
    const sub = Object.fromEntries(new URLSearchParams(q || ''));
    return expand(fs.readFileSync(path.join(ROOT, p), 'utf8'), sub, depth + 1);
  });
  return html.replace(/<!--#\s*if\s+expr="\$arg_(\w+)\s*=\s*(\w+)"\s*-->([\s\S]*?)(?:<!--#\s*else\s*-->([\s\S]*?))?<!--#\s*endif\s*-->/g,
    (_, k, v, a, b) => (args[k] === v ? a : (b || '')));
}

http.createServer((req, res) => {
  const u = new URL(req.url, 'http://x'), p = decodeURIComponent(u.pathname);
  if (p.startsWith('/api/')) {
    const q = http.request({ host: '127.0.0.1', port: API, path: req.url, method: req.method, headers: { ...req.headers, 'x-real-ip': req.socket.remoteAddress } },
      r => { res.writeHead(r.statusCode, r.headers); r.pipe(res); });
    q.on('error', () => { res.writeHead(502); res.end(); });
    req.pipe(q); return;
  }
  let f = path.join(ROOT, p); if (f.endsWith('/')) f += 'index.html';
  if (!f.startsWith(ROOT)) { res.writeHead(403); return res.end(); }
  fs.readFile(f, (err, buf) => {
    if (err) { res.writeHead(404); return res.end('not found'); }
    const ext = path.extname(f);
    if (ext === '.html') buf = Buffer.from(expand(buf.toString('utf8'), {}, 0));
    res.writeHead(200, { 'Content-Type': types[ext] || 'application/octet-stream', 'Cache-Control': 'no-store' });
    res.end(buf);
  });
}).listen(PORT, () => console.log('serving ' + ROOT + ' on :' + PORT));
