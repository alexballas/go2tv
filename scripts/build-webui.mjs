import { createHash } from "node:crypto";
import { readFile, readdir, rename, rm, writeFile } from "node:fs/promises";

const distDir = "internal/webui/dist";
const sourceDir = "internal/webui/src";
const hashedAssetPattern = /^app\.[a-f0-9]{8}\.(css|js)$/;

const oldAssets = (await readdir(distDir)).filter((name) =>
  hashedAssetPattern.test(name),
);
await Promise.all(oldAssets.map((name) => rm(`${distDir}/${name}`)));

async function hashAsset(extension) {
  const sourcePath = `${distDir}/app.${extension}`;
  const data = await readFile(sourcePath);
  const hash = createHash("sha256").update(data).digest("hex").slice(0, 8);
  const name = `app.${hash}.${extension}`;
  await rename(sourcePath, `${distDir}/${name}`);
  return name;
}

const [cssAsset, jsAsset] = await Promise.all(["css", "js"].map(hashAsset));
const template = await readFile(`${sourceDir}/index.html`, "utf8");
const html = template.replace("__CSS__", cssAsset).replace("__JS__", jsAsset);
await writeFile(`${distDir}/index.html`, html);
