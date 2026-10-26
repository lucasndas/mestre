import type { FullResult, Reporter, TestCase, TestResult } from "@playwright/test/reporter";

// The e2e smoke has a budget of its own: the time of the tests, summed, apart
// from starting the engine (`go run` compiles it first). Over the budget the
// run fails and the slowest tests are listed (docs/qualidade/testes.md#smoke-do-painel).
export const E2E_BUDGET_MS = 60_000;

export default class BudgetReporter implements Reporter {
  private readonly times: { title: string; ms: number }[] = [];

  onTestEnd(test: TestCase, result: TestResult): void {
    this.times.push({ title: test.titlePath().slice(1).join(" › "), ms: result.duration });
  }

  async onEnd(result: FullResult): Promise<{ status?: FullResult["status"] } | undefined> {
    const total = this.times.reduce((sum, t) => sum + t.ms, 0);
    const seconds = (total / 1000).toFixed(1);
    if (total <= E2E_BUDGET_MS) {
      console.log(
        `e2e: ${this.times.length} tests in ${seconds}s (budget ${E2E_BUDGET_MS / 1000}s)`,
      );
      return undefined;
    }
    console.log(
      `FAIL e2e: ${this.times.length} tests in ${seconds}s, budget ${E2E_BUDGET_MS / 1000}s. The slowest:`,
    );
    for (const t of [...this.times].sort((a, b) => b.ms - a.ms).slice(0, 5)) {
      console.log(`  ${(t.ms / 1000).toFixed(2)}s  ${t.title}`);
    }
    return { status: result.status === "passed" ? "failed" : result.status };
  }
}
