const encoder = new TextEncoder();

// filepath.Match on Linux uses byte literals/stars and UTF-8 runes for ?/classes.
function runeAt(bytes, at) {
  const first = bytes[at];
  if (first < 0x80) return [first, 1];
  const size = first >= 0xc2 && first <= 0xdf ? 2
    : first >= 0xe0 && first <= 0xef ? 3
    : first >= 0xf0 && first <= 0xf4 ? 4 : 0;
  if (!size || at + size > bytes.length) return [0xfffd, 1];
  let rune = first & (0x7f >> size);
  for (let i = 1; i < size; i++) {
    const byte = bytes[at + i];
    if ((byte & 0xc0) !== 0x80) return [0xfffd, 1];
    rune = (rune << 6) | (byte & 0x3f);
  }
  if ((size === 3 && rune < 0x800) || (size === 4 && rune < 0x10000)
      || rune > 0x10ffff || (rune >= 0xd800 && rune <= 0xdfff)) return [0xfffd, 1];
  return [rune, size];
}

function segmentMatcher(pattern) {
  const chars = [...pattern];
  const tokens = [];
  const badPattern = () => { throw new Error('syntax error in pattern'); };
  let i = 0;
  function classCharacter() {
    if (i >= chars.length || chars[i] === '-' || chars[i] === ']') badPattern();
    if (chars[i] === '\\' && ++i >= chars.length) badPattern();
    const rune = chars[i++].codePointAt(0);
    if (i === chars.length) badPattern();
    return rune;
  }
  while (i < chars.length) {
    const char = chars[i++];
    if (char === '*') tokens.push({ star: true });
    else if (char === '?') tokens.push({ any: true });
    else if (char === '[') {
      const negated = chars[i] === '^';
      if (negated) i++;
      const ranges = [];
      do {
        const low = classCharacter();
        let high = low;
        if (chars[i] === '-') {
          i++;
          high = classCharacter();
        }
        ranges.push([low, high]);
      } while (chars[i] !== ']');
      i++;
      tokens.push({ ranges, negated });
    } else {
      let literal = char;
      if (char === '\\') {
        if (i === chars.length) badPattern();
        literal = chars[i++];
      }
      for (const byte of encoder.encode(literal)) tokens.push({ byte });
    }
  }
  return name => {
    const bytes = encoder.encode(name);
    const memo = new Map();
    function match(t, n) {
      const key = `${t}:${n}`;
      if (memo.has(key)) return memo.get(key);
      let matched;
      if (t === tokens.length) matched = n === bytes.length;
      else {
        const token = tokens[t];
        if (token.star) matched = match(t + 1, n) || (n < bytes.length && match(t, n + 1));
        else if (n === bytes.length) matched = false;
        else if (token.byte !== undefined) matched = token.byte === bytes[n] && match(t + 1, n + 1);
        else {
          const [rune, size] = runeAt(bytes, n);
          const accepts = token.any || (token.ranges.some(([low, high]) => low <= rune && rune <= high) !== token.negated);
          matched = accepts && match(t + 1, n + size);
        }
      }
      memo.set(key, matched);
      return matched;
    }
    return match(0, 0);
  };
}

// Slashless patterns match basenames; a whole ** path segment spans zero or more segments.
export function compileGlob(pattern) {
  const parts = pattern.split('/');
  const matchers = parts.map(segmentMatcher);
  if (parts.length === 1) return name => matchers[0](name.slice(name.lastIndexOf('/') + 1));
  return name => {
    const names = name.split('/');
    const memo = new Map();
    function match(p, n) {
      const key = `${p}:${n}`;
      if (memo.has(key)) return memo.get(key);
      const matched = p === parts.length ? n === names.length
        : parts[p] === '**' ? match(p + 1, n) || (n < names.length && match(p, n + 1))
        : n < names.length && matchers[p](names[n]) && match(p + 1, n + 1);
      memo.set(key, matched);
      return matched;
    }
    return match(0, 0);
  };
}
