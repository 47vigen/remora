import { validatePassword } from "./password";

test("validatePassword", () => {
  expect(validatePassword({ current: "", next: "x".repeat(12), confirm: "x".repeat(12) }).current).toBe(
    "Enter your current password.",
  );
  expect(validatePassword({ current: "a", next: "short", confirm: "short" }).next).toBe("Use at least 12 characters.");
  expect(validatePassword({ current: "a", next: "x".repeat(12), confirm: "y".repeat(12) }).confirm).toBe(
    "Passwords do not match.",
  );
  expect(validatePassword({ current: "a", next: "a", confirm: "a" }).next).toBeDefined(); // same as current and too short
  expect(validatePassword({ current: "z".repeat(12), next: "z".repeat(12), confirm: "z".repeat(12) }).next).toBe(
    "Choose a password different from the current one.",
  );
  expect(validatePassword({ current: "a", next: "x".repeat(12), confirm: "x".repeat(12) })).toEqual({});
});
