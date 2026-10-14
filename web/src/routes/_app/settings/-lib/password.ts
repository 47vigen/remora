/** The server's minimum; it refuses a shorter password with 400. */
export const MIN_PASSWORD = 12;

export function validatePassword(p: {
  current: string;
  next: string;
  confirm: string;
}): Partial<Record<"current" | "next" | "confirm", string>> {
  const errors: Partial<Record<"current" | "next" | "confirm", string>> = {};
  if (p.current === "") errors.current = "Enter your current password.";
  if (p.next.length < MIN_PASSWORD) errors.next = `Use at least ${MIN_PASSWORD} characters.`;
  else if (p.next === p.current) errors.next = "Choose a password different from the current one.";
  if (p.confirm !== p.next) errors.confirm = "Passwords do not match.";
  return errors;
}
