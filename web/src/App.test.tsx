import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { App } from "./App";

test("the placeholder page names the product", () => {
  render(<App />);
  expect(screen.getByRole("heading", { name: "Mestre" })).toBeTruthy();
});
