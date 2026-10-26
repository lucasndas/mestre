import type { ErrorBody } from "../gen/panel";

// The error of the API: the code is stable and the front end translates it by
// the code and the params; message is the Portuguese fallback of the server.
export class ApiError extends Error {
  readonly code: string;
  readonly params: Record<string, unknown>;
  readonly status: number;

  constructor(status: number, code: string, message: string, params: Record<string, unknown> = {}) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.params = params;
  }
}

/** The code of an error the server never sent: the network failed. */
export const NETWORK = "network";

let onUnauthenticated: () => void = () => {};

/** The shell sets what happens when the server says the session is gone. */
export function setUnauthenticatedHandler(fn: () => void) {
  onUnauthenticated = fn;
}

export interface CallOptions {
  /** The PIN of a sensitive action goes in the very request of the action. */
  pin?: string;
  /** A 401 is the answer the screen expects (the login itself): do not redirect. */
  expect401?: boolean;
}

export async function api<T>(
  method: string,
  path: string,
  body?: unknown,
  opts: CallOptions = {},
): Promise<T> {
  const headers: Record<string, string> = {};
  // a form (the files of a message) goes as it is: the browser writes the content type, with the boundary
  const isForm = typeof FormData !== "undefined" && body instanceof FormData;
  if (body !== undefined && !isForm) headers["Content-Type"] = "application/json";
  if (opts.pin) headers["X-Mestre-Pin"] = opts.pin;
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : isForm ? body : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, NETWORK, "network");
  }
  if (res.ok) {
    return (res.status === 204 ? undefined : await res.json()) as T;
  }
  let err = new ApiError(res.status, "internal", res.statusText);
  try {
    const b = (await res.json()) as ErrorBody;
    err = new ApiError(res.status, b.error.code, b.error.message, b.error.params ?? {});
  } catch {
    // no JSON body: keep the generic error
  }
  if (res.status === 401 && err.code === "unauthenticated" && !opts.expect401) onUnauthenticated();
  throw err;
}
