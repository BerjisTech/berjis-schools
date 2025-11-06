// Minimal JSON value types for safer 'any' replacement
export type JSONPrimitive = string | number | boolean | null;
export type JSONObject = Record<string, unknown>;
export type JSONValue = JSONPrimitive | JSONObject | unknown[];
export type JSONArray = unknown[];

// Helper for loose API blobs where only top-level is known
export type JSONRecord = Record<string, unknown>;
