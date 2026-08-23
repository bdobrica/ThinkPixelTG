#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

const policy = readFileSync("dependency-policy.yaml", "utf8");
const sourceBlock = policy.match(/allowed_module_sources:\n((?:  - .+\n)+)/)?.[1] ?? "";
const hosts = [...sourceBlock.matchAll(/^  - ([a-z0-9.-]+)$/gm)].map((match) => match[1]);
if (hosts.length === 0) throw new Error("dependency policy has no allowed sources");

const modules = execFileSync("go", ["list", "-m", "-f", "{{.Main}} {{.Path}}", "all"], { encoding: "utf8" });
for (const line of modules.trim().split("\n")) {
  const [main, modulePath] = line.split(" ");
  if (main === "true" || !modulePath?.includes(".")) continue;
  if (!hosts.some((host) => modulePath === host || modulePath.startsWith(`${host}/`))) {
    throw new Error(`module source is not allowed: ${modulePath}`);
  }
}

const exceptions = readFileSync("docs/development/dependency-exceptions.yaml", "utf8");
for (const match of exceptions.matchAll(/^\s+expires: ['\"]?(\d{4}-\d{2}-\d{2})/gm)) {
  if (new Date(`${match[1]}T23:59:59Z`) < new Date()) {
    throw new Error(`dependency exception expired: ${match[1]}`);
  }
}

console.log("dependency policy: sources and exceptions valid");
