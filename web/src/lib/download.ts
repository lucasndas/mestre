import type { ErrorBody } from "../gen/panel";
import { ApiError } from "./api";

/**
 * Downloads a file the API serves (an export): the request carries the session like every
 * other, and the browser saves the body under the name the server suggested.
 */
export async function download(path: string, fallbackName: string): Promise<void> {
  let res: Response;
  try {
    res = await fetch(path, { credentials: "same-origin" });
  } catch {
    throw new ApiError(0, "network", "network");
  }
  if (!res.ok) {
    let err = new ApiError(res.status, "internal", res.statusText);
    try {
      const b = (await res.json()) as ErrorBody;
      err = new ApiError(res.status, b.error.code, b.error.message, b.error.params ?? {});
    } catch {
      // no JSON body: keep the generic error
    }
    throw err;
  }
  const name = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") ?? "")?.[1];
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement("a");
  a.href = url;
  a.download = name ?? fallbackName;
  document.body.append(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
