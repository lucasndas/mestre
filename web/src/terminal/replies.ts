// xterm.js answers by itself the questions a program prints (where is the cursor, what
// terminal are you, what are your colors). Those answers come out of onData like a key
// would, but nobody typed them: the output of the agent asked for them. Sent as input
// they would put bytes of the agent's choosing in the PTY under the name of the person
// who is typing, so they are never sent (the machine's own emulator answers its programs).
const ESC = String.fromCharCode(27);
const BEL = String.fromCharCode(7);
const REPLIES = [
  // cursor position and other CSI reports: ESC [ ... R, device attributes ... c,
  // status reports ... n, mode reports ... $y, window reports ... t, focus in/out
  `^${ESC}\\[[?>=]?[\\d;:]*[Rcnt]$`,
  `^${ESC}\\[[?>=]?[\\d;:]*\\$y$`,
  `^${ESC}\\[[IO]$`,
  // OSC (colors, titles) and DCS (status strings) answers, ended by BEL or ST
  `^${ESC}\\][^${BEL}${ESC}]*(${BEL}|${ESC}\\\\)$`,
  `^${ESC}P[^${ESC}]*${ESC}\\\\$`,
].map((r) => new RegExp(r));

/** True for what xterm.js answers to the program; false for what a person typed. */
export function isTerminalReply(data: string): boolean {
  return REPLIES.some((r) => r.test(data));
}
