#!/usr/bin/env node
/**
 * dadi — invoke grantable Dimaag registry tools over HTTP.
 *
 * Requires DIMAAG_URL (e.g. http://dimaag.dadi). No default.
 *
 *   dadi help
 *   dadi help browser_spawn
 *   dadi browser_spawn --help
 *   dadi nas_get_logs --services dimaag --level error
 */

const baseUrl = process.env.DIMAAG_URL;
if (!baseUrl || !baseUrl.trim()) {
  console.error("dadi: DIMAAG_URL is required");
  process.exit(1);
}

const root = baseUrl.replace(/\/$/, "");

/**
 * Parse argv into { tool, help, input }.
 * @param {string[]} argv
 */
function parseArgs(argv) {
  const args = [...argv];
  if (args.length === 0 || args[0] === "help" || args[0] === "--help" || args[0] === "-h") {
    const tool = args[0] === "help" ? args[1] : undefined;
    return { tool: tool ?? null, help: true, input: {} };
  }

  const tool = args.shift();
  if (!tool) {
    return { tool: null, help: true, input: {} };
  }

  if (args.includes("help") || args.includes("--help") || args.includes("-h")) {
    return { tool, help: true, input: {} };
  }

  /** @type {Record<string, unknown>} */
  const input = {};
  for (let i = 0; i < args.length; i++) {
    const token = args[i];
    if (!token.startsWith("--")) {
      console.error(`dadi: unexpected argument ${token} (use --key value)`);
      process.exit(2);
    }
    const body = token.slice(2);
    if (body.includes("=")) {
      const eq = body.indexOf("=");
      const key = body.slice(0, eq);
      const value = body.slice(eq + 1);
      input[key] = coerce(value);
      continue;
    }
    const key = body;
    const next = args[i + 1];
    if (next === undefined || next.startsWith("--")) {
      input[key] = true;
      continue;
    }
    input[key] = coerce(next);
    i++;
  }
  return { tool, help: false, input };
}

/**
 * @param {string} value
 */
function coerce(value) {
  if (value === "true") return true;
  if (value === "false") return false;
  if (value === "null") return null;
  if (/^-?\d+(\.\d+)?$/.test(value)) return Number(value);
  return value;
}

/**
 * @param {string} path
 * @param {RequestInit} [init]
 */
async function api(path, init) {
  const res = await fetch(`${root}${path}`, {
    ...init,
    headers: {
      accept: "application/json",
      ...(init?.body ? { "content-type": "application/json" } : {}),
      ...(init?.headers ?? {}),
    },
  });
  const text = await res.text();
  let body;
  try {
    body = text ? JSON.parse(text) : null;
  } catch {
    body = text;
  }
  if (!res.ok) {
    const msg =
      body && typeof body === "object" && body.error?.message
        ? body.error.message
        : typeof body === "string"
          ? body
          : res.statusText;
    console.error(`dadi: ${res.status} ${msg}`);
    process.exit(1);
  }
  return body;
}

/**
 * @param {unknown} schema
 * @param {string} indent
 */
function formatSchema(schema, indent = "  ") {
  if (!schema || typeof schema !== "object") return "";
  const props = schema.properties ?? {};
  const required = new Set(schema.required ?? []);
  const keys = Object.keys(props);
  if (keys.length === 0) return `${indent}(no parameters)\n`;
  let out = "";
  for (const key of keys) {
    const prop = props[key] ?? {};
    const typ = Array.isArray(prop.type) ? prop.type.join("|") : (prop.type ?? "any");
    const req = required.has(key) ? "required" : "optional";
    const desc = prop.description ? ` — ${prop.description}` : "";
    out += `${indent}--${key} (${typ}, ${req})${desc}\n`;
  }
  return out;
}

async function main() {
  const { tool, help, input } = parseArgs(process.argv.slice(2));

  if (help && !tool) {
    const { tools } = await api("/tools");
    console.log("dadi <tool> [--key value …]");
    console.log("dadi help [tool]");
    console.log("");
    for (const t of tools) {
      console.log(`  ${t.name}`);
      console.log(`    ${t.description}`);
    }
    return;
  }

  if (help && tool) {
    const detail = await api(`/tools/${encodeURIComponent(tool)}`);
    console.log(detail.name);
    console.log(detail.description);
    console.log("Parameters:");
    process.stdout.write(formatSchema(detail.input_schema));
    return;
  }

  if (!tool) {
    console.error("dadi: missing tool name");
    process.exit(2);
  }

  const result = await api(`/tools/${encodeURIComponent(tool)}/execute`, {
    method: "POST",
    body: JSON.stringify(input),
  });

  let content = result.content;
  try {
    content = JSON.parse(result.content);
  } catch {
    /* keep string */
  }
  const rendered = typeof content === "string" ? content : JSON.stringify(content, null, 2);
  if (result.is_error) {
    console.error(rendered);
    process.exit(1);
  }
  console.log(rendered);
}

main().catch((err) => {
  console.error(`dadi: ${err instanceof Error ? err.message : String(err)}`);
  process.exit(1);
});
