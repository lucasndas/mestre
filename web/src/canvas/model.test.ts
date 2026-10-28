import { expect, test } from "vitest";
import { tableState } from "./fixtures";
import { addArrow, draftEdges, emptyDraft, fromQuest, move, remove, toRequest } from "./mission";
import { buildEdges, buildNodes, focusCharacter, groupOf, initialView, layoutItems } from "./model";

test("the nodes of the table are its characters and scrolls, in the places the layout keeps", () => {
  const nodes = buildNodes(tableState());
  expect(nodes.map((n) => n.id).sort()).toEqual([
    "character:dev",
    "character:gm",
    "character:mm",
    "character:rev",
    "scroll:loja/m1/p0001",
  ]);
  const dev = nodes.find((n) => n.id === "character:dev");
  expect(dev?.position).toEqual({ x: 700, y: -100 });
  expect([dev?.width, dev?.height]).toEqual([700, 500]);
});

// CA-CANVAS-06, the part of the screen: the layout the server keeps comes back as the same layout.
test("what is drawn from the layout is saved as the same layout", () => {
  const st = tableState();
  const back = layoutItems(buildNodes(st));
  for (const it of st.layout) {
    const got = back.find((b) => b.key === it.key);
    expect(got, it.key).toMatchObject({ x: it.x, y: it.y });
  }
  expect(back.find((b) => b.key === "character:dev")).toMatchObject({ w: 700, h: 500 });
});

test("groups come first and hold their children at relative positions", () => {
  const st = tableState({
    layout: [
      { key: "character:mm", x: 10, y: 20, parent: "group:g1" },
      { key: "group:g1", label: "Time", x: 100, y: 100, w: 900, h: 700 },
    ],
  });
  const nodes = buildNodes(st);
  expect(nodes[0]).toMatchObject({ id: "group:g1", type: "group", data: { label: "Time" } });
  expect(nodes.find((n) => n.id === "character:mm")).toMatchObject({
    parentId: "group:g1",
    position: { x: 10, y: 20 },
  });
  // a node whose group is gone is a free node
  const orphan = buildNodes(
    tableState({ layout: [{ key: "character:mm", x: 1, y: 2, parent: "group:zzz" }] }),
  );
  expect(orphan.find((n) => n.id === "character:mm")?.parentId).toBeUndefined();
});

test("the lines: connections, numbered steps with the current one highlighted, returns and waits", () => {
  const edges = buildEdges(tableState());
  const by = (kind: string) => edges.filter((e) => e.data?.kind === kind);
  expect(by("connection")).toHaveLength(2);
  expect(by("connection").every((e) => e.deletable)).toBe(true); // the engine decides what is refused
  expect(by("step")).toHaveLength(1);
  expect(by("step")[0]).toMatchObject({
    source: "character:dev",
    target: "character:rev",
    data: { label: "2", current: true },
  });
  expect(by("return")[0]).toMatchObject({ source: "character:rev", target: "character:dev" });
  expect(by("wait")[0]).toMatchObject({
    source: "character:rev",
    target: "character:dev",
    data: { label: "Dev-1", deadlock: false },
  });
});

test("a wait that is part of a deadlock is marked", () => {
  const st = tableState({
    waits: [
      { id: "w1", waiter: "rev", target: "dev", kind: "ask", reason: "Dev-1", deadlock: true },
    ],
  });
  expect(buildEdges(st).find((e) => e.data?.kind === "wait")?.data?.deadlock).toBe(true);
});

test("a wait for a time has no line: it is on the node", () => {
  const st = tableState({
    waits: [{ id: "w1", waiter: "rev", kind: "until", reason: "14:00", deadlock: false }],
  });
  expect(buildEdges(st).some((e) => e.data?.kind === "wait")).toBe(false);
});

