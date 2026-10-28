import { expect, test } from "vitest";
import { setupI18n } from "../i18n";
import { ApiError } from "./api";
import { errorText } from "./errors";
import { formatMoment, isMessageCode, messageText } from "./messages";

const refusal = new ApiError(
  422,
  "decree.scope_required",
  "Sentence of the server, in some language",
  {},
);

// CA-MSG-05
test("a refusal of the engine is said from the generated catalog by its code, not from the message of the server", () => {
  const pt = errorText(setupI18n("pt-BR").getFixedT(null, "errors"), refusal);
  expect(pt).toBe(messageText("pt-BR", "decree.scope_required"));
  expect(pt).toContain("escopo");
  expect(pt).not.toContain("Sentence of the server");
  const en = errorText(setupI18n("en").getFixedT(null, "errors"), refusal);
  expect(en).toBe(messageText("en", "decree.scope_required"));
  expect(en).toContain("scope");
});

test("a code of the API keeps its text in the catalog of the panel, and a code nobody knows says the message", () => {
  const t = setupI18n("en").getFixedT(null, "errors");
  expect(errorText(t, new ApiError(404, "not_found", "x"))).not.toBe("x");
  expect(errorText(t, new ApiError(409, "canvas.from_the_future", "Words of the engine"))).toBe(
    "Words of the engine",
  );
});

test("the level of a refusal is named by the catalog of the engine", () => {
  const err = new ApiError(403, "forbidden_level", "x", { need: "owner", have: "approver" });
  expect(errorText(setupI18n("pt-BR").getFixedT(null, "errors"), err)).toContain(
    "Senhor da Guilda",
  );
  expect(errorText(setupI18n("en").getFixedT(null, "errors"), err)).toContain("Guildmaster");
});

test("plural, a param that is another message, a list and {{x}} inside a text come out as the Go side makes them", () => {
  expect(isMessageCode("decree.scope_required")).toBe(true);
  expect(isMessageCode("nothing.here")).toBe(false);
  expect(
    messageText("en", "level.required", {
      need: { code: "level.owner" },
      have: { code: "level.approver" },
    }),
  ).toBe("Requires the Guildmaster level; yours is Approver.");
  expect(
    messageText("pt-BR", "canvas.automatic_connection", { reasons: ["mestre", "gm"] }),
  ).toContain("mestre, gm");
  expect(messageText("en", "workspace.repo_taken", { slug: "{{x}} {{slug}}" })).toContain(
    "{{x}} {{slug}}",
  );
  expect(messageText("fr", "level.owner")).toBe("Senhor da Guilda");
});

test("an instant comes out in the zone of the Guild, in the form of each language", () => {
  const at = "2026-10-26T02:59:00Z";
  expect(formatMoment(at, "pt-BR", "America/Sao_Paulo")).toBe(
    "25/10/2026 23:59 (America/Sao_Paulo, UTC-03:00)",
  );
  expect(formatMoment(at, "en", "America/Sao_Paulo")).toBe(
    "2026-10-25 23:59 (America/Sao_Paulo, UTC-03:00)",
  );
  expect(formatMoment(at, "en", "UTC")).toBe("2026-10-26 02:59 UTC");
});
