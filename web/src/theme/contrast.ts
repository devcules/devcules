// WCAG contrast math for the token test. sRGB channel to linear,
// relative luminance, ratio: the standard definitions, no shortcuts.
export function luminance(r: number, g: number, b: number): number {
  const linear = (c: number): number => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
}

export function ratio(l1: number, l2: number): number {
  const [hi, lo] = l1 >= l2 ? [l1, l2] : [l2, l1];
  return (hi + 0.05) / (lo + 0.05);
}

export function parseHex(hex: string): [number, number, number] {
  const m = /^#([0-9a-fA-F]{6})$/.exec(hex.trim());
  if (!m) {
    throw new Error(`not a hex colour: ${hex}`);
  }
  const v = parseInt(m[1], 16);
  return [(v >> 16) & 255, (v >> 8) & 255, v & 255];
}

export function contrast(fg: string, bg: string): number {
  const [r1, g1, b1] = parseHex(fg);
  const [r2, g2, b2] = parseHex(bg);
  return ratio(luminance(r1, g1, b1), luminance(r2, g2, b2));
}
