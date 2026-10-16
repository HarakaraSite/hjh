// rg JSON represents non-UTF-8 names and text as base64 bytes.
function rgText(value) {
  return value.text !== undefined ? value.text : { base64: value.bytes };
}

function byteText(bytes) {
  try {
    return new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes);
  } catch {
    let binary = '';
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return { base64: btoa(binary) };
  }
}

function parseRG(bytes, add) {
  const text = new TextDecoder().decode(bytes);
  for (const line of text.split('\n')) {
    if (!line.trim()) continue;
    const event = JSON.parse(line);
    if (event.type === 'match' || event.type === 'context') {
      add({ path: rgText(event.data.path), line: event.data.line_number,
        text: rgText(event.data.lines), match: event.type === 'match' });
    }
  }
}

// Find the NUL filename boundary before looking for LF: a filename may contain LF.
function parseGNU(bytes, add) {
  let start = 0;
  while (start < bytes.length) {
    const endPath = bytes.indexOf(0, start);
    if (endPath < 0) throw new Error('GNU grep output has no filename separator');
    const name = bytes.subarray(start, endPath);
    start = endPath + 1;
    let endLine = bytes.indexOf(10, start);
    endLine = endLine < 0 ? bytes.length : endLine + 1;
    let separator = start;
    while (separator < endLine && bytes[separator] >= 48 && bytes[separator] <= 57) separator++;
    if (separator === start || separator === endLine || (bytes[separator] !== 58 && bytes[separator] !== 45)) {
      throw new Error('GNU grep output has no line number separator');
    }
    const number = Number(new TextDecoder().decode(bytes.subarray(start, separator)));
    add({ path: byteText(name), line: number,
      text: byteText(bytes.subarray(separator + 1, endLine)), match: bytes[separator] === 58 });
    start = endLine;
  }
}

export function grepPage(bytes, backend, output, offset, limit) {
  const result = { items: [], matches: 0, offset, total: 0 };
  const seen = new Set();
  function add(line) {
    if (line.match) result.matches++;
    let item = line;
    if (output === 'files') {
      if (!line.match) return;
      const key = JSON.stringify(line.path);
      if (seen.has(key)) return;
      seen.add(key);
      item = line.path;
    }
    if (result.total >= offset && result.items.length < limit) result.items.push(item);
    result.total++;
  }
  if (backend === 'rg') parseRG(bytes, add);
  else parseGNU(bytes, add);
  const next = offset + result.items.length;
  if (next < result.total) result.nextOffset = next;
  return result;
}
