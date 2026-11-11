// Internal decoding primitives shared by types.ts and events.ts. Not exported from the
// package entry point; callers use the typed parse* functions instead.

// Thrown for any value that does not satisfy docs/contracts.md. The message names only
// the field, never the rejected value, so private text cannot leak into logs.
export class ContractValidationError extends Error {
  readonly field: string;
  constructor(field: string) {
    super(`Invalid contract field: ${field}`);
    this.field = field;
    this.name = "ContractValidationError";
  }
}

export const invalid = (field: string): never => {
  throw new ContractValidationError(field);
};

export function object(value: unknown, field: string): Record<string, unknown> {
  if (!value || typeof value !== "object" || Array.isArray(value))
    return invalid(field);
  return value as Record<string, unknown>;
}

export function text(
  value: unknown,
  field: string,
  max: number,
  empty = false,
): string {
  if (
    typeof value !== "string" ||
    value.length > max ||
    (!empty && !value.trim())
  )
    return invalid(field);
  return value;
}

export function integer(
  value: unknown,
  field: string,
  min = 0,
  max = Number.MAX_SAFE_INTEGER,
): number {
  if (
    typeof value !== "number" ||
    !Number.isSafeInteger(value) ||
    value < min ||
    value > max
  )
    return invalid(field);
  return value;
}

export function boolean(value: unknown, field: string): boolean {
  if (typeof value !== "boolean") return invalid(field);
  return value;
}

export function choice<const T extends readonly string[]>(
  value: unknown,
  field: string,
  choices: T,
): T[number] {
  if (typeof value !== "string" || !choices.includes(value))
    return invalid(field);
  return value as T[number];
}

export function uuid(value: unknown, field: string): string {
  const result = text(value, field, 36);
  if (
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(
      result,
    )
  )
    return invalid(field);
  return result;
}

// RFC 3339 with an explicit offset. `utc` additionally requires the `Z` form used for
// server metadata; business preferences may carry a local offset.
export function date(value: unknown, field: string, utc = false): string {
  const result = text(value, field, 40);
  const match =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/.exec(
      result,
    );
  if (
    !match ||
    !Number.isFinite(Date.parse(result)) ||
    (utc && !result.endsWith("Z"))
  )
    return invalid(field);
  const year = Number(match[1]),
    month = Number(match[2]),
    day = Number(match[3]);
  const days = new Date(Date.UTC(year, month, 0)).getUTCDate();
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > days ||
    Number(match[4]) > 23 ||
    Number(match[5]) > 59 ||
    Number(match[6]) > 59
  )
    return invalid(field);
  return result;
}

export function zone(value: unknown, field: string): string {
  const result = text(value, field, 100);
  try {
    new Intl.DateTimeFormat("en", { timeZone: result });
  } catch {
    return invalid(field);
  }
  return result;
}

export function phone(value: unknown, field: string): string {
  const result = text(value, field, 16);
  if (!/^\+[1-9]\d{1,14}$/.test(result)) return invalid(field);
  return result;
}

export function strings(
  value: unknown,
  field: string,
  maxItems: number,
  maxText: number,
): string[] {
  if (!Array.isArray(value) || value.length > maxItems) return invalid(field);
  return value.map((item, i) => text(item, `${field}[${i}]`, maxText));
}

export function optional<T>(
  value: unknown,
  read: (value: unknown) => T,
): T | undefined {
  return value === undefined ? undefined : read(value);
}

// Rejects fields the contract does not define, so a typo in a request is a 400 rather
// than a silently dropped value. Responses from the server are decoded leniently instead.
export function exact(
  v: Record<string, unknown>,
  field: string,
  allowed: readonly string[],
): void {
  for (const key of Object.keys(v))
    if (!allowed.includes(key)) invalid(`${field}.${key}`);
}
