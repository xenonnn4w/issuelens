import {build} from 'esbuild';
import {readFile, writeFile} from 'node:fs/promises';

await build({entryPoints:['src/architecture-theme.ts'], bundle:true, format:'iife', target:'es2022', minify:true, outfile:'out/static/architecture-theme.js'});

const result = await build({
  entryPoints:['src/app.ts'], bundle:true, splitting:true, format:'esm',
  target:'es2022', minify:true, outdir:'out/static/build',
  entryNames:'[name]-[hash]', chunkNames:'chunk-[hash]', metafile:true,
});
const entry = Object.entries(result.metafile.outputs).find(([, output]) => output.entryPoint === 'src/app.ts');
if (!entry) throw new Error('Application entry point missing');
const html = await readFile('out/index.html','utf8');
const script = `<script type="module" src="/${entry[0].replace(/^out\//,'')}"></script>`;
const updated = html.replace(/<script\b[^>]*src="\/static\/(?:app\.js[^"\s]*|build\/app-[^"\s]+)"[^>]*><\/script>/,script);
if (updated === html && !html.includes(script)) throw new Error('Application script tag missing');
await writeFile('out/index.html',updated);
console.log(`Application entry: ${entry[0]} (${entry[1].bytes} bytes)`);
