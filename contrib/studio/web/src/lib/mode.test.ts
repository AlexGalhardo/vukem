import { describe, expect, test } from "bun:test";
import { formatCount, formatTTL, modeBadge } from "./mode";

describe("modeBadge", () => {
  test("says read-only when bootstrap.write is false", () => {
    expect(modeBadge({ write: false })).toEqual({
      label: "Read-only",
      tone: "readonly",
      hint: "Studio cannot change data. Start it with --write to edit.",
    });
  });

  test("says write enabled when bootstrap.write is true", () => {
    const badge = modeBadge({ write: true });
    expect(badge.label).toBe("Write enabled");
    expect(badge.tone).toBe("write");
  });
});

describe("formatCount", () => {
  test("groups thousands and names an unknown estimate", () => {
    expect(formatCount(1234567)).toBe("1,234,567");
    expect(formatCount(0)).toBe("0");
    expect(formatCount(-1)).toBe("unknown");
  });
});

describe("formatTTL", () => {
  test("uses the largest whole unit", () => {
    expect(formatTTL(-1)).toBe("no expiry");
    expect(formatTTL(45)).toBe("45s");
    expect(formatTTL(60)).toBe("1m");
    expect(formatTTL(7200)).toBe("2h");
    expect(formatTTL(172800)).toBe("2d");
  });
});
