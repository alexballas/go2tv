import {createHash} from "node:crypto";
import {readFile,readdir,rm,writeFile} from "node:fs/promises";

const dist="internal/webui/dist",src="internal/webui/src";
for(const name of await readdir(dist))if(/^app\..*\.(css|js)$/.test(name))await rm(`${dist}/${name}`);
const hash=buffer=>createHash("sha256").update(buffer).digest("hex").slice(0,8);
for(const ext of ["css","js"]){const path=`${dist}/app.${ext}`,data=await readFile(path),name=`app.${hash(data)}.${ext}`;await writeFile(`${dist}/${name}`,data);await rm(path);globalThis[ext]=name}
let html=await readFile(`${src}/index.html`,"utf8");html=html.replace("__CSS__",globalThis.css).replace("__JS__",globalThis.js);await writeFile(`${dist}/index.html`,html);
