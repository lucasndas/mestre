import { expect, test } from "vitest";
import { decodeFrame, FRAME_TERMINAL, ulidOf } from "../lib/ws";
import { xtermFactory } from "./factory";
import { OutputCursor } from "./stream";
import fixture from "./testdata/replay.json";

/** The binary frame the Citadel sends the browser (internal/panel): type, id, offset, data. */
function encodeFrame(idBytes: Uint8Array, offset: number, data: Uint8Array): ArrayBuffer {
  const buf = new ArrayBuffer(25 + data.length);
  const u = new Uint8Array(buf);
  u[0] = FRAME_TERMINAL;
  u.set(idBytes, 1);
  new DataView(buf).setBigUint64(17, BigInt(offset), false);
  u.set(data, 25);
  return buf;
}

const idBytes = Uint8Array.from({ length: 16 }, (_, i) => i * 7 + 1);
const enc = (s: string) => new TextEncoder().encode(s);

test("a frame is decoded to its terminal id as the 26-character ULID, its offset and its data", () => {
  const f = decodeFrame(encodeFrame(idBytes, 300 * 1024, new Uint8Array([1, 2, 3])));
  expect(f?.type).toBe(FRAME_TERMINAL);
  expect(f?.id).toBe(ulidOf(idBytes));
  expect(f?.id).toHaveLength(26);
  expect(f?.offset).toBe(300 * 1024);
  expect([...(f?.data ?? [])]).toEqual([1, 2, 3]);
  expect(decodeFrame(new ArrayBuffer(10))).toBeNull();
  // as the Go side prints ids (internal/protocol ULID.String): all zero and all ones
  expect(ulidOf(new Uint8Array(16))).toBe("0".repeat(26));
  expect(ulidOf(new Uint8Array(16).fill(255))).toBe(`7${"Z".repeat(25)}`);
});

// CA-CANVAS-09: the replay the panel receives draws, in xterm.js, the same rows the
// emulator of the machine has (the fixture is made by Go: internal/panel
// terminals_fixture_test.go).
test("CA-CANVAS-09: the rows xterm.js draws from the replay are the rows of the emulator of the machine", async () => {
  const data = Uint8Array.from(atob(fixture.data_base64), (c) => c.charCodeAt(0));
  const term = xtermFactory.snapshot({ cols: fixture.cols, rows: fixture.rows });
  const cursor = new OutputCursor({
    write: (d) => term.write(d),
    reset: () => term.reset(),
    lost: () => {
      throw new Error("nothing was lost");
    },
  });
  // frames of 41 new bytes, each one repeating the last 20 of the one before (a retransmission)
  for (let sent = 0; sent < data.length; sent += 41) {
    const from = Math.max(0, sent - 20);
    const frame = decodeFrame(encodeFrame(idBytes, from, data.subarray(from, sent + 41)));
    if (!frame) throw new Error("bad frame");
    cursor.push(frame.offset, frame.data);
  }
  expect(cursor.offset).toBe(data.length);
  await term.settle();
  expect(term.rows()).toEqual(fixture.screen);
  term.dispose();
});

test("a stream that starts past offset 0, or jumps, is a lost stretch; a screen picture and a reset start over", () => {
  const writes: string[] = [];
  let lost = 0;
  const c = new OutputCursor({
    write: (d) => writes.push(typeof d === "string" ? d : new TextDecoder().decode(d)),
    reset: () => writes.push("<reset>"),
    lost: () => lost++,
  });
  c.push(100, enc("abc")); // the buffer begins at 100: what is before it is lost
  expect(lost).toBe(1);
  c.push(103, enc("def"));
  c.push(104, enc("ef")); // already written
  c.push(110, enc("xyz")); // a hole
  expect(lost).toBe(2);
  expect(writes).toEqual(["abc", "def", "xyz"]);
  c.screen("one\ntwo", 500);
  expect(writes.slice(3)).toEqual(["<reset>", "one\r\ntwo"]);
  c.push(500, enc("!"));
  expect(writes.at(-1)).toBe("!");
  expect(lost).toBe(2);
  c.reset();
  expect(c.offset).toBe(0);
});
