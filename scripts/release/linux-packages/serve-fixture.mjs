import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { origin } from './model.mjs';

const root = '/repository';
createServer(async (request, response) => {
  try {
    const pathname = new URL(request.url, 'http://registry:8080').pathname;
    const target = path.resolve(root, `.${pathname}`);
    if (!target.startsWith(`${root}/`)) throw new Error('path');
    let bytes = await readFile(target);
    // Only unsigned locator/configuration files change authority in this isolated fixture.
    if (pathname.endsWith('/mirrorlist') || pathname.endsWith('.sources') || pathname.endsWith('.repo')) bytes = Buffer.from(bytes.toString().replaceAll(origin, 'http://registry:8080'));
    response.writeHead(200, { 'Content-Length': bytes.length }); response.end(bytes);
  } catch { response.writeHead(404); response.end(); }
}).listen(8080, '0.0.0.0');
