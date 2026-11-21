import { describe, expect, test } from "bun:test";
import { fit, keyAction, maxScale, minScale, panBy, zoomBy } from "./viewport";

describe("viewport", () => {
  test("fit shrinks a large drawing, centres a small one and never zooms in", () => {
    const big = fit(2000, 1000, 1000, 500);
    expect(big.scale).toBe(0.5);
    expect([big.x, big.y]).toEqual([0, 0]);
    const small = fit(100, 100, 500, 300);
    expect(small.scale).toBe(1);
    expect([small.x, small.y]).toEqual([200, 100]);
  });

  test("fit of an empty drawing or box is the identity", () => {
    expect(fit(0, 0, 500, 500)).toEqual({ x: 0, y: 0, scale: 1 });
    expect(fit(10, 10, 0, 0)).toEqual({ x: 0, y: 0, scale: 1 });
  });

  test("zoom keeps the point at the centre of the box in place", () => {
    const v = { x: 30, y: 20, scale: 1 };
    const z = zoomBy(v, 2, 400, 200);
    const world = (vp: typeof v): number[] => [(200 - vp.x) / vp.scale, (100 - vp.y) / vp.scale];
    expect(world(z)).toEqual(world(v));
    expect(z.scale).toBe(2);
  });

  test("zoom stops at the limits", () => {
    expect(zoomBy({ x: 0, y: 0, scale: maxScale }, 2, 100, 100).scale).toBe(maxScale);
    expect(zoomBy({ x: 0, y: 0, scale: minScale }, 0.5, 100, 100).scale).toBe(minScale);
  });

  test("keys pan, zoom and fit; other keys are not owned", () => {
    const v = { x: 0, y: 0, scale: 1 };
    expect(keyAction("ArrowRight", v, 400, 300, 800, 600)?.x).toBeLessThan(0);
    expect(keyAction("ArrowDown", v, 400, 300, 800, 600)?.y).toBeLessThan(0);
    expect(keyAction("+", v, 400, 300, 800, 600)?.scale).toBeGreaterThan(1);
    expect(keyAction("-", v, 400, 300, 800, 600)?.scale).toBeLessThan(1);
    expect(keyAction("0", v, 400, 300, 800, 600)?.scale).toBe(0.5);
    expect(keyAction("a", v, 400, 300, 800, 600)).toBeNull();
    expect(panBy(v, 5, 6)).toEqual({ x: 5, y: 6, scale: 1 });
  });
});
