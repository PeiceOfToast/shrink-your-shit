export function formatCount(n: number): string {
  return n.toLocaleString("en-US");
}

export function formatGB(n: number): string {
  return `${n.toFixed(1)} GB`;
}

export function formatBytes(bytes: number): string {
  const gb = bytes / 1e9;
  return gb >= 100 ? `${Math.round(gb)} GB` : `${gb.toFixed(0)} GB`;
}

export function formatPct(n: number): string {
  return `${n} %`;
}
