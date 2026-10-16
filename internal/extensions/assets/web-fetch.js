import { createWriteStream } from 'node:fs';
import path from 'node:path';
import process from 'node:process';

const maxBodyBytes = 64 * 1024;
const acceptText = 'text/*, application/json, application/xml;q=0.9, */*;q=0.1';
const userAgent = 'hjh';

// Decode valid UTF-8 sequences explicitly: Go's JSON encoding replaces each
// invalid byte, while web_fetch's strings.ToValidUTF8 replaces an invalid run.
// The stdlib TextDecoder groups some malformed sequences differently.
function decodeUTF8(bytes, collapseInvalid = false) {
  let text = '';
  let invalid = false;
  for (let i = 0; i < bytes.length;) {
    const first = bytes[i];
    let size = 0;
    let point = first;
    const continuation = j => j < bytes.length && bytes[j] >= 0x80 && bytes[j] <= 0xbf;
    if (first < 0x80) size = 1;
    else if (first >= 0xc2 && first <= 0xdf && continuation(i + 1)) {
      size = 2;
      point = ((first & 31) << 6) | (bytes[i + 1] & 63);
    } else if (first >= 0xe0 && first <= 0xef && continuation(i + 1) && continuation(i + 2) &&
      (first !== 0xe0 || bytes[i + 1] >= 0xa0) && (first !== 0xed || bytes[i + 1] < 0xa0)) {
      size = 3;
      point = ((first & 15) << 12) | ((bytes[i + 1] & 63) << 6) | (bytes[i + 2] & 63);
    } else if (first >= 0xf0 && first <= 0xf4 && continuation(i + 1) && continuation(i + 2) && continuation(i + 3) &&
      (first !== 0xf0 || bytes[i + 1] >= 0x90) && (first !== 0xf4 || bytes[i + 1] < 0x90)) {
      size = 4;
      point = ((first & 7) << 18) | ((bytes[i + 1] & 63) << 12) | ((bytes[i + 2] & 63) << 6) | (bytes[i + 3] & 63);
    }
    if (size) {
      text += String.fromCodePoint(point);
      invalid = false;
      i += size;
    } else {
      if (!collapseInvalid || !invalid) text += '\ufffd';
      invalid = true;
      i++;
    }
  }
  return text;
}

export async function http(args) {
  const response = await fetch(args.url);
  const bytes = new Uint8Array(await response.arrayBuffer());
  return { status: response.status, body: decodeUTF8(bytes) };
}

async function readBounded(body) {
  const bytes = new Uint8Array(maxBodyBytes);
  let length = 0;
  let truncated = false;
  const reader = body.getReader();
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      const keep = Math.min(value.length, maxBodyBytes - length);
      bytes.set(value.subarray(0, keep), length);
      length += keep;
      if (value.length > keep) {
        truncated = true;
        break;
      }
    }
  } finally {
    // A stream may read ahead internally, but only the first 64 KiB are kept.
    await reader.cancel();
    reader.releaseLock();
  }
  return { bytes: bytes.subarray(0, length), truncated };
}

async function saveResponse(body, filename) {
  const reader = body.getReader();
  let writer;
  let bytes = 0;
  try {
    writer = createWriteStream(filename).getWriter();
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      await writer.write(value);
      bytes += value.length;
    }
    await writer.close();
    return bytes;
  } catch (error) {
    if (writer) await writer.abort();
    throw error;
  } finally {
    await reader.cancel();
    reader.releaseLock();
    if (writer) writer.releaseLock();
  }
}

function isTextual(contentType) {
  const type = contentType.toLowerCase();
  return type.startsWith('text/') || type.startsWith('application/json') ||
    type.startsWith('application/xml') || type.startsWith('application/xhtml+xml') ||
    type.includes('+json') || type.includes('+xml');
}

function htmlToText(html) {
  const entities = { '&nbsp;': ' ', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&#39;': "'", '&amp;': '&' };
  let text = html.replace(/<script\b[^>]*>[\s\S]*?<\/script>/gi, ' ')
    .replace(/<style\b[^>]*>[\s\S]*?<\/style>/gi, ' ')
    .replace(/<!--[\s\S]*?-->/g, ' ')
    .replace(/<\/(p|div|section|article|header|footer|li|tr|h[1-6]|blockquote|pre)>/gi, '\n')
    .replace(/<br[ \t\r\n\f\v]*\/?>/gi, '\n')
    .replace(/<[^>]+>/g, ' ')
    .replace(/&nbsp;|&lt;|&gt;|&quot;|&#39;|&amp;/g, entity => entities[entity]);
  const lines = text.split('\n').map(line => line.replace(/[ \t\f\v]+/g, ' ').trim());
  text = lines.filter((line, i) => line !== '' || (i > 0 && lines[i - 1] !== '')).join('\n');
  return text.replace(/\n{3,}/g, '\n\n').trim();
}

export async function webFetch(args) {
  const url = args.url.trim();
  const saveTo = args.save_to;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 30_000);
  let response;
  try {
    response = await fetch(url, {
      signal: controller.signal,
      headers: { Accept: saveTo ? '*/*' : acceptText, 'User-Agent': userAgent },
    }).catch(error => { throw new Error('web_fetch request failed: ' + error.message); });
    const finalURL = response.url || url;
    const contentType = response.headers.get('content-type') || '';
    if (!response.ok) {
      const operation = saveTo ? 'download failed for ' + saveTo : 'request failed';
      throw new Error(`web_fetch ${operation} (${response.status}) for ${finalURL}`);
    }
    if (saveTo) {
      const bytes = await saveResponse(response.body, path.resolve(process.cwd(), saveTo))
        .catch(error => { throw new Error(`web_fetch download failed for ${saveTo}: ${error.message}`); });
      return [`Saved: ${saveTo}`, `URL: ${finalURL}`, `Status: ${response.status}`,
        `Content-Type: ${contentType || 'unknown'}`, `Bytes: ${bytes}`].join('\n');
    }
    const { bytes, truncated } = await readBounded(response.body)
      .catch(error => { throw new Error('web_fetch request failed: ' + error.message); });
    const metadata = [`URL: ${finalURL}`, `Status: ${response.status}`,
      `Content-Type: ${contentType || 'unknown'}`, `truncated: ${truncated}`].join('\n');
    if (!isTextual(contentType)) return metadata;
    let text = decodeUTF8(bytes, true).replace(/^\ufeff/, '');
    if (contentType.toLowerCase().includes('text/html')) text = htmlToText(text);
    return metadata + '\n\n' + text + (truncated ? '\n\n[body truncated]' : '');
  } finally {
    clearTimeout(timer);
    if (response && !response.body.locked) await response.body.cancel();
  }
}
