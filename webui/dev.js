// Same-origin HTTPS development server. It serves public/, falls back to index.html for
// Elm routes, and proxies /graphql to bitmagnet so its Secure, SameSite browser cookie is
// exercised under the same origin used in production.

const childProcess = require("child_process");
const fs = require("fs");
const http = require("http");
const https = require("https");
const path = require("path");

const root = path.join(__dirname, "public");
const port = Number(process.env.PORT || 8000);
const upstream = new URL(process.env.BITMAGNET_URL || "http://localhost:3333");
const certificateDirectory = path.join(__dirname, ".dev");
const certificatePath = process.env.MAGNES_DEV_CERT || path.join(certificateDirectory, "localhost.pem");
const keyPath = process.env.MAGNES_DEV_KEY || path.join(certificateDirectory, "localhost-key.pem");

const types = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
};

function ensureCertificate() {
  if (fs.existsSync(certificatePath) && fs.existsSync(keyPath)) return;

  if (process.env.MAGNES_DEV_CERT || process.env.MAGNES_DEV_KEY) {
    throw new Error("MAGNES_DEV_CERT and MAGNES_DEV_KEY must both name existing files");
  }

  fs.mkdirSync(certificateDirectory, { recursive: true });
  childProcess.execFileSync(
    "openssl",
    [
      "req",
      "-x509",
      "-newkey",
      "rsa:2048",
      "-nodes",
      "-days",
      "30",
      "-subj",
      "/CN=localhost",
      "-addext",
      "subjectAltName=DNS:localhost,IP:127.0.0.1",
      "-keyout",
      keyPath,
      "-out",
      certificatePath,
    ],
    { stdio: "ignore" },
  );
}

function proxyGraphql(req, res) {
  const transport = upstream.protocol === "https:" ? https : http;
  const target = new URL("/graphql", upstream);
  const proxy = transport.request(
    {
      protocol: target.protocol,
      hostname: target.hostname,
      port: target.port,
      path: target.pathname + (req.url.includes("?") ? req.url.slice(req.url.indexOf("?")) : ""),
      method: req.method,
      headers: req.headers,
    },
    (response) => {
      res.writeHead(response.statusCode || 502, response.headers);
      response.pipe(res);
    },
  );

  proxy.on("error", (error) => {
    res.writeHead(502, { "content-type": "text/plain; charset=utf-8" });
    res.end(`Could not reach bitmagnet: ${error.message}\n`);
  });
  req.pipe(proxy);
}

function serve(req, res) {
    const requestUrl = new URL(req.url, "https://localhost");
    if (requestUrl.pathname === "/graphql") {
      proxyGraphql(req, res);
      return;
    }

    const requested = decodeURIComponent(requestUrl.pathname);
    const candidate = path.join(root, requested);
    // Compare against root + separator, not root: a bare prefix test also accepts a
    // sibling directory whose name merely starts with "public". Decoding happens after
    // the URL parser has normalized the path, so %2e%2e%2f survives to reach path.join
    // and dot segments are a live input here rather than a theoretical one.
    const inside = candidate === root || candidate.startsWith(root + path.sep);
    const exists = inside && fs.existsSync(candidate) && fs.statSync(candidate).isFile();

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
}

ensureCertificate();
https
  .createServer(
    { cert: fs.readFileSync(certificatePath), key: fs.readFileSync(keyPath) },
    serve,
  )
  .listen(port, () => {
    console.log(`magnes dev server on https://localhost:${port}`);
    console.log(`proxying /graphql to ${new URL("/graphql", upstream)}`);
  });
