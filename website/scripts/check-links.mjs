import { readdir, readFile, access } from "node:fs/promises";
import { resolve, dirname, join } from "node:path";

const root = new URL("../dist/", import.meta.url).pathname;
const base = "/magnes/";
async function walk(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const nested = await Promise.all(
    entries.map((entry) =>
      entry.isDirectory() ? walk(join(dir, entry.name)) : join(dir, entry.name),
    ),
  );
  return nested.flat();
}
const files = await walk(root);
let checked = 0;
const failures = [];
for (const file of files.filter((file) => file.endsWith(".html"))) {
  const html = await readFile(file, "utf8");
  for (const match of html.matchAll(/(?:href|src)="([^"#]+)"/g)) {
    const href = match[1].replaceAll("&amp;", "&");
    if (href.startsWith("https://github.com/giorgosg/magnes/edit/")) {
      failures.push(`${file}: unwanted Edit page link: ${href}`);
    }
    if (/^(?:[a-z]+:|\/\/)/i.test(href)) continue;
    const path = decodeURIComponent(href.split(/[?#]/)[0]);
    if (!path) continue;
    if (path.startsWith("/") && !path.startsWith(base)) {
      failures.push(`${file}: URL outside Pages base: ${href}`);
      continue;
    }
    const target = path.startsWith("/")
      ? resolve(root, path.slice(base.length))
      : resolve(dirname(file), path);
    try {
      await access(
        target.endsWith("/") || !/\.[^/]+$/.test(target)
          ? join(target, "index.html")
          : target,
      );
      checked++;
    } catch {
      failures.push(`${file}: missing target: ${href}`);
    }
  }
}
if (failures.length) throw new Error(failures.join("\n"));
console.log(
  `Checked ${checked} local links and assets across ${files.filter((file) => file.endsWith(".html")).length} pages.`,
);