test("grouping puts the nodes in a box with a margin and moves them into it", () => {
  const nodes = buildNodes(tableState()).filter(
    (n) => n.id === "character:mm" || n.id === "scroll:loja/m1/p0001",
  );
  const made = groupOf(nodes, "group:x", "Time", true);
  expect(made[0]).toMatchObject({ id: "group:x", type: "group" });
  const [group, ...kids] = made;
  for (const k of kids) {
    expect(k.parentId).toBe("group:x");
    expect(k.position.x).toBeGreaterThanOrEqual(0);
    expect(k.position.y).toBeGreaterThanOrEqual(0);
    expect(k.position.x + (k.width ?? 0)).toBeLessThanOrEqual(group?.width ?? 0);
  }
});

test("a mission is drawn by dragging arrows, numbered by the order, and adjusted by moving the steps", () => {
  let d = { ...emptyDraft(), name: "entrega" };
  d = addArrow(d, "Dev-1", "Revisor");
  d = addArrow(d, "Revisor", "QA");
  expect(d.steps.map((s) => [s.id, s.after])).toEqual([
    ["dev-1", []],
    ["revisor", ["dev-1"]],
    ["qa", ["revisor"]],
  ]);
  // an arrow back to an earlier step is a return
  d = addArrow(d, "QA", "Dev-1");
  expect(d.steps[2]?.returnTo).toBe("dev-1");
  // the same arrow twice changes nothing
  expect(addArrow(d, "Dev-1", "Revisor")).toEqual(d);
  // moving a step down swaps the numbers, and the arrows follow the order
  const moved = move(d, "dev-1", 1);
  expect(moved.steps.map((s) => s.id)).toEqual(["revisor", "dev-1", "qa"]);
  expect(moved.steps[0]?.after).toEqual([]);
  expect(moved.steps[1]?.returnTo).toBe("revisor"); // dev-1 came before revisor, now after it: it hands back
  // removing a step takes its arrows
  const r = remove(d, "revisor");
  expect(r.steps.map((s) => s.id)).toEqual(["dev-1", "qa"]);
  expect(r.steps[1]?.after).toEqual([]);
  expect(toRequest(d, true)).toMatchObject({
    name: "entrega",
    run: true,
    steps: [
      { id: "dev-1", character: "Dev-1" },
      { id: "revisor", after: ["dev-1"] },
      { id: "qa", after: ["revisor"], can_return_to: "dev-1" },
    ],
  });
});

test("a draft mission of the table can be adjusted: it loads into the drawing", () => {
  const st = tableState();
  const q = { ...(st.quests[0] as (typeof st.quests)[number]), editable: true };
  const d = fromQuest(q, st);
  expect(d).toMatchObject({
    questId: "q1",
    name: "entrega",
    steps: [
      { id: "dev", character: "Dev-1" },
      { id: "rev", character: "Revisor", after: ["dev"], returnTo: "dev" },
    ],
  });
  expect(draftEdges(d, st).map((e) => e.data?.kind)).toEqual(["step", "return"]);
});

test("the board opens on who works, else on the master of the table", () => {
  const st = tableState();
  expect(focusCharacter(st)).toBe("character:dev");
  st.characters = st.characters.map((c) => ({ ...c, state: "idle" }));
  expect(focusCharacter(st)).toBe("character:mm");
});

test("the board opens on the whole table when it fits the frame, else centered on the focus", () => {
  const nodes = buildNodes(tableState());
  const small = initialView(nodes, "character:dev", { width: 100000, height: 100000 });
  expect(small.nodes).toBeUndefined();
  const tight = initialView(nodes, "character:dev", { width: 800, height: 600 });
  expect(tight.nodes).toEqual([{ id: "character:dev" }]);
  // never shrunk to fit: a table that needs less than 100% opens on the focus
  const bounds = nodes.filter((n) => !n.parentId);
  const w =
    Math.max(...bounds.map((n) => n.position.x + (n.width ?? 0))) -
    Math.min(...bounds.map((n) => n.position.x));
  const h =
    Math.max(...bounds.map((n) => n.position.y + (n.height ?? 0))) -
    Math.min(...bounds.map((n) => n.position.y));
  expect(initialView(nodes, "character:dev", { width: w * 0.7, height: h * 0.7 }).nodes).toEqual([
    { id: "character:dev" },
  ]);
});
