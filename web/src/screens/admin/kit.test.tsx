import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactElement } from "react";
import { I18nextProvider } from "react-i18next";
import { expect, test, vi } from "vitest";
import { setupI18n } from "../../i18n";
import { ApiError } from "../../lib/api";
import { meQuery } from "../../lib/queries";
import { ME } from "../../test-utils";
import { EmptyState, LevelGate, LoadError, StatusStrip, VocabLine } from "./kit";

function show(ui: ReactElement, level = "owner", language: "pt-BR" | "en" = "pt-BR") {
  const qc = new QueryClient();
  qc.setQueryData(meQuery.queryKey, { ...ME, user: { ...ME.user, level } });
  return render(
    <I18nextProvider i18n={setupI18n(language)}>
      <QueryClientProvider client={qc}>{ui}</QueryClientProvider>
    </I18nextProvider>,
  );
}

// CA-ADM-17
test("the status strip says the tone with a word and a symbol, and holds the one main action", () => {
  show(
    <StatusStrip
      tone="warn"
      title="Nenhuma conta de IA entrou ainda"
      hint="Sem conta, nenhum agente roda."
    >
      <button type="button">Entrar em outra conta</button>
    </StatusStrip>,
  );
  const strip = screen.getByRole("status");
  expect(strip.textContent).toContain("Falta algo: Nenhuma conta de IA entrou ainda");
  expect(strip.textContent).toContain("!");
  expect(screen.getByRole("button", { name: "Entrar em outra conta" })).toBeTruthy();
});

// CA-ADM-17
test("the empty state names the thing, why it matters and the next step", () => {
  show(
    <EmptyState
      title="Nenhum App ainda"
      why="Um App é o robô do Mestre no GitHub."
      steps={["Gerar o link", "Abrir no navegador"]}
    >
      <button type="button">Gerar o link</button>
    </EmptyState>,
  );
  expect(screen.getByRole("heading", { name: "Nenhum App ainda" })).toBeTruthy();
  expect(screen.getAllByRole("listitem")).toHaveLength(2);
  expect(screen.getByRole("button", { name: "Gerar o link" })).toBeTruthy();
});

// CA-ADM-17
test("the vocabulary line gives each word as a term with its help", async () => {
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  show(<VocabLine terms={["Perfil de conta"]} />);
  expect(screen.getByText("Vocabulário:")).toBeTruthy();
  await user.click(screen.getByRole("button", { name: /Perfil de conta/ }));
  expect((await screen.findByRole("dialog")).textContent).toContain("No mundo real");
});

test("the mark of the vocabulary trails its term, so a wrapped line never starts with it", () => {
  show(<VocabLine terms={["Perfil de conta", "Classe"]} />);
  const marks = screen.getAllByText("·");
  expect(marks).toHaveLength(1);
  expect(marks[0]?.previousElementSibling?.textContent).toContain("Perfil de conta");
});

// CA-ADM-18
test("a failed read shows what, why in the language, a retry and the raw answer folded away", async () => {
  const user = userEvent.setup({ delay: null, pointerEventsCheck: 0 });
  const retry = vi.fn();
  show(
    <LoadError
      what="as contas"
      error={new ApiError(500, "internal", "boom: db locked")}
      onRetry={retry}
    />,
  );
  expect(screen.getByRole("alert").textContent).toContain("Não foi possível carregar as contas.");
  const details = screen.getByText("Detalhes técnicos").closest("details");
  expect(details?.open).toBe(false);
  expect(details?.textContent).toContain("internal: boom: db locked");
  await user.click(screen.getByRole("button", { name: "Tentar de novo" }));
  expect(retry).toHaveBeenCalledOnce();
});

// CA-ADM-25
test("a button that needs a level is shown off with the level it needs and the user's own", () => {
  show(
    <LevelGate need="owner" label="Cadastrar">
      <button type="button">Cadastrar</button>
    </LevelGate>,
    "administrator",
  );
  expect((screen.getByRole("button", { name: "Cadastrar" }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect(screen.getByText("Exige o nível Senhor da Guilda; o seu é Administrador.")).toBeTruthy();
});

// CA-ADM-25
test("who reaches the level gets the action itself", () => {
  show(
    <LevelGate need="administrator" label="Cadastrar">
      <button type="button">Cadastrar</button>
    </LevelGate>,
    "owner",
  );
  expect((screen.getByRole("button", { name: "Cadastrar" }) as HTMLButtonElement).disabled).toBe(
    false,
  );
  expect(screen.queryByText(/Exige o nível/)).toBeNull();
});

// CA-ADM-25
test("a screen above the level of the user is a refusal that says the level, not a read that failed", () => {
  show(
    <LoadError
      what="os Apps do GitHub"
      error={new ApiError(403, "forbidden_level", "x", { need: "owner", have: "approver" })}
      onRetry={() => undefined}
    />,
    "approver",
  );
  const alert = screen.getByRole("alert");
  expect(alert.textContent).toContain(
    "Esta ação exige o nível Senhor da Guilda; o seu é Aprovador.",
  );
  expect(alert.textContent).not.toContain("Não foi possível carregar");
  expect(screen.queryByRole("button", { name: "Tentar de novo" })).toBeNull();
});
