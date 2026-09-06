/**
 * 开发预览服务器：构建 dist/ 后启动静态服务，支持 --port / --host / --watch。
 * 用法：npm run dev [-- --port 7100 --watch]
 */
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const DIST = path.join(ROOT, 'dist');

const args = process.argv.slice(2);
const opt = (name, dflt) => {
  const i = args.indexOf('--' + name);
  return i >= 0 ? args[i + 1] : dflt;
};
const PORT = Number(opt('port', '7100'));
const HOST = opt('host', '127.0.0.1');
const WATCH = args.includes('--watch');

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.png': 'image/png',
  '.jpg': 'image/jpeg',
  '.svg': 'image/svg+xml',
  '.ico': 'image/x-icon',
  '.woff2': 'font/woff2',
};

function runBuild() {
  return new Promise((resolve, reject) => {
    // build.mjs 以仓库根目录为基准解析 esbuild entryPoint（metafile 相对 cwd），
    // 因此子进程必须在仓库根目录运行，否则入口名校验会报 missing build entry。
    const p = spawn(process.execPath, [path.join(ROOT, 'scripts/build.mjs')], {
      cwd: path.dirname(ROOT),
      stdio: 'inherit',
    });
    p.on('exit', (code) => (code === 0 ? resolve() : reject(new Error('build failed: ' + code))));
  });
}

function previewHub() {
  return `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>AI-CRM 新壳预览</title><style>body{font-family:-apple-system,'PingFang SC',sans-serif;background:#F5F6F7;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}main{width:min(680px,92vw)}h1{font-size:22px;color:#1F2329;margin:0 0 6px}p.sub{font-size:13px;color:#8F959E;margin:0 0 24px}a.card{display:block;background:#fff;border:1px solid #DEE0E3;border-radius:12px;padding:18px 20px;margin:0 0 12px;text-decoration:none;color:#1F2329;transition:box-shadow .15s}a.card:hover{box-shadow:0 8px 24px rgba(15,23,42,.08)}a.card b{font-size:15px}a.card span{display:block;font-size:12px;color:#646A73;margin-top:4px}p.tip{font-size:12px;color:#BBBFC4;margin-top:20px}</style></head><body><main><h1>AI-CRM 新壳预览</h1><p class="sub">三端统一新壳 · 静态预览（数据区在无后端时展示空态/错误态，属预期）</p><a class="card" href="/admin/"><b>管理后台</b><span>40 屏 · /admin/（默认进入客户列表，左侧导航切换全部页面）</span></a><a class="card" href="/sidebar/"><b>企微侧边栏</b><span>8 个功能 Tab · /sidebar/</span></a><a class="card" href="/h5/"><b>用户端 H5</b><span>12 屏 · /h5/（索引页列出全部屏幕）</span></a><a class="card" href="/member-grid-share/"><b>会员网格公开分享页</b><span>/member-grid-share/</span></a><p class="tip">本页仅存在于本地开发预览（web/scripts/dev.mjs），不进入生产构建产物。</p></main></body></html>`;
}

function serve() {
  const server = http.createServer((req, res) => {
    const url = decodeURIComponent((req.url || '/').split('?')[0]);
    if (url === '/') {
      res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      res.end(previewHub());
      return;
    }
    // 纯静态预览没有后端：API 请求返回 JSON 错误体而不是 HTML 404，
    // 让前端 transport 走标准失败语义，页面展示干净的中文空态/错误提示。
    if (url.startsWith('/api/') || url.startsWith('/static/')) {
      res.writeHead(404, { 'Content-Type': 'application/json; charset=utf-8' });
      res.end(JSON.stringify({ error: 'preview_no_backend', message: '本地预览未连接后端，数据区为空态/错误态属预期' }));
      return;
    }
    let file = path.join(DIST, url);
    if (!file.startsWith(DIST)) {
      res.writeHead(403).end('Forbidden');
      return;
    }
    if (fs.existsSync(file) && fs.statSync(file).isDirectory()) {
      file = path.join(file, 'index.html');
    }
    if (!fs.existsSync(file)) {
      res.writeHead(404).end('Not Found: ' + url);
      return;
    }
    res.writeHead(200, { 'Content-Type': MIME[path.extname(file)] || 'application/octet-stream' });
    fs.createReadStream(file).pipe(res);
  });
  server.listen(PORT, HOST, () => {
    console.log(`\n▶ AI-CRM 预览服务  http://${HOST}:${PORT}/`);
    console.log(`  管理后台  http://${HOST}:${PORT}/admin/customers.html`);
    console.log(`  企微侧边栏 http://${HOST}:${PORT}/sidebar/`);
    console.log(`  用户端 H5 http://${HOST}:${PORT}/h5/\n`);
  });
}

async function main() {
  await runBuild();
  serve();
  if (WATCH) {
    let timer = null;
    fs.watch(path.join(ROOT, 'src'), { recursive: true }, () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        console.log('· src 变动，重新构建…');
        runBuild().then(() => console.log('✓ 已重建')).catch((e) => console.error(e.message));
      }, 200);
    });
    console.log('watch 模式已开启（监听 src/）');
  }
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
