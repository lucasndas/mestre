/** How many terminals draw with WebGL at once (docs/painel/canvas.md#terminais-no-canvas). */
export const MAX_WEBGL_TERMINALS = 8;

/**
 * The registry of the terminals that may draw with WebGL: a browser keeps a few GPU
 * contexts, so only `limit` terminals at a time get one; the others, and a terminal
 * that is off screen or far in the zoom, show a snapshot. A terminal that leaves
 * frees its place for the first one waiting.
 */
export class WebglBudget {
  private active = new Set<string>();
  private waiting: string[] = [];
  private callbacks = new Map<string, (live: boolean) => void>();

  constructor(readonly limit = MAX_WEBGL_TERMINALS) {}

  /** How many hold a place now. */
  get used(): number {
    return this.active.size;
  }

  /** Asks for a place; tell is called now and whenever the answer changes. Undo with release. */
  request(id: string, tell: (live: boolean) => void) {
    this.callbacks.set(id, tell);
    if (this.active.has(id)) return tell(true);
    if (this.active.size < this.limit) {
      this.active.add(id);
      return tell(true);
    }
    if (!this.waiting.includes(id)) this.waiting.push(id);
    tell(false);
  }

  /** Gives the place back (or stops waiting for one). */
  release(id: string) {
    this.callbacks.delete(id);
    this.waiting = this.waiting.filter((w) => w !== id);
    if (!this.active.delete(id)) return;
    const next = this.waiting.shift();
    if (next === undefined) return;
    this.active.add(next);
    this.callbacks.get(next)?.(true);
  }
}

/** The registry the panel uses unless a canvas or a test gives another. */
export const defaultBudget = new WebglBudget();
