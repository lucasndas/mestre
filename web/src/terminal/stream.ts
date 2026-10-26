/** What a terminal drawn from the stream must offer: bytes in, and a way to start over. */
export interface StreamSink {
  write(data: Uint8Array | string): void;
  reset(): void;
  /** A stretch of the output is missing (it left the buffer of the machine, or was dropped). */
  lost(): void;
}

/**
 * Applies the output of a terminal to a sink, in order and without repeating a byte.
 *
 * After each (re)subscribe the Citadel sends the buffer of the machine from offset 0
 * and then the live output, contiguous. The cursor keeps the offset it expects next:
 * what is before it is trimmed (a retransmission), and a frame past it is a lost
 * stretch. docs/painel/canvas.md#terminais-no-canvas.
 */
export class OutputCursor {
  private next = 0;
  private started = false;

  constructor(private readonly sink: StreamSink) {}

  /** The offset the cursor expects next. */
  get offset(): number {
    return this.next;
  }

  /** The stream starts again from offset 0 (a new subscription). */
  reset() {
    this.next = 0;
    this.started = false;
    this.sink.reset();
  }

  push(offset: number, data: Uint8Array) {
    if (!this.started) {
      this.started = true;
      if (offset > 0) this.sink.lost();
      this.next = offset;
    } else if (offset > this.next) {
      this.sink.lost();
      this.next = offset;
    }
    const end = offset + data.length;
    if (end <= this.next) return;
    const fresh = offset < this.next ? data.subarray(this.next - offset) : data;
    this.sink.write(fresh);
    this.next = end;
  }

  /** The picture of the screen a slow browser gets instead of what it lost; the frames go on from offset. */
  screen(text: string, offset: number) {
    this.sink.reset();
    this.sink.write(text.split("\n").join("\r\n"));
    this.next = offset;
    this.started = true;
  }
}
