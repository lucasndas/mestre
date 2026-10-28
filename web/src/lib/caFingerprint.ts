/** The route that serves the certificate of the Mestre CA (the bytes the installer hashes). */
export const CA_PATH = "/ca.crt";

/** Lowercase hexadecimal, the way `sha256sum` prints it. */
export function toHex(bytes: ArrayBuffer): string {
  return Array.from(new Uint8Array(bytes), (b) => b.toString(16).padStart(2, "0")).join("");
}

/** The same value in groups of eight, which is easier to read aloud and to compare by eye. */
export function groupHex(hex: string): string {
  return hex.match(/.{1,8}/g)?.join(" ") ?? hex;
}

/**
 * The check code of the certificate: the SHA-256 of what `/ca.crt` serves, computed here in the
 * browser and never typed by a person (docs/painel/crescer-a-guilda.md#1-convidar-uma-pessoa).
 * It rejects when the browser cannot hash or the route does not answer, so the screen never
 * hands out an invite without a value it computed.
 */
export async function caCheckCode(): Promise<string> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) throw new Error("crypto.subtle is not available");
  const res = await fetch(CA_PATH, { cache: "no-store" });
  if (!res.ok) throw new Error(`${CA_PATH}: ${res.status}`);
  return toHex(await subtle.digest("SHA-256", await res.arrayBuffer()));
}
