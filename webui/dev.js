// Dev server for `public/`, with the one thing real paths require: any unmatched path
// serves index.html, so /torrent/<hash> survives a refresh. In production that is the
// web server's job (and eventually the proxy's).

const http = require("http");
const fs = require("fs");
const path = require("path");

const root = path.join(__dirname, "public");
const port = Number(process.env.PORT || 8000);

const types = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
};

http
  .createServer((req, res) => {
    const requested = decodeURIComponent(new URL(req.url, "http://x").pathname);
    const candidate = path.join(root, requested);
    const exists =
      candidate.startsWith(root) && fs.existsSync(candidate) && fs.statSync(candidate).isFile();

    // A missing path with an extension is a missing asset, and must 404 — serving the
    // app for it would hand back HTML to a <script> tag. Only extensionless paths are
    // routes, and those are the ones that fall back so a deep link survives a refresh.
    // config.js is the case that matters: it is optional, and its absence is normal.
    if (!exists && path.extname(requested)) {
      res.writeHead(404, { "content-type": "text/plain" });
      res.end("Not found\n");
      return;
    }

    const file = exists ? candidate : path.join(root, "index.html");

    res.writeHead(200, {
      "content-type": types[path.extname(file)] || "application/octet-stream",
      "cache-control": "no-store",
    });
    fs.createReadStream(file).pipe(res);
  })
  .listen(port, () => console.log(`magnes dev server on http://localhost:${port}`));
