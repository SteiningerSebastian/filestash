/* Tiny static file server for OFFLINE theme verification (node, no deps).
 * Serves the E:\filestash repo root so that:
 *   /plugin/plg_theme_htl/verify/mockup.html  can load
 *   /public/assets/...  design system CSS   and   /plugin/plg_theme_htl/htl.css
 * Run:  node mockup-server.js   then open http://127.0.0.1:8399/plugin/plg_theme_htl/verify/mockup.html
 */
const http = require("http");
const fs = require("fs");
const path = require("path");

const ROOT = path.resolve(__dirname, "..", "..", "..", ".."); // repo root: E:\filestash (public/, server/, docker/ live here)
const PORT = 8399;

const MIME = {
    ".html": "text/html",
    ".css": "text/css",
    ".js": "application/javascript",
    ".svg": "image/svg+xml",
    ".png": "image/png",
    ".ico": "image/x-icon",
    ".json": "application/json",
    ".woff2": "font/woff2",
};

http.createServer((req, res) => {
    let urlPath = decodeURIComponent(req.url.split("?")[0]);
    // theme plugin sources live under server/plugin/, not a top-level plugin/
    urlPath = urlPath.replace(/^\/plugin\//, "/server/plugin/");
    if (urlPath === "/") urlPath = "/server/plugin/plg_theme_htl/verify/mockup.html";
    const filePath = path.join(ROOT, urlPath);
    if (!filePath.startsWith(ROOT)) {
        res.writeHead(403); res.end(); return;
    }
    fs.readFile(filePath, (err, data) => {
        if (err) { res.writeHead(404); res.end("not found: " + urlPath); return; }
        res.writeHead(200, { "Content-Type": MIME[path.extname(filePath)] || "application/octet-stream" });
        res.end(data);
    });
}).listen(PORT, "127.0.0.1", () => console.log(`mockup server on http://127.0.0.1:${PORT}/`));