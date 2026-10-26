import { expect, test } from "vitest";
import { isTerminalReply } from "./replies";

test("what xterm.js answers to the program is not typing, and what a person types is", () => {
  for (const reply of [
    "\x1b[12;40R", // cursor position
    "\x1b[?1;2c", // primary device attributes
    "\x1b[>0;276;0c", // secondary device attributes
    "\x1b[0n", // status
    "\x1b[?2026;2$y", // mode report
    "\x1b[8;50;200t", // window size
    "\x1b]11;rgb:1f1f/1a1a/1414\x1b\\", // OSC color, ST
    "\x1b]10;rgb:efef/e6e6/d2d2\x07", // OSC color, BEL
    "\x1bP1$r0m\x1b\\", // DCS status string
    "\x1b[I", // focus in
  ]) {
    expect(isTerminalReply(reply), JSON.stringify(reply)).toBe(true);
  }
  for (const typed of [
    "ls\r",
    "a",
    "\x1b[A",
    "\x1b[3~",
    "\x1b[1;5C",
    "\x1b[Z",
    "\x7f",
    "\x03",
    "olá",
  ]) {
    expect(isTerminalReply(typed), JSON.stringify(typed)).toBe(false);
  }
});
